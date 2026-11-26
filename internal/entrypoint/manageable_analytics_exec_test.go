// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func newStoreBackedAnalyticsExec(t *testing.T) *GatewayExecutor {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyObservabilityAgent, Name: "sentinel", Default: true})
	rt := runtime.NewRuntime(brk, nil, runtime.WithFeatureRegistry(reg))

	factory := func(appConfig map[string]any, s runtime.Services) (*GatewayCore, error) {
		core := NewGatewayCore(&recordingAdapter{}, appConfig)
		core.SetServices(s)
		return core, nil
	}
	exe := NewGatewayExecutor(rt.Services(), factory)
	cfg := map[string]any{
		"namespace":  "test-ns",
		"gateway_id": "gw-1",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": store.TestSQLiteDBURL(t),
		},
	}
	if err := exe.LoadConfig("gw-1", cfg); err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if err := exe.Init(t.Context(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = exe.Stop(context.Background()) })
	return exe
}

func TestExecutorAnalytics_StoreBackedReportsAndError(t *testing.T) {
	exe := newStoreBackedAnalyticsExec(t)
	id := adminIdentity()

	reports := [][]string{
		{"analytics", "tasks", "summary"},
		{"analytics", "tasks", "failed"},
		{"analytics", "tasks", "slow"},
		{"analytics", "agents", "performance"},
		{"analytics", "tokens", "summary"},
		{"analytics", "scheduledTasks", "health"},
		{"analytics", "feedback", "summary"},
	}
	for _, path := range reports {
		res, err := exe.HandleManagement(t.Context(), "get", path, nil, id)
		if err != nil {
			t.Fatalf("report %v: unexpected error: %v", path, err)
		}
		if res == nil {
			t.Errorf("report %v: nil result", path)
		}
	}

	if _, err := exe.HandleManagement(t.Context(), "get", []string{"analytics", "bogus"}, nil, id); err == nil {
		t.Error("unknown report should error")
	}

	_ = exe.Gateways()[0].Store().Close()
	_, err := exe.HandleManagement(t.Context(), "get", []string{"analytics", "tasks", "summary"}, nil, id)
	if err == nil || !strings.Contains(err.Error(), "errorId=") {
		t.Errorf("closed-store query should return an errorId-correlated error, got %v", err)
	}
}

func newAnalyticsExec(t *testing.T, flagEnabled bool) *GatewayExecutor {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	var opts []runtime.Option
	if flagEnabled {
		reg := features.NewRegistry()
		reg.Register(features.Definition{Key: features.KeyObservabilityAgent, Name: "sentinel", Default: true})
		opts = append(opts, runtime.WithFeatureRegistry(reg))
	}
	rt := runtime.NewRuntime(brk, nil, opts...)
	return NewGatewayExecutor(rt.Services(), fakeGatewayFactory)
}

func adminIdentity() *auth.Identity {
	return &auth.Identity{UserID: "admin", Scopes: []string{rbac.ScopeAnalyticsRead}}
}

func TestExecutorAnalytics_FeatureGated(t *testing.T) {
	exe := newAnalyticsExec(t, false)

	_, err := exe.HandleManagement(context.Background(), "get",
		[]string{"analytics", "tasks", "summary"}, nil, adminIdentity())
	if err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("flag-off error = %v, want 'not enabled'", err)
	}

	for _, ep := range exe.ManagementEndpoints() {
		if strings.HasPrefix(ep.Path, "analytics/") {
			t.Errorf("flag off but ManagementEndpoints advertises %q", ep.Path)
		}
	}
}

func TestExecutorAnalytics_FlagOnAdvertisesAndNeedsStore(t *testing.T) {
	exe := newAnalyticsExec(t, true)

	var advertised bool
	for _, ep := range exe.ManagementEndpoints() {
		if ep.Path == "analytics/tasks/summary" {
			advertised = true
		}
	}
	if !advertised {
		t.Error("flag on but analytics/tasks/summary not advertised")
	}

	_, err := exe.HandleManagement(context.Background(), "get",
		[]string{"analytics", "tasks", "summary"}, nil, adminIdentity())
	if err == nil || !strings.Contains(err.Error(), "analytics unavailable") {
		t.Fatalf("no-store error = %v, want 'analytics unavailable'", err)
	}
}

func newAuthzAnalyticsExec(t *testing.T) *GatewayExecutor {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatalf("connect dev broker: %v", err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyObservabilityAgent, Name: "sentinel", Default: true})
	rt := runtime.NewRuntime(brk, nil,
		runtime.WithFeatureRegistry(reg),
		runtime.WithControlAuthorizer(auth.DenyAllControlAuthorizer{}),
	)
	return NewGatewayExecutor(rt.Services(), fakeGatewayFactory)
}

func TestExecutorAnalytics_AuthzEnabled_DeniesUncredentialed(t *testing.T) {
	exe := newAuthzAnalyticsExec(t)
	path := []string{"analytics", "feedback", "summary"}

	if _, err := exe.HandleManagement(context.Background(), "get", path, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "insufficient permissions") {
		t.Errorf("nil identity error = %v, want 'insufficient permissions'", err)
	}

	nonAdmin := &auth.Identity{UserID: "carol", Scopes: []string{"agent:research:invoke"}}
	if _, err := exe.HandleManagement(context.Background(), "get", path, nil, nonAdmin); err == nil ||
		!strings.Contains(err.Error(), "insufficient permissions") {
		t.Errorf("non-admin error = %v, want 'insufficient permissions'", err)
	}

	if _, err := exe.HandleManagement(context.Background(), "get", path, nil, adminIdentity()); err == nil ||
		!strings.Contains(err.Error(), "analytics unavailable") {
		t.Errorf("admin error = %v, want 'analytics unavailable' (gate passed)", err)
	}
}

func TestControlAuthzEnabled(t *testing.T) {
	if (&GatewayExecutor{}).controlAuthzEnabled() {
		t.Error("nil services must report controlAuthzEnabled=false (degrade to community)")
	}
	if newAnalyticsExec(t, true).controlAuthzEnabled() {
		t.Error("noop control authorizer (trust off) must report false")
	}
	if !newAuthzAnalyticsExec(t).controlAuthzEnabled() {
		t.Error("non-noop control authorizer (trust on) must report true")
	}
}

func TestExecutorAnalytics_BadMethodAndScope(t *testing.T) {
	exe := newAnalyticsExec(t, true)

	if _, err := exe.HandleManagement(context.Background(), "post",
		[]string{"analytics", "tasks", "summary"}, nil, adminIdentity()); err == nil ||
		!strings.Contains(err.Error(), "unsupported method") {
		t.Errorf("bad method error = %v, want 'unsupported method'", err)
	}

	denied := &auth.Identity{UserID: "bob", Scopes: []string{"tool:other:invoke"}}
	if _, err := exe.HandleManagement(context.Background(), "get",
		[]string{"analytics", "tasks", "summary"}, nil, denied); err == nil ||
		!strings.Contains(err.Error(), "insufficient permissions") {
		t.Errorf("scope-deny error = %v, want 'insufficient permissions'", err)
	}
}
