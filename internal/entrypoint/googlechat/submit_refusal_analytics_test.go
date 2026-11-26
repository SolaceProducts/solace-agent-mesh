// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package googlechat

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestOnlyARefusedGoogleChatMessageIsReported(t *testing.T) {
	a, sink := analyticsAdapter(t)
	readyToSubmitTo(a, "agent")

	accepted := dmJob(1, "spaces/DM1/messages/accepted", "hello").ev
	if res := a.process(t.Context(), &accepted); res.outcome != outcomeSubmitted {
		t.Fatalf("accepted message outcome = %v, want submitted", res.outcome)
	}
	a.cfg.DefaultAgentName = "does-not-exist"
	refused := dmJob(1, "spaces/DM1/messages/refused", "hello").ev
	if res := a.process(t.Context(), &refused); res.outcome != outcomeDeclined {
		t.Fatalf("refused message outcome = %v, want declined", res.outcome)
	}

	events := sink.rejections(t)
	if len(events) != 1 {
		t.Fatalf("task:submit_rejected delivered %d times for one refused and one accepted message, want 1", len(events))
	}
	want := map[string]any{
		productanalytics.PropRejectReason:  productanalytics.RejectAgentNotFound,
		productanalytics.PropSubmittedFrom: "google_chat",
	}
	for key, v := range want {
		if got := events[0].Properties[key]; got != v {
			t.Errorf("%s = %v, want %v", key, got, v)
		}
	}
}

func analyticsAdapter(t *testing.T) (*Adapter, *captureSink) {
	t.Helper()
	sink := &captureSink{}
	analytics := productanalytics.NewService(productanalytics.Options{
		Sink:    sink,
		Consent: productanalytics.ConsentFunc(func(context.Context) bool { return true }),
		Identity: productanalytics.IdentityFunc(func(context.Context) productanalytics.Identity {
			return productanalytics.Identity{
				Marker:     "11111111-1111-4111-8111-111111111111",
				BindingKey: []byte("0123456789abcdef0123456789abcdef"),
			}
		}),
		Logger: slog.New(slog.DiscardHandler),
		Config: productanalytics.ServiceConfig{BatchSize: 1, FlushInterval: 10 * time.Millisecond},
	})
	if err := analytics.Start(t.Context()); err != nil {
		t.Fatalf("start analytics: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = analytics.Stop(ctx)
	})
	a := NewAdapter(nil)
	a.chatOverride = newFakeChat()
	newEventCore(t, a, map[string]any{"type": "sql", "database_url": store.TestDBURL(t)}, runtime.WithUsageReporter(analytics))
	return a, sink
}

type captureSink struct {
	mu     sync.Mutex
	events []productanalytics.Event
}

func (s *captureSink) Send(_ context.Context, events []productanalytics.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, events...)
	return nil
}

func (s *captureSink) named(name productanalytics.EventName) []productanalytics.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []productanalytics.Event
	for _, ev := range s.events {
		if ev.Name == name {
			out = append(out, ev)
		}
	}
	return out
}

func (s *captureSink) rejections(t *testing.T) []productanalytics.Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.named(productanalytics.EventTaskSubmitRejected)) > 0 {
			time.Sleep(200 * time.Millisecond)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return s.named(productanalytics.EventTaskSubmitRejected)
}
