// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

type countingNotifier struct{ n atomic.Int32 }

func (c *countingNotifier) Notify() { c.n.Add(1) }

func setupModelConfigSvcWithOutbox(t *testing.T) (*ModelConfigService, *store.OutboxRepo, *store.DB) {
	t.Helper()
	db := testOpenDB(t)
	svc := NewModelConfigService(store.NewModelConfigRepo(db), slog.Default())
	svc.SetFeatureRegistry(decisionNodesOn())
	outboxRepo := store.NewOutboxRepo(db)
	svc.WithOutbox(outboxRepo)
	return svc, outboxRepo, db
}

func startModelOutboxRelay(t *testing.T, svc *ModelConfigService, outboxRepo *store.OutboxRepo, brk broker.Broker, namespace string) {
	t.Helper()
	relay := NewModelChangePublisher(svc, NewModelConfigPublisher(brk, namespace, false, slog.Default()))
	composite := platpkg.NewCompositeOutboxPublisher(buildOutboxRoutes(nil, nil, relay, nil, nil), slog.Default())
	poller := platpkg.NewOutboxPoller(outboxRepo, composite, platpkg.OutboxPollerConfig{
		PollInterval:    50 * time.Millisecond,
		CleanupInterval: time.Hour,
		CleanupAge:      time.Hour,
		BatchSize:       10,
		MaxRetries:      1000,
		BackoffBase:     20 * time.Millisecond,
		BackoffMax:      50 * time.Millisecond,
	}, slog.Default())
	svc.WithOutbox(outboxRepo)
	svc.SetOutboxNotifier(poller)
	if err := poller.Start(t.Context()); err != nil {
		t.Fatalf("start outbox poller: %v", err)
	}
	t.Cleanup(func() { _ = poller.Stop(context.Background()) }) //nolint:contextcheck
}

func createTestModel(t *testing.T, svc *ModelConfigService, alias string) *ModelConfigResponse {
	t.Helper()
	resp, err := svc.Create(t.Context(), &CreateModelConfigRequest{
		Alias:     alias,
		Provider:  "openai",
		ModelName: "gpt-4",
		APIBase:   "https://llm.example.com/v1",
	}, "user1")
	if err != nil {
		t.Fatalf("create model config: %v", err)
	}
	return resp
}

func TestModelConfigService_RecordsOutboxEventPerWrite(t *testing.T) {
	t.Parallel()
	svc, outboxRepo, _ := setupModelConfigSvcWithOutbox(t)
	notifier := &countingNotifier{}
	svc.SetOutboxNotifier(notifier)
	ctx := t.Context()

	created := createTestModel(t, svc, "outbox-model")
	newName := "gpt-4o"
	if _, err := svc.Update(ctx, created.ID, &UpdateModelConfigRequest{ModelName: &newName}, "user1"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if err := svc.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	events, err := outboxRepo.GetDispatchable(ctx, 10)
	if err != nil {
		t.Fatalf("GetDispatchable: %v", err)
	}
	wantTypes := []string{modelOutboxEventCreated, modelOutboxEventUpdated, modelOutboxEventDeleted}
	if len(events) != len(wantTypes) {
		t.Fatalf("got %d outbox events, want %d: %+v", len(events), len(wantTypes), events)
	}
	for i, e := range events {
		if e.EventType != wantTypes[i] {
			t.Errorf("event %d type = %q, want %q", i, e.EventType, wantTypes[i])
		}
		if e.AggregateID != created.ID {
			t.Errorf("event %d aggregate = %q, want %q", i, e.AggregateID, created.ID)
		}
		var payload modelOutboxPayload
		if err := json.Unmarshal([]byte(e.Payload), &payload); err != nil || payload.Alias != "outbox-model" {
			t.Errorf("event %d payload = %s, want alias outbox-model", i, e.Payload)
		}
	}
	if got := notifier.n.Load(); got != 3 {
		t.Errorf("Notify called %d times, want 3", got)
	}
}

func TestModelConfigService_OutboxFailureRollsBackWrite(t *testing.T) {
	t.Parallel()
	newName := "gpt-4o"
	tests := []struct {
		name  string
		write func(ctx context.Context, svc *ModelConfigService, existingID string) error
		check func(t *testing.T, repo *store.ModelConfigRepo, existingID string)
	}{
		{
			name: "create",
			write: func(ctx context.Context, svc *ModelConfigService, _ string) error {
				_, err := svc.Create(ctx, &CreateModelConfigRequest{
					Alias: "never-saved", Provider: "openai", ModelName: "gpt-4", APIBase: "https://llm.example.com/v1",
				}, "user1")
				return err
			},
			check: func(t *testing.T, repo *store.ModelConfigRepo, _ string) {
				if mc, _ := repo.GetByAlias(t.Context(), "never-saved"); mc != nil {
					t.Error("model row committed although its outbox event failed")
				}
			},
		},
		{
			name: "update",
			write: func(ctx context.Context, svc *ModelConfigService, id string) error {
				_, err := svc.Update(ctx, id, &UpdateModelConfigRequest{ModelName: &newName}, "user1")
				return err
			},
			check: func(t *testing.T, repo *store.ModelConfigRepo, id string) {
				if mc, _ := repo.GetByID(t.Context(), id); mc == nil || mc.ModelName != "gpt-4" {
					t.Errorf("model row changed although its outbox event failed: %+v", mc)
				}
			},
		},
		{
			name: "delete",
			write: func(ctx context.Context, svc *ModelConfigService, id string) error {
				return svc.Delete(ctx, id)
			},
			check: func(t *testing.T, repo *store.ModelConfigRepo, id string) {
				if mc, _ := repo.GetByID(t.Context(), id); mc == nil {
					t.Error("model row deleted although its outbox event failed")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc, _, db := setupModelConfigSvcWithOutbox(t)
			existing := createTestModel(t, svc, "existing")
			if _, err := db.ExecContext(t.Context(), `DROP TABLE outbox_events`); err != nil {
				t.Fatalf("drop outbox table: %v", err)
			}

			if err := tt.write(t.Context(), svc, existing.ID); err == nil {
				t.Fatal("write succeeded without its outbox event")
			}
			tt.check(t, store.NewModelConfigRepo(db), existing.ID)
		})
	}
}

func modelTopicCollector(t *testing.T, brk broker.Broker, topic string) broker.Queue {
	t.Helper()
	q, err := brk.CreateDirectSubscription()
	if err != nil {
		t.Fatalf("CreateDirectSubscription: %v", err)
	}
	if err := brk.Subscribe(q, topic); err != nil {
		t.Fatalf("Subscribe %s: %v", topic, err)
	}
	return q
}

func receiveModelConfig(t *testing.T, brk broker.Broker, q broker.Queue, timeout time.Duration) (map[string]any, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	msg, err := brk.Receive(ctx, q)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("decode payload %s: %v", msg.Payload, err)
	}
	return payload, true
}

func TestModelChangePublisher_PublishesCurrentRowAndDeletes(t *testing.T) {
	t.Parallel()
	for _, servesPythonAgents := range []bool{false, true} {
		t.Run(fmt.Sprintf("servesPythonAgents=%v", servesPythonAgents), func(t *testing.T) {
			t.Parallel()
			testModelChangePublisherPublishesCurrentRowAndDeletes(t, servesPythonAgents)
		})
	}
}

func testModelChangePublisherPublishesCurrentRowAndDeletes(t *testing.T, servesPythonAgents bool) {
	const apiKey = "sk-relay-secret"
	brk := connectedDevBroker(t)
	svc, _ := setupModelConfigSvc(t)
	relay := NewModelChangePublisher(svc, NewModelConfigPublisher(brk, "default", servesPythonAgents, slog.Default()))
	created, err := svc.Create(t.Context(), &CreateModelConfigRequest{
		Alias:      "relay-model",
		Provider:   "openai",
		ModelName:  "gpt-4",
		APIBase:    "https://llm.example.com/v1",
		AuthConfig: map[string]any{"type": "apikey", "api_key": apiKey},
	}, "user1")
	if err != nil {
		t.Fatalf("create model config: %v", err)
	}
	aliasQ := modelTopicCollector(t, brk, a2a.ModelConfigUpdateTopic("default", "relay-model"))
	idQ := modelTopicCollector(t, brk, a2a.ModelConfigUpdateTopic("default", created.ID))
	event := func(eventType string) store.OutboxEvent {
		return store.OutboxEvent{EventType: eventType, AggregateID: created.ID, Payload: `{"alias":"relay-model"}`}
	}

	if err := relay.PublishOutboxEvent(t.Context(), event(modelOutboxEventUpdated)); err != nil {
		t.Fatalf("relay update: %v", err)
	}
	for name, q := range map[string]broker.Queue{"alias": aliasQ, "id": idQ} {
		payload, ok := receiveModelConfig(t, brk, q, 2*time.Second)
		if !ok {
			t.Fatalf("no update on the %s topic", name)
		}
		if payload["model_id"] != created.ID || payload["alias"] != "relay-model" {
			t.Errorf("update on the %s topic does not name the model: %v", name, payload)
		}
		encoded, _ := json.Marshal(payload)
		carriesKey := strings.Contains(string(encoded), apiKey)
		_, carriesConfig := payload["model_config"]
		if carriesKey != servesPythonAgents || carriesConfig != servesPythonAgents {
			t.Errorf("update on the %s topic: model_config present = %v, API key present = %v, want both %v: %s",
				name, carriesConfig, carriesKey, servesPythonAgents, encoded)
		}
		if strings.Contains(string(encoded), samapp.ModelConfigIDKey) {
			t.Errorf("update on the %s topic carries %s, which Python agents forward to the provider: %s", name, samapp.ModelConfigIDKey, encoded)
		}
	}

	if err := svc.Delete(t.Context(), created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := relay.PublishOutboxEvent(t.Context(), event(modelOutboxEventUpdated)); err != nil {
		t.Fatalf("relay update of a deleted row: %v", err)
	}
	if payload, ok := receiveModelConfig(t, brk, aliasQ, 200*time.Millisecond); ok {
		t.Fatalf("update of a deleted row was published: %v", payload)
	}

	if err := relay.PublishOutboxEvent(t.Context(), event(modelOutboxEventDeleted)); err != nil {
		t.Fatalf("relay delete: %v", err)
	}
	payload, ok := receiveModelConfig(t, brk, aliasQ, 2*time.Second)
	if !ok {
		t.Fatal("no delete on the alias topic")
	}
	if cfg, present := payload["model_config"]; !present || cfg != nil {
		t.Errorf("delete payload = %v, want model_config null", payload)
	}
}

func TestModelChangePublisher_ReturnsErrorsForRetry(t *testing.T) {
	t.Parallel()
	brk := connectedDevBroker(t)
	db := testOpenDB(t)
	svc := NewModelConfigService(store.NewModelConfigRepo(db), slog.Default())
	svc.SetFeatureRegistry(decisionNodesOn())
	relay := NewModelChangePublisher(svc, NewModelConfigPublisher(brk, "default", false, slog.Default()))
	created := createTestModel(t, svc, "failing-model")

	if err := relay.PublishOutboxEvent(t.Context(), store.OutboxEvent{
		EventType: modelOutboxEventUpdated, AggregateID: created.ID, Payload: "not json",
	}); err == nil {
		t.Error("undecodable payload returned nil")
	}

	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	if err := relay.PublishOutboxEvent(t.Context(), store.OutboxEvent{
		EventType: modelOutboxEventUpdated, AggregateID: created.ID, Payload: `{"alias":"failing-model"}`,
	}); err == nil {
		t.Error("failed row read returned nil")
	}
}

type guaranteedOnlyBroker struct {
	broker.Broker
	down atomic.Bool
}

var errDirectPublish = errors.New("direct publish used")

func (b *guaranteedOnlyBroker) Publish(context.Context, string, *broker.Message) error {
	return errDirectPublish
}

func (b *guaranteedOnlyBroker) PublishGuaranteed(ctx context.Context, topic string, msg *broker.Message) error {
	if b.down.Load() {
		return errors.New("broker unreachable")
	}
	return b.Broker.PublishGuaranteed(ctx, topic, msg)
}

func TestModelChangePublisher_PublishesGuaranteed(t *testing.T) {
	t.Parallel()
	brk := &guaranteedOnlyBroker{Broker: connectedDevBroker(t)}
	svc, outboxRepo, db := setupModelConfigSvcWithOutbox(t)
	aliasQ := modelTopicCollector(t, brk, a2a.ModelConfigUpdateTopic("default", "general"))
	brk.down.Store(true)
	startModelOutboxRelay(t, svc, outboxRepo, brk, "default")

	createTestModel(t, svc, "general")
	var published, retries int
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := db.QueryRowContext(t.Context(),
			`SELECT published, retry_count FROM outbox_events WHERE event_type = ?`, modelOutboxEventCreated,
		).Scan(&published, &retries); err != nil {
			t.Fatalf("read outbox row: %v", err)
		}
		if published != 0 || retries >= 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if published != 0 || retries < 1 {
		t.Fatalf("while the broker is down: published=%d retry_count=%d, want the event held back for retry", published, retries)
	}
	if payload, ok := receiveModelConfig(t, brk, aliasQ, 200*time.Millisecond); ok {
		t.Fatalf("published while the broker was down: %v", payload)
	}

	brk.down.Store(false)
	if _, ok := receiveModelConfig(t, brk, aliasQ, 15*time.Second); !ok {
		t.Fatal("the retried event never reached the alias topic")
	}
}

func TestModelConfigService_RenameRecordsThePreviousAlias(t *testing.T) {
	t.Parallel()
	svc, outboxRepo, _ := setupModelConfigSvcWithOutbox(t)
	ctx := t.Context()
	created := createTestModel(t, svc, "research")
	newName := "gpt-4o"
	if _, err := svc.Update(ctx, created.ID, &UpdateModelConfigRequest{ModelName: &newName}, "user1"); err != nil {
		t.Fatalf("update: %v", err)
	}
	newAlias := "analysis"
	if _, err := svc.Update(ctx, created.ID, &UpdateModelConfigRequest{Alias: &newAlias}, "user1"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	events, err := outboxRepo.GetDispatchable(ctx, 10)
	if err != nil {
		t.Fatalf("GetDispatchable: %v", err)
	}
	want := []modelOutboxPayload{
		{Alias: "research"},
		{Alias: "research"},
		{Alias: "analysis", PreviousAlias: "research"},
	}
	if len(events) != len(want) {
		t.Fatalf("got %d outbox events, want %d", len(events), len(want))
	}
	for i, e := range events {
		var got modelOutboxPayload
		if err := json.Unmarshal([]byte(e.Payload), &got); err != nil {
			t.Fatalf("event %d payload %s: %v", i, e.Payload, err)
		}
		if got != want[i] {
			t.Errorf("event %d payload = %+v, want %+v", i, got, want[i])
		}
	}
}

func TestModelChangePublisher_MessagesNameTheChangedKeys(t *testing.T) {
	t.Parallel()
	brk := connectedDevBroker(t)
	svc, _ := setupModelConfigSvc(t)
	relay := NewModelChangePublisher(svc, NewModelConfigPublisher(brk, "default", false, slog.Default()))
	created := createTestModel(t, svc, "analysis")
	aliasQ := modelTopicCollector(t, brk, a2a.ModelConfigUpdateTopic("default", "analysis"))
	idQ := modelTopicCollector(t, brk, a2a.ModelConfigUpdateTopic("default", created.ID))
	oldQ := modelTopicCollector(t, brk, a2a.ModelConfigUpdateTopic("default", "research"))

	rename := store.OutboxEvent{EventType: modelOutboxEventUpdated, AggregateID: created.ID, Payload: `{"alias":"analysis","previousAlias":"research"}`}
	if err := relay.PublishOutboxEvent(t.Context(), rename); err != nil {
		t.Fatalf("relay rename: %v", err)
	}
	for name, q := range map[string]broker.Queue{"alias": aliasQ, "id": idQ} {
		payload, ok := receiveModelConfig(t, brk, q, 2*time.Second)
		if !ok {
			t.Fatalf("no update on the %s topic", name)
		}
		if payload["model_id"] != created.ID || payload["alias"] != "analysis" || payload["previous_alias"] != "research" {
			t.Errorf("update on the %s topic = %v, want model_id, alias analysis, previous_alias research", name, payload)
		}
	}
	if payload, ok := receiveModelConfig(t, brk, oldQ, 200*time.Millisecond); ok {
		t.Fatalf("published on the old alias topic: %v", payload)
	}

	update := store.OutboxEvent{EventType: modelOutboxEventUpdated, AggregateID: created.ID, Payload: `{"alias":"analysis"}`}
	if err := relay.PublishOutboxEvent(t.Context(), update); err != nil {
		t.Fatalf("relay update: %v", err)
	}
	payload, _ := receiveModelConfig(t, brk, idQ, 2*time.Second)
	if _, present := payload["previous_alias"]; present {
		t.Errorf("update without a rename names a previous alias: %v", payload)
	}

	del := store.OutboxEvent{EventType: modelOutboxEventDeleted, AggregateID: created.ID, Payload: `{"alias":"analysis"}`}
	if err := relay.PublishOutboxEvent(t.Context(), del); err != nil {
		t.Fatalf("relay delete: %v", err)
	}
	payload, _ = receiveModelConfig(t, brk, idQ, 2*time.Second)
	if cfg, present := payload["model_config"]; !present || cfg != nil || payload["model_id"] != created.ID || payload["alias"] != "analysis" {
		t.Errorf("delete payload = %v, want model_config null with model_id and alias", payload)
	}
}

func TestModelChangePublisher_RenameThenDeleteNamesThePreviousAlias(t *testing.T) {
	t.Parallel()
	brk := connectedDevBroker(t)
	svc, _ := setupModelConfigSvc(t)
	relay := NewModelChangePublisher(svc, NewModelConfigPublisher(brk, "default", false, slog.Default()))
	created := createTestModel(t, svc, "analysis")
	idQ := modelTopicCollector(t, brk, a2a.ModelConfigUpdateTopic("default", created.ID))
	aliasQ := modelTopicCollector(t, brk, a2a.ModelConfigUpdateTopic("default", "analysis"))
	oldQ := modelTopicCollector(t, brk, a2a.ModelConfigUpdateTopic("default", "research"))
	if err := svc.Delete(t.Context(), created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	rename := store.OutboxEvent{EventType: modelOutboxEventUpdated, AggregateID: created.ID, Payload: `{"alias":"analysis","previousAlias":"research"}`}
	if err := relay.PublishOutboxEvent(t.Context(), rename); err != nil {
		t.Fatalf("relay rename of a deleted row: %v", err)
	}

	payload, ok := receiveModelConfig(t, brk, idQ, 2*time.Second)
	if !ok {
		t.Fatal("nothing published on the model id topic")
	}
	if cfg, present := payload["model_config"]; !present || cfg != nil || payload["model_id"] != created.ID || payload["alias"] != "analysis" || payload["previous_alias"] != "research" {
		t.Fatalf("payload = %v, want a null config naming the model id, analysis and research", payload)
	}
	for name, q := range map[string]broker.Queue{"alias": aliasQ, "previous alias": oldQ} {
		if p, ok := receiveModelConfig(t, brk, q, 200*time.Millisecond); ok {
			t.Fatalf("published on the %s topic: %v", name, p)
		}
	}

	plain := store.OutboxEvent{EventType: modelOutboxEventUpdated, AggregateID: created.ID, Payload: `{"alias":"analysis"}`}
	if err := relay.PublishOutboxEvent(t.Context(), plain); err != nil {
		t.Fatalf("relay update of a deleted row: %v", err)
	}
	if p, ok := receiveModelConfig(t, brk, idQ, 200*time.Millisecond); ok {
		t.Fatalf("an update of a deleted row without a rename published %v", p)
	}
}
