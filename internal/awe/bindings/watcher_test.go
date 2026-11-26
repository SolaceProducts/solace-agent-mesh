// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bindings

import (
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

const watcherNS = "watch-ns"

func startWatcher(t *testing.T, r *BindingsRegistry) broker.Broker {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	w := NewWatcher(r, brk, watcherNS, slog.Default())
	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Stop(t.Context()) })
	return brk
}

func publishChange(t *testing.T, brk broker.Broker, topicKey string, payload map[string]any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	topic := a2a.ModelConfigUpdateTopic(watcherNS, topicKey)
	if err := brk.PublishGuaranteed(t.Context(), topic, &broker.Message{Topic: topic, Payload: data}); err != nil {
		t.Fatal(err)
	}
}

func TestWatcher_PullsEveryKeyTheChangeNames(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	r := newPullRegistry(t, p)
	for _, key := range []string{"model-1", "general"} {
		if err := r.UpsertModel(t.Context(), key, map[string]any{"id": "v1"}); err != nil {
			t.Fatal(err)
		}
		p.set(key, "v2")
	}
	brk := startWatcher(t, r)

	publishChange(t, brk, "model-1", map[string]any{"model_config": map[string]any{}, "model_id": "model-1", "alias": "general"})

	waitBound(t, r, "model-1", "v2")
	waitBound(t, r, "general", "v2")
}

func TestWatcher_DropsTheAliasARenameReplaced(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	r := newPullRegistry(t, p)
	if err := r.UpsertModel(t.Context(), "research", map[string]any{"id": "v1"}); err != nil {
		t.Fatal(err)
	}
	p.set("model-1", "v1")
	p.set("analysis", "v1")
	brk := startWatcher(t, r)

	publishChange(t, brk, "model-1", map[string]any{"model_config": map[string]any{}, "model_id": "model-1", "alias": "analysis", "previous_alias": "research"})

	waitBound(t, r, "research", "")
}

func TestWatcher_IgnoresMessagesThatAreNotAChange(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	r := newPullRegistry(t, p)
	r.Want(func(string) bool { return true })
	brk := startWatcher(t, r)

	publishChange(t, brk, "bootstrap/model-1", map[string]any{
		"component_id": "agent", "component_type": "agent", "model_id": "model-1",
		"reply_to": a2a.ModelConfigBootstrapResponseTopic(watcherNS, "model-1", "agent"),
	})
	publishChange(t, brk, "response/model-1/agent", map[string]any{"model_config": map[string]any{"model": "m"}})
	publishChange(t, brk, "general", map[string]any{"model_config": map[string]any{"model": "m"}})
	time.Sleep(50 * time.Millisecond)

	if got := p.calls.Load(); got != 0 {
		t.Fatalf("platform pulled %d times, want 0", got)
	}
}

func TestWatcher_StopEndsTheSubscription(t *testing.T) {
	t.Parallel()
	p := newPlatformStub()
	r := newPullRegistry(t, p)
	r.Want(func(string) bool { return true })
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	w := NewWatcher(r, brk, watcherNS, slog.Default())
	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := w.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}

	publishChange(t, brk, "general", map[string]any{"model_id": "model-1", "alias": "general"})
	time.Sleep(50 * time.Millisecond)

	if got := p.calls.Load(); got != 0 {
		t.Fatalf("platform pulled %d times after Stop, want 0", got)
	}
}

func TestWatcher_NilBrokerIsANoOp(t *testing.T) {
	t.Parallel()
	w := NewWatcher(New(), nil, watcherNS, nil)
	if err := w.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := w.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}
