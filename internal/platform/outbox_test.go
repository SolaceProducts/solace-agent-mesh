// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type mockPublisher struct {
	mu        sync.Mutex
	published []store.OutboxEvent
	failIDs   map[string]bool
}

func (m *mockPublisher) PublishOutboxEvent(_ context.Context, event store.OutboxEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failIDs[event.ID] {
		return fmt.Errorf("simulated publish failure for %s", event.ID)
	}
	m.published = append(m.published, event)
	return nil
}

func (m *mockPublisher) getPublished() []store.OutboxEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]store.OutboxEvent, len(m.published))
	copy(cp, m.published)
	return cp
}

func setupOutboxPollerTestDB(t *testing.T) *store.OutboxRepo {
	t.Helper()
	return store.NewOutboxRepo(store.TestDB(t))
}

func TestOutboxPoller_PollOnce(t *testing.T) {
	repo := setupOutboxPollerTestDB(t)
	ctx := context.Background()

	e1 := &store.OutboxEvent{EventType: "type1", AggregateID: idgen.New(), Payload: `{"k":"v1"}`}
	e2 := &store.OutboxEvent{EventType: "type2", AggregateID: idgen.New(), Payload: `{"k":"v2"}`}
	if err := repo.Create(ctx, e1); err != nil {
		t.Fatalf("Create e1: %v", err)
	}
	if err := repo.Create(ctx, e2); err != nil {
		t.Fatalf("Create e2: %v", err)
	}

	pub := &mockPublisher{}
	cfg := OutboxPollerConfig{
		PollInterval:    time.Hour,
		CleanupInterval: time.Hour,
		CleanupAge:      time.Hour,
		BatchSize:       100,
	}
	poller := NewOutboxPoller(repo, pub, cfg, slog.Default())

	poller.pollOnce(ctx)

	published := pub.getPublished()
	if len(published) != 2 {
		t.Fatalf("expected 2 published events, got %d", len(published))
	}

	unpublished, err := repo.GetDispatchable(ctx, 100)
	if err != nil {
		t.Fatalf("GetDispatchable: %v", err)
	}
	if len(unpublished) != 0 {
		t.Errorf("expected 0 unpublished after poll, got %d", len(unpublished))
	}
}

func TestOutboxPoller_PollOnce_PartialFailure(t *testing.T) {
	repo := setupOutboxPollerTestDB(t)
	ctx := context.Background()

	e1 := &store.OutboxEvent{EventType: "type1", AggregateID: idgen.New(), Payload: "{}"}
	e2 := &store.OutboxEvent{EventType: "type2", AggregateID: idgen.New(), Payload: "{}"}
	_ = repo.Create(ctx, e1)
	_ = repo.Create(ctx, e2)

	pub := &mockPublisher{failIDs: map[string]bool{e1.ID: true}}
	cfg := OutboxPollerConfig{
		PollInterval:    time.Hour,
		CleanupInterval: time.Hour,
		CleanupAge:      time.Hour,
		BatchSize:       100,
		MaxRetries:      10,
		BackoffBase:     time.Millisecond,
		BackoffMax:      time.Millisecond,
	}
	poller := NewOutboxPoller(repo, pub, cfg, slog.Default())
	poller.pollOnce(ctx)

	published := pub.getPublished()
	if len(published) != 1 {
		t.Fatalf("expected 1 published event, got %d", len(published))
	}
	if published[0].ID != e2.ID {
		t.Errorf("published event ID = %q, want %q", published[0].ID, e2.ID)
	}

	time.Sleep(5 * time.Millisecond)
	unpublished, _ := repo.GetDispatchable(ctx, 100)
	if len(unpublished) != 1 {
		t.Fatalf("expected 1 unpublished, got %d", len(unpublished))
	}
	if unpublished[0].ID != e1.ID {
		t.Errorf("unpublished event ID = %q, want %q", unpublished[0].ID, e1.ID)
	}
	if unpublished[0].RetryCount != 1 {
		t.Errorf("retry_count = %d, want 1", unpublished[0].RetryCount)
	}
	if unpublished[0].LastError == nil || *unpublished[0].LastError == "" {
		t.Errorf("last_error empty, want non-empty after publish failure")
	}
}

func TestOutboxPoller_DeadLetterAfterMaxRetries(t *testing.T) {
	repo := setupOutboxPollerTestDB(t)
	ctx := context.Background()

	e := &store.OutboxEvent{EventType: "tool.uploaded", AggregateID: idgen.New(), Payload: "{}"}
	if err := repo.Create(ctx, e); err != nil {
		t.Fatalf("Create: %v", err)
	}

	pub := &mockPublisher{failIDs: map[string]bool{e.ID: true}}
	cfg := OutboxPollerConfig{
		PollInterval:    time.Hour,
		CleanupInterval: time.Hour,
		CleanupAge:      time.Hour,
		BatchSize:       100,
		MaxRetries:      2,
		BackoffBase:     time.Microsecond,
		BackoffMax:      time.Microsecond,
	}
	poller := NewOutboxPoller(repo, pub, cfg, slog.Default())

	for range 3 {
		poller.pollOnce(ctx)
		time.Sleep(time.Millisecond)
	}

	dispatchable, _ := repo.GetDispatchable(ctx, 100)
	if len(dispatchable) != 0 {
		t.Fatalf("expected dead row to disappear from dispatch queue, got %d", len(dispatchable))
	}
}

func TestOutboxPoller_PollOnce_Empty(t *testing.T) {
	repo := setupOutboxPollerTestDB(t)
	pub := &mockPublisher{}
	cfg := DefaultOutboxPollerConfig()
	poller := NewOutboxPoller(repo, pub, cfg, slog.Default())

	poller.pollOnce(context.Background())

	if len(pub.getPublished()) != 0 {
		t.Error("expected no published events")
	}
}

func TestOutboxPoller_CleanupOnce(t *testing.T) {
	repo := setupOutboxPollerTestDB(t)
	ctx := context.Background()

	e := &store.OutboxEvent{EventType: "old", AggregateID: idgen.New(), Payload: "{}"}
	_ = repo.Create(ctx, e)
	_ = repo.MarkPublished(ctx, []string{e.ID})

	cfg := OutboxPollerConfig{
		PollInterval:    time.Hour,
		CleanupInterval: time.Hour,
		CleanupAge:      0,
		BatchSize:       100,
	}
	pub := &mockPublisher{}
	poller := NewOutboxPoller(repo, pub, cfg, slog.Default())

	time.Sleep(10 * time.Millisecond)
	job := poller.CleanupJob()
	if job.Name != leader.JobPlatformOutboxCleanup || job.Every != cfg.CleanupInterval {
		t.Fatalf("cleanup job = %s every %s, want %s every %s", job.Name, job.Every, leader.JobPlatformOutboxCleanup, cfg.CleanupInterval)
	}
	if err := job.Run(ctx, leader.Term{Number: 1}); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	unpublished, _ := repo.GetDispatchable(ctx, 100)
	if len(unpublished) != 0 {
		t.Errorf("expected 0 events after cleanup, got %d", len(unpublished))
	}
}

func TestOutboxPoller_StartStop(t *testing.T) {
	repo := setupOutboxPollerTestDB(t)
	ctx := context.Background()

	e := &store.OutboxEvent{EventType: "test", AggregateID: idgen.New(), Payload: "{}"}
	_ = repo.Create(ctx, e)

	pub := &mockPublisher{}
	cfg := OutboxPollerConfig{
		PollInterval:    50 * time.Millisecond,
		CleanupInterval: time.Hour,
		CleanupAge:      time.Hour,
		BatchSize:       100,
	}
	poller := NewOutboxPoller(repo, pub, cfg, slog.Default())

	if err := poller.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for len(pub.getPublished()) == 0 {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for event to be published")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if err := poller.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	published := pub.getPublished()
	if len(published) != 1 {
		t.Errorf("expected 1 published event, got %d", len(published))
	}
	if published[0].ID != e.ID {
		t.Errorf("published ID = %q, want %q", published[0].ID, e.ID)
	}
}

func TestCompositeOutboxPublisher_RoutesMatchingPrefix(t *testing.T) {
	t.Parallel()

	connectorPub := &mockPublisher{}
	toolPub := &mockPublisher{}
	routes := []OutboxRoute{
		{Prefix: "connector.", Publisher: connectorPub},
		{Prefix: "tool.", Publisher: toolPub},
	}
	composite := NewCompositeOutboxPublisher(routes, slog.Default())

	event := store.OutboxEvent{ID: "e1", EventType: "connector.updated", AggregateID: idgen.New(), Payload: "{}"}
	if err := composite.PublishOutboxEvent(context.Background(), event); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := len(connectorPub.getPublished()); got != 1 {
		t.Errorf("connector publisher received %d events, want 1", got)
	}
	if got := len(toolPub.getPublished()); got != 0 {
		t.Errorf("tool publisher received %d events, want 0", got)
	}
}

func TestCompositeOutboxPublisher_MultipleMatchesBothFire(t *testing.T) {
	t.Parallel()

	redeployPub := &mockPublisher{}
	syncPub := &mockPublisher{}
	routes := []OutboxRoute{
		{Prefix: "tool.", Publisher: redeployPub},
		{Prefix: "tool.", Publisher: syncPub},
	}
	composite := NewCompositeOutboxPublisher(routes, slog.Default())

	event := store.OutboxEvent{ID: "e2", EventType: "tool.uploaded", AggregateID: idgen.New(), Payload: "{}"}
	if err := composite.PublishOutboxEvent(context.Background(), event); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if got := len(redeployPub.getPublished()); got != 1 {
		t.Errorf("redeploy publisher received %d events, want 1", got)
	}
	if got := len(syncPub.getPublished()); got != 1 {
		t.Errorf("sync publisher received %d events, want 1", got)
	}
}

func TestCompositeOutboxPublisher_FailurePropagates(t *testing.T) {
	t.Parallel()

	failingPub := &mockPublisher{failIDs: map[string]bool{"e3": true}}
	successPub := &mockPublisher{}
	routes := []OutboxRoute{
		{Prefix: "tool.", Publisher: failingPub},
		{Prefix: "tool.", Publisher: successPub},
	}
	composite := NewCompositeOutboxPublisher(routes, slog.Default())

	event := store.OutboxEvent{ID: "e3", EventType: "tool.uploaded", AggregateID: idgen.New(), Payload: "{}"}
	err := composite.PublishOutboxEvent(context.Background(), event)
	if err == nil {
		t.Fatal("expected error when a matching publisher fails")
	}
	if got := len(successPub.getPublished()); got != 0 {
		t.Errorf("successPub received %d events; expected short-circuit on first error", got)
	}
}

func TestCompositeOutboxPublisher_NoMatchReturnsNil(t *testing.T) {
	t.Parallel()

	pub := &mockPublisher{}
	routes := []OutboxRoute{
		{Prefix: "connector.", Publisher: pub},
	}
	composite := NewCompositeOutboxPublisher(routes, slog.Default())

	event := store.OutboxEvent{ID: "e4", EventType: "random.event", AggregateID: idgen.New(), Payload: "{}"}
	if err := composite.PublishOutboxEvent(context.Background(), event); err != nil {
		t.Fatalf("unmatched event should not error, got %v", err)
	}
	if got := len(pub.getPublished()); got != 0 {
		t.Errorf("no publisher should be invoked, got %d events", got)
	}
}

func TestCompositeOutboxPublisher_IntegratesWithPoller(t *testing.T) {
	repo := setupOutboxPollerTestDB(t)
	ctx := context.Background()

	eConnector := &store.OutboxEvent{EventType: "connector.updated", AggregateID: idgen.New(), Payload: "{}"}
	eTool := &store.OutboxEvent{EventType: "tool.uploaded", AggregateID: idgen.New(), Payload: "{}"}
	eUnknown := &store.OutboxEvent{EventType: "mystery.event", AggregateID: idgen.New(), Payload: "{}"}
	if err := repo.Create(ctx, eConnector); err != nil {
		t.Fatalf("Create connector event: %v", err)
	}
	if err := repo.Create(ctx, eTool); err != nil {
		t.Fatalf("Create tool event: %v", err)
	}
	if err := repo.Create(ctx, eUnknown); err != nil {
		t.Fatalf("Create unknown event: %v", err)
	}

	connPub := &mockPublisher{}
	toolPub := &mockPublisher{}
	composite := NewCompositeOutboxPublisher([]OutboxRoute{
		{Prefix: "connector.", Publisher: connPub},
		{Prefix: "tool.", Publisher: toolPub},
	}, slog.Default())

	poller := NewOutboxPoller(repo, composite, OutboxPollerConfig{
		PollInterval:    time.Hour,
		CleanupInterval: time.Hour,
		CleanupAge:      time.Hour,
		BatchSize:       100,
	}, slog.Default())
	poller.pollOnce(ctx)

	if got := len(connPub.getPublished()); got != 1 {
		t.Errorf("connPub got %d events, want 1", got)
	}
	if got := len(toolPub.getPublished()); got != 1 {
		t.Errorf("toolPub got %d events, want 1", got)
	}

	unpublished, err := repo.GetDispatchable(ctx, 100)
	if err != nil {
		t.Fatalf("GetDispatchable: %v", err)
	}
	if len(unpublished) != 0 {
		t.Errorf("expected 0 unpublished events, got %d", len(unpublished))
	}
}

func TestOutboxPoller_BatchSize(t *testing.T) {
	repo := setupOutboxPollerTestDB(t)
	ctx := context.Background()

	for i := range 5 {
		_ = repo.Create(ctx, &store.OutboxEvent{
			EventType:   fmt.Sprintf("type%d", i),
			AggregateID: idgen.New(),
			Payload:     "{}",
		})
	}

	pub := &mockPublisher{}
	cfg := OutboxPollerConfig{
		PollInterval:    time.Hour,
		CleanupInterval: time.Hour,
		CleanupAge:      time.Hour,
		BatchSize:       3,
	}
	poller := NewOutboxPoller(repo, pub, cfg, slog.Default())

	poller.pollOnce(ctx)
	if got := len(pub.getPublished()); got != 3 {
		t.Fatalf("first poll: expected 3 published, got %d", got)
	}

	poller.pollOnce(ctx)
	if got := len(pub.getPublished()); got != 5 {
		t.Fatalf("second poll: expected 5 total published, got %d", got)
	}
}
