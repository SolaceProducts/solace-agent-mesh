// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker/migration"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/testing/mockllm"
)

type fixedLLMClient struct {
	content string
}

func (c *fixedLLMClient) ChatCompletion(_ context.Context, _ *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: c.content}, nil
}

func (c *fixedLLMClient) ChatCompletionStream(_ context.Context, _ *llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch, nil
}

func TestDynamicLLMProvider_HotSwapsOnBrokerEvent(t *testing.T) {
	srv := startSwapMockLLM(t)
	brk := connectedDevBroker(t)
	svc := setupRelayedModelConfigSvc(t, brk)

	rc := llm.NewReconfigurableClient(&fixedLLMClient{content: "INITIAL"})
	startProvider(t, svc, rc, brk)

	if got := chat(t, rc); got != "INITIAL" {
		t.Fatalf("baseline content = %q, want INITIAL (seed must not swap)", got)
	}

	generalID := saveGeneralModel(t, svc, srv)
	waitForReply(t, rc, swapReply)
	if got := rc.ModelConfigID(); got != generalID {
		t.Errorf("live client configured from %q, want the general row %s", got, generalID)
	}
	if got := rc.Model(); got != swapModel {
		t.Errorf("live client's model = %q, want the swapped row's %q: a gateway reads the model's name from it", got, swapModel)
	}

	captured := srv.CapturedRequests()
	if len(captured) == 0 {
		t.Fatal("mock received no request; swapped client did not reach the server")
	}
	if last := string(captured[len(captured)-1]); !strings.Contains(last, swapModel) {
		t.Errorf("captured request model mismatch; want it to contain %q, got %s", swapModel, last)
	}
}

func TestDynamicLLMProvider_EveryGWERebuilds(t *testing.T) {
	srv := startSwapMockLLM(t)
	brk := connectedDevBroker(t)
	svc := setupRelayedModelConfigSvc(t, brk)

	gweA := llm.NewReconfigurableClient(&fixedLLMClient{content: "INITIAL"})
	gweB := llm.NewReconfigurableClient(&fixedLLMClient{content: "INITIAL"})
	startProvider(t, svc, gweA, brk)
	startProvider(t, svc, gweB, brk)

	saveGeneralModel(t, svc, srv)
	waitForReply(t, gweA, swapReply)
	waitForReply(t, gweB, swapReply)
}

func TestDynamicLLMProvider_RetiresOldQueue(t *testing.T) {
	brk := connectedDevBroker(t)
	svc, _ := setupModelConfigSvc(t)
	old := migration.ModelProviderQueue("default", "general")
	q, err := brk.CreateQueue(old.Queue, broker.QueueOptions{Named: true})
	if err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	if err := brk.Subscribe(q, old.Topics[0]); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	startProvider(t, svc, llm.NewReconfigurableClient(&fixedLLMClient{content: "INITIAL"}), brk)

	if brk.HasQueue(old.Queue) {
		t.Fatalf("queue %q still exists after start", old.Queue)
	}
}

func TestDynamicLLMProvider_StopReleasesSubscription(t *testing.T) {
	brk := connectedDevBroker(t)
	svc, _ := setupModelConfigSvc(t)
	provider := NewDynamicLLMProvider(svc, llm.NewReconfigurableClient(&fixedLLMClient{content: "INITIAL"}), "general", brk, "default", slog.Default())
	if err := provider.SeedAndStart(t.Context()); err != nil {
		t.Fatalf("SeedAndStart: %v", err)
	}
	topic := a2a.ModelConfigUpdateTopic("default", "general")
	if got := brk.MatchingQueues(topic); len(got) == 0 {
		t.Fatal("nothing subscribed to the model topic after start")
	}

	provider.Stop(t.Context())

	if got := brk.MatchingQueues(topic); len(got) != 0 {
		t.Errorf("model topic still reaches %v after Stop", got)
	}
}

const (
	swapModel = "swapped-e2e-marker"
	swapReply = "SWAPPED_OK"
)

func startSwapMockLLM(t *testing.T) *mockllm.MockLLMServer {
	t.Helper()
	scenario, err := mockllm.ParseScenario([]byte(`{
		"id": "hot-swap",
		"responses": [
			{"choices": [{"index": 0, "message": {"role": "assistant", "content": "` + swapReply + `"}, "finish_reason": "stop"}]}
		]
	}`))
	if err != nil {
		t.Fatalf("parse scenario: %v", err)
	}
	srv := mockllm.NewMockLLMServer("127.0.0.1:0", 0, 0, slog.Default())
	srv.LoadScenarios(scenario)
	if startErr := srv.Start(); startErr != nil {
		t.Fatalf("start mock LLM: %v", startErr)
	}
	t.Cleanup(srv.Stop)
	return srv
}

func connectedDevBroker(t *testing.T) *broker.DevBroker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) }) //nolint:contextcheck
	return brk
}

func startProvider(t *testing.T, svc *ModelConfigService, rc *llm.ReconfigurableClient, brk broker.Broker) {
	t.Helper()
	provider := NewDynamicLLMProvider(svc, rc, "general", brk, "default", slog.Default())
	if err := provider.SeedAndStart(t.Context()); err != nil {
		t.Fatalf("SeedAndStart: %v", err)
	}
	t.Cleanup(func() { provider.Stop(context.Background()) }) //nolint:contextcheck
}

func setupRelayedModelConfigSvc(t *testing.T, brk broker.Broker) *ModelConfigService {
	t.Helper()
	svc, outboxRepo, _ := setupModelConfigSvcWithOutbox(t)
	startModelOutboxRelay(t, svc, outboxRepo, brk, "default")
	return svc
}

func saveGeneralModel(t *testing.T, svc *ModelConfigService, srv *mockllm.MockLLMServer) string {
	t.Helper()
	created, err := svc.Create(t.Context(), &CreateModelConfigRequest{
		Alias:      "general",
		Provider:   "openai",
		ModelName:  swapModel,
		APIBase:    "http://" + srv.Addr() + "/v1",
		AuthConfig: map[string]any{"type": "apikey", "api_key": "sk-test"},
	}, "user1")
	if err != nil {
		t.Fatalf("create model config: %v", err)
	}
	return created.ID
}

func waitForReply(t *testing.T, rc llm.Client, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for chat(t, rc) != want {
		if time.Now().After(deadline) {
			t.Fatalf("live client never answered %q after the model-config change", want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func chat(t *testing.T, c llm.Client) string {
	t.Helper()
	resp, err := c.ChatCompletion(context.Background(), &llm.ChatRequest{
		Messages: []llm.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		return ""
	}
	if resp == nil {
		return ""
	}
	return resp.Content
}

func TestDynamicLLMProvider_RetriesAFailedStoreRead(t *testing.T) {
	srv := startSwapMockLLM(t)
	brk := connectedDevBroker(t)
	db := testOpenDB(t)
	svc := NewModelConfigService(store.NewModelConfigRepo(db), slog.Default())
	svc.SetFeatureRegistry(decisionNodesOn())

	rc := llm.NewReconfigurableClient(&fixedLLMClient{content: "INITIAL"})
	provider := NewDynamicLLMProvider(svc, rc, "general", brk, "default", slog.Default())
	provider.retryInitial, provider.retryMax = 20*time.Millisecond, 50*time.Millisecond
	if err := provider.SeedAndStart(t.Context()); err != nil {
		t.Fatalf("SeedAndStart: %v", err)
	}
	t.Cleanup(func() { provider.Stop(context.Background()) }) //nolint:contextcheck

	saveGeneralModel(t, svc, srv)
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE model_configurations RENAME TO model_configurations_hidden`); err != nil {
		t.Fatalf("hide table: %v", err)
	}
	topic := a2a.ModelConfigUpdateTopic("default", "general")
	if err := brk.Publish(t.Context(), topic, &broker.Message{Topic: topic, Payload: []byte(`{}`)}); err != nil {
		t.Fatalf("publish change: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := chat(t, rc); got != "INITIAL" {
		t.Fatalf("content while the store is unreadable = %q, want INITIAL", got)
	}

	if _, err := db.ExecContext(t.Context(), `ALTER TABLE model_configurations_hidden RENAME TO model_configurations`); err != nil {
		t.Fatalf("restore table: %v", err)
	}
	waitForReply(t, rc, swapReply)
}

type countingHandler struct {
	slog.Handler
	message string
	count   *atomic.Int32
}

func (h countingHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == h.message {
		h.count.Add(1)
	}
	return h.Handler.Handle(ctx, r)
}

func (h countingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return countingHandler{Handler: h.Handler.WithAttrs(attrs), message: h.message, count: h.count}
}

func (h countingHandler) WithGroup(name string) slog.Handler {
	return countingHandler{Handler: h.Handler.WithGroup(name), message: h.message, count: h.count}
}

func TestDynamicLLMProvider_ConfigThatDoesNotBuildIsNotRetried(t *testing.T) {
	srv := startSwapMockLLM(t)
	brk := connectedDevBroker(t)
	db := testOpenDB(t)
	svc := NewModelConfigService(store.NewModelConfigRepo(db), slog.Default())
	svc.SetFeatureRegistry(decisionNodesOn())
	var failures atomic.Int32
	logger := slog.New(countingHandler{Handler: slog.Default().Handler(), message: "dynamic-llm: failed to apply model config from store", count: &failures})

	rc := llm.NewReconfigurableClient(&fixedLLMClient{content: "INITIAL"})
	provider := NewDynamicLLMProvider(svc, rc, "general", brk, "default", logger)
	provider.retryInitial, provider.retryMax = 10*time.Millisecond, 20*time.Millisecond
	if err := provider.SeedAndStart(t.Context()); err != nil {
		t.Fatalf("SeedAndStart: %v", err)
	}
	t.Cleanup(func() { provider.Stop(context.Background()) }) //nolint:contextcheck
	topic := a2a.ModelConfigUpdateTopic("default", "general")
	announce := func() {
		if err := brk.Publish(t.Context(), topic, &broker.Message{Topic: topic, Payload: []byte(`{}`)}); err != nil {
			t.Fatalf("publish change: %v", err)
		}
	}

	saveGeneralModel(t, svc, srv)
	announce()
	waitForReply(t, rc, swapReply)

	general, err := svc.GetByAlias(t.Context(), "general")
	if err != nil {
		t.Fatalf("read general: %v", err)
	}
	bad := map[string]any{"api_key_file": filepath.Join(t.TempDir(), "missing")}
	if _, err := svc.Update(t.Context(), general.ID, &UpdateModelConfigRequest{ModelParams: bad}, "user1"); err != nil {
		t.Fatalf("update: %v", err)
	}
	announce()
	deadline := time.Now().Add(2 * time.Second)
	for failures.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)

	if got := failures.Load(); got != 1 {
		t.Fatalf("apply failures = %d, want 1 (a config that does not build is not retried)", got)
	}
	if got := chat(t, rc); got != swapReply {
		t.Fatalf("content after a bad config = %q, want the previous client kept", got)
	}
}
