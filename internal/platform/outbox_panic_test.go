// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type panickingPublisher struct {
	mu        sync.Mutex
	panicIDs  map[string]bool
	published []string
}

func (p *panickingPublisher) PublishOutboxEvent(_ context.Context, e store.OutboxEvent) error {
	if p.panicIDs[e.ID] {
		panic("simulated publisher bug")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.published = append(p.published, e.ID)
	return nil
}

func TestOutboxPoller_APublisherPanicIsThatEventsFailure(t *testing.T) {
	repo := setupOutboxPollerTestDB(t)
	ctx := context.Background()

	poison := &store.OutboxEvent{EventType: "connector.updated", AggregateID: idgen.New(), Payload: "{}"}
	healthy := &store.OutboxEvent{EventType: "connector.updated", AggregateID: idgen.New(), Payload: "{}"}
	if err := repo.Create(ctx, poison); err != nil {
		t.Fatalf("Create poison: %v", err)
	}
	if err := repo.Create(ctx, healthy); err != nil {
		t.Fatalf("Create healthy: %v", err)
	}

	var logs bytes.Buffer
	pub := &panickingPublisher{panicIDs: map[string]bool{poison.ID: true}}
	poller := NewOutboxPoller(repo, pub, OutboxPollerConfig{
		PollInterval:    time.Hour,
		CleanupInterval: time.Hour,
		CleanupAge:      time.Hour,
		BatchSize:       100,
		MaxRetries:      2,
		BackoffBase:     time.Millisecond,
		BackoffMax:      time.Millisecond,
	}, slog.New(slog.NewJSONHandler(&logs, nil)))

	poller.pollOnce(ctx)

	var panicLine map[string]any
	for _, raw := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line %q is not JSON: %v", raw, err)
		}
		if msg, _ := line["msg"].(string); strings.Contains(msg, "outbox publisher panicked") {
			panicLine = line
		}
	}
	if panicLine == nil {
		t.Fatalf("the panic was not logged:\n%s", logs.String())
	}
	for key, want := range map[string]any{"eventID": poison.ID, "eventType": "connector.updated", "aggregateID": poison.AggregateID, "panicType": "string"} {
		if panicLine[key] != want {
			t.Errorf("panic log field %q = %v, want %v", key, panicLine[key], want)
		}
	}
	if stack, _ := panicLine["stack"].(string); !strings.HasPrefix(stack, "goroutine ") {
		t.Errorf("the panic log carries no stack: %v", panicLine["stack"])
	}
	if strings.Contains(logs.String(), "simulated publisher bug") {
		t.Errorf("a log quotes the panic value, which can carry user content:\n%s", logs.String())
	}

	if len(pub.published) != 1 || pub.published[0] != healthy.ID {
		t.Fatalf("published = %v, want only the healthy event: the panic must not cost the rest of the batch", pub.published)
	}
	time.Sleep(5 * time.Millisecond)
	pending, err := repo.GetDispatchable(ctx, 100)
	if err != nil {
		t.Fatalf("GetDispatchable: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != poison.ID || pending[0].RetryCount != 1 || pending[0].LastError == nil {
		t.Fatalf("pending = %+v, want the poison event recorded as one failed attempt", pending)
	}
	if strings.Contains(*pending[0].LastError, "simulated publisher bug") || !strings.Contains(*pending[0].LastError, "publisher panicked") {
		t.Errorf("last_error = %q, want the fact of the panic without its text", *pending[0].LastError)
	}

	for range 4 {
		time.Sleep(5 * time.Millisecond)
		poller.pollOnce(ctx)
	}
	if pending, err = repo.GetDispatchable(ctx, 100); err != nil || len(pending) != 0 {
		t.Fatalf("pending = %+v (err %v), want the poison event marked dead after max retries", pending, err)
	}
}
