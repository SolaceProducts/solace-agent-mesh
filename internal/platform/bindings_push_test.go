// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/config"
	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type fakeAWE struct {
	mu         sync.Mutex
	patchBodys []json.RawMessage
}

func (*fakeAWE) Init(_ context.Context, _ config.Config) error { return nil }
func (*fakeAWE) Start(_ context.Context) error                 { return nil }
func (*fakeAWE) Stop(_ context.Context) error                  { return nil }
func (*fakeAWE) Health() error                                 { return nil }

func (*fakeAWE) ManagementEndpoints() []control.Endpoint {
	return []control.Endpoint{{Method: "patch", Path: "bindings"}}
}

func (f *fakeAWE) HandleManagement(_ context.Context, method string, path []string, body json.RawMessage, _ *auth.Identity) (any, error) {
	if method == "patch" && len(path) == 1 && path[0] == "bindings" {
		f.mu.Lock()
		f.patchBodys = append(f.patchBodys, append(json.RawMessage(nil), body...))
		f.mu.Unlock()
		return map[string]any{"ok": true}, nil
	}
	return nil, nil
}

func (f *fakeAWE) Snapshot() []json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]json.RawMessage, len(f.patchBodys))
	copy(out, f.patchBodys)
	return out
}

func setupPusherTest(t *testing.T) (*BindingsPusher, *fakeAWE, func()) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	awe := &fakeAWE{}
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	rt.Register("awe", awe)

	ctrlSvc := control.NewService(rt, "test-ns")
	rt.Register("control", ctrlSvc)

	if err := rt.Init(context.Background()); err != nil {
		t.Fatalf("init rt: %v", err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start rt: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	client := control.NewClient(brk, "test-ns", control.WithTimeout(2*time.Second))
	pusher := NewBindingsPusher(client, slog.Default())

	cleanup := func() {
		_ = rt.Stop(context.Background())
		_ = brk.Disconnect(context.Background())
	}
	return pusher, awe, cleanup
}

func setupFanoutPusherTest(t *testing.T, registerSTR bool) (*BindingsPusher, *fakeAWE, *fakeAWE, func()) {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}

	awe := &fakeAWE{}
	str := &fakeAWE{}
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	rt.Register("awe", awe)
	if registerSTR {
		rt.Register("str", str)
	}
	rt.Register("control", control.NewService(rt, "test-ns"))

	if err := rt.Init(context.Background()); err != nil {
		t.Fatalf("init rt: %v", err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start rt: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	client := control.NewClient(brk, "test-ns", control.WithTimeout(30*time.Second))
	pusher := NewBindingsPusher(client, slog.Default(), AWEBindingApp, STRBindingApp)

	cleanup := func() {
		_ = rt.Stop(context.Background())
		_ = brk.Disconnect(context.Background())
	}
	return pusher, awe, str, cleanup
}

func eventually(t *testing.T, within time.Duration, fn func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fn()
}

func TestBindingsPusher_FansOutToSTR(t *testing.T) {
	pusher, awe, str, cleanup := setupFanoutPusherTest(t, true)
	defer cleanup()

	pusher.UpsertModel(context.Background(), "general", map[string]any{"model": "m"})

	if got := len(awe.Snapshot()); got != 1 {
		t.Fatalf("awe recorded %d patches, want 1", got)
	}
	if !eventually(t, 3*time.Second, func() bool { return len(str.Snapshot()) == 1 }) {
		t.Fatalf("str recorded %d patches, want 1", len(str.Snapshot()))
	}
}

func TestBindingsPusher_SecondaryAbsentDoesNotBlockCaller(t *testing.T) {
	pusher, awe, str, cleanup := setupFanoutPusherTest(t, false)
	defer cleanup()

	start := time.Now()
	pusher.UpsertModel(context.Background(), "general", map[string]any{"model": "m"})
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("UpsertModel blocked %v on absent secondary; want prompt return", elapsed)
	}
	if got := len(awe.Snapshot()); got != 1 {
		t.Fatalf("awe recorded %d patches, want 1", got)
	}
	if got := len(str.Snapshot()); got != 0 {
		t.Fatalf("str (unregistered) recorded %d patches, want 0", got)
	}
}

type erroringApp struct {
	mu    sync.Mutex
	calls int
}

func (*erroringApp) Init(context.Context, config.Config) error { return nil }
func (*erroringApp) Start(context.Context) error               { return nil }
func (*erroringApp) Stop(context.Context) error                { return nil }
func (*erroringApp) Health() error                             { return nil }
func (*erroringApp) ManagementEndpoints() []control.Endpoint {
	return []control.Endpoint{{Method: "patch", Path: "bindings"}}
}
func (a *erroringApp) HandleManagement(_ context.Context, method string, path []string, _ json.RawMessage, _ *auth.Identity) (any, error) {
	if method == "patch" && len(path) == 1 && path[0] == "bindings" {
		a.mu.Lock()
		a.calls++
		a.mu.Unlock()
		return nil, fmt.Errorf("simulated str binding apply failure")
	}
	return nil, nil
}
func (a *erroringApp) count() int { a.mu.Lock(); defer a.mu.Unlock(); return a.calls }

func TestBindingsPusher_SecondaryRetries(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect broker: %v", err)
	}
	strApp := &erroringApp{}
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	rt.Register("str", strApp)
	rt.Register("control", control.NewService(rt, "test-ns"))
	if err := rt.Init(context.Background()); err != nil {
		t.Fatalf("init rt: %v", err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("start rt: %v", err)
	}
	t.Cleanup(func() {
		_ = rt.Stop(context.Background())
		_ = brk.Disconnect(context.Background())
	})
	time.Sleep(50 * time.Millisecond)

	pusher := NewBindingsPusher(control.NewClient(brk, "test-ns", control.WithTimeout(2*time.Second)),
		slog.Default(), AWEBindingApp, STRBindingApp)

	pusher.pushSecondary(context.Background(), STRBindingApp, "upsert",
		[]byte(`{"models":{"general":{"model":"m"}}}`), 1)

	if got := strApp.count(); got != secondaryPushAttempts {
		t.Fatalf("str received %d push attempts, want %d", got, secondaryPushAttempts)
	}
}

func TestBindingsPusher_UpsertModel(t *testing.T) {
	pusher, awe, cleanup := setupPusherTest(t)
	defer cleanup()

	pusher.UpsertModel(context.Background(), "planning", map[string]any{"model": "gpt-4"})

	bodies := awe.Snapshot()
	if len(bodies) != 1 {
		t.Fatalf("expected 1 recorded patch, got %d", len(bodies))
	}
	var parsed map[string]any
	if err := json.Unmarshal(bodies[0], &parsed); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	models := parsed["models"].(map[string]any)
	planning := models["planning"].(map[string]any)
	if planning["model"] != "gpt-4" {
		t.Errorf("model = %v, want gpt-4", planning["model"])
	}
}

func TestBindingsPusher_DeleteModel(t *testing.T) {
	pusher, awe, cleanup := setupPusherTest(t)
	defer cleanup()

	pusher.DeleteModel(context.Background(), "planning")

	bodies := awe.Snapshot()
	if len(bodies) != 1 {
		t.Fatalf("expected 1 recorded patch, got %d", len(bodies))
	}
	var parsed map[string]any
	if err := json.Unmarshal(bodies[0], &parsed); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	models := parsed["models"].(map[string]any)
	if v, ok := models["planning"]; !ok || v != nil {
		t.Errorf("expected models.planning == null, got %v (ok=%v)", v, ok)
	}
}

func TestBindingsPusher_UpsertAll_MultipleModels(t *testing.T) {
	pusher, awe, cleanup := setupPusherTest(t)
	defer cleanup()

	pusher.UpsertAll(context.Background(), map[string]map[string]any{
		"planning": {"model": "gpt-4"},
		"general":  {"model": "gpt-3.5"},
	})

	bodies := awe.Snapshot()
	if len(bodies) != 1 {
		t.Fatalf("expected 1 recorded patch, got %d", len(bodies))
	}
	var parsed map[string]any
	if err := json.Unmarshal(bodies[0], &parsed); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	models := parsed["models"].(map[string]any)
	if len(models) != 2 {
		t.Errorf("expected 2 models, got %d: %v", len(models), models)
	}
}

func TestBindingsPusher_UpsertAll_EmptyIsNoop(t *testing.T) {
	pusher, awe, cleanup := setupPusherTest(t)
	defer cleanup()

	pusher.UpsertAll(context.Background(), nil)
	pusher.UpsertAll(context.Background(), map[string]map[string]any{})

	if bodies := awe.Snapshot(); len(bodies) != 0 {
		t.Errorf("expected no patches for empty input, got %d", len(bodies))
	}
}

func TestBindingsPusher_NilClient_NoPanic(t *testing.T) {
	pusher := NewBindingsPusher(nil, nil)
	pusher.UpsertModel(context.Background(), "a", map[string]any{})
	pusher.DeleteModel(context.Background(), "a")
	pusher.UpsertAll(context.Background(), map[string]map[string]any{"a": {}})
	pusher.UpsertModelByKeys(context.Background(), map[string]any{}, "a", "b")
	pusher.DeleteModelByKeys(context.Background(), "a", "b")
	pusher.UpsertAndDeleteByKeys(context.Background(), map[string]any{}, []string{"a"}, []string{"old"})
}

func TestBindingsPusher_UpsertAndDeleteByKeys_AtomicRename(t *testing.T) {
	pusher, awe, cleanup := setupPusherTest(t)
	defer cleanup()

	raw := map[string]any{"model": "gpt-5"}
	pusher.UpsertAndDeleteByKeys(context.Background(), raw,
		[]string{"new-alias", "uuid-123"},
		[]string{"old-alias"},
	)

	bodies := awe.Snapshot()
	if len(bodies) != 1 {
		t.Fatalf("expected 1 patch (combined rename), got %d", len(bodies))
	}
	var parsed map[string]any
	if err := json.Unmarshal(bodies[0], &parsed); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	models := parsed["models"].(map[string]any)
	for _, key := range []string{"new-alias", "uuid-123"} {
		if v, ok := models[key].(map[string]any); !ok || v == nil {
			t.Errorf("expected upsert entry for %q, got %v", key, models[key])
		}
	}
	if v, has := models["old-alias"]; !has || v != nil {
		t.Errorf("expected models[old-alias] == null, got %v (has=%v)", v, has)
	}
}

func TestBindingsPusher_UpsertAndDeleteByKeys_AllEmptyIsNoop(t *testing.T) {
	pusher, awe, cleanup := setupPusherTest(t)
	defer cleanup()

	pusher.UpsertAndDeleteByKeys(context.Background(), map[string]any{}, nil, nil)
	pusher.UpsertAndDeleteByKeys(context.Background(), map[string]any{}, []string{""}, []string{""})

	if bodies := awe.Snapshot(); len(bodies) != 0 {
		t.Errorf("expected no patches when all keys are empty, got %d", len(bodies))
	}
}

func TestBindingsPusher_UpsertModelByKeys_MultiKey(t *testing.T) {
	pusher, awe, cleanup := setupPusherTest(t)
	defer cleanup()

	raw := map[string]any{"model": "gpt-4", "api_key": "sk-v1"}
	pusher.UpsertModelByKeys(context.Background(), raw, "general", "uuid-123")

	bodies := awe.Snapshot()
	if len(bodies) != 1 {
		t.Fatalf("expected 1 patch (one PATCH carries both keys), got %d", len(bodies))
	}
	var parsed map[string]any
	if err := json.Unmarshal(bodies[0], &parsed); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	models := parsed["models"].(map[string]any)
	if len(models) != 2 {
		t.Fatalf("expected 2 keyed entries, got %d: %v", len(models), models)
	}
	for _, key := range []string{"general", "uuid-123"} {
		entry, ok := models[key].(map[string]any)
		if !ok {
			t.Errorf("missing %q key in patch body: %v", key, models)
			continue
		}
		if entry["model"] != "gpt-4" || entry["api_key"] != "sk-v1" {
			t.Errorf("entry for %q does not match raw config: %v", key, entry)
		}
	}
}

func TestBindingsPusher_UpsertModelByKeys_SkipsEmptyKeys(t *testing.T) {
	pusher, awe, cleanup := setupPusherTest(t)
	defer cleanup()

	raw := map[string]any{"model": "gpt-4"}
	pusher.UpsertModelByKeys(context.Background(), raw, "general", "", "uuid-123", "")

	bodies := awe.Snapshot()
	if len(bodies) != 1 {
		t.Fatalf("expected 1 patch, got %d", len(bodies))
	}
	var parsed map[string]any
	if err := json.Unmarshal(bodies[0], &parsed); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	models := parsed["models"].(map[string]any)
	if len(models) != 2 {
		t.Errorf("expected 2 entries (empty strings filtered), got %d: %v", len(models), models)
	}
	if _, has := models[""]; has {
		t.Errorf("empty key reached the wire: %v", models)
	}
}

func TestBindingsPusher_UpsertModelByKeys_AllEmptyIsNoop(t *testing.T) {
	pusher, awe, cleanup := setupPusherTest(t)
	defer cleanup()

	pusher.UpsertModelByKeys(context.Background(), map[string]any{"model": "gpt-4"})
	pusher.UpsertModelByKeys(context.Background(), map[string]any{"model": "gpt-4"}, "")
	pusher.UpsertModelByKeys(context.Background(), map[string]any{"model": "gpt-4"}, "", "")

	if bodies := awe.Snapshot(); len(bodies) != 0 {
		t.Errorf("expected no patches when all keys are empty, got %d", len(bodies))
	}
}

func TestBindingsPusher_DeleteModelByKeys_MultiKey(t *testing.T) {
	pusher, awe, cleanup := setupPusherTest(t)
	defer cleanup()

	pusher.DeleteModelByKeys(context.Background(), "general", "uuid-123")

	bodies := awe.Snapshot()
	if len(bodies) != 1 {
		t.Fatalf("expected 1 patch, got %d", len(bodies))
	}
	var parsed map[string]any
	if err := json.Unmarshal(bodies[0], &parsed); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	models := parsed["models"].(map[string]any)
	if len(models) != 2 {
		t.Fatalf("expected 2 keyed entries, got %d: %v", len(models), models)
	}
	for _, key := range []string{"general", "uuid-123"} {
		v, ok := models[key]
		if !ok || v != nil {
			t.Errorf("expected models[%q] == null, got %v (ok=%v)", key, v, ok)
		}
	}
}

func TestBindingsPusher_DeleteModelByKeys_SkipsEmptyKeys(t *testing.T) {
	pusher, awe, cleanup := setupPusherTest(t)
	defer cleanup()

	pusher.DeleteModelByKeys(context.Background(), "general", "", "uuid-123")

	bodies := awe.Snapshot()
	if len(bodies) != 1 {
		t.Fatalf("expected 1 patch, got %d", len(bodies))
	}
	var parsed map[string]any
	if err := json.Unmarshal(bodies[0], &parsed); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	models := parsed["models"].(map[string]any)
	if len(models) != 2 {
		t.Errorf("expected 2 entries (empty string filtered), got %d: %v", len(models), models)
	}
	if _, has := models[""]; has {
		t.Errorf("empty key reached the wire: %v", models)
	}
}

func TestBindingsPusher_DeleteModelByKeys_AllEmptyIsNoop(t *testing.T) {
	pusher, awe, cleanup := setupPusherTest(t)
	defer cleanup()

	pusher.DeleteModelByKeys(context.Background())
	pusher.DeleteModelByKeys(context.Background(), "")
	pusher.DeleteModelByKeys(context.Background(), "", "")

	if bodies := awe.Snapshot(); len(bodies) != 0 {
		t.Errorf("expected no patches when all keys are empty, got %d", len(bodies))
	}
}
