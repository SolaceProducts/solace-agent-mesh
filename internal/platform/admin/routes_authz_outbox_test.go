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
	"net/http"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type testGWE struct {
	deps *RouteDeps
}

func startTestGWE(t *testing.T, brk broker.Broker, dbURL string) *testGWE {
	t.Helper()
	core := platpkg.NewPlatformCore(
		map[string]any{"namespace": "test-ns", "database_url": dbURL},
		platpkg.ExtensionPoints{},
	)
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()))
	core.SetServices(rt.Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	deps := &RouteDeps{IsRBACAuthority: true}
	RegisterRoutesWithDeps(http.NewServeMux(), core, deps)
	deps.AuthzLookupHandler.cache.Set(cachedLookupKey, true)
	for _, svc := range deps.BackgroundServices {
		switch svc.(type) {
		case *platpkg.OutboxPoller, *AuthzChangeListener:
			if err := svc.Start(t.Context()); err != nil {
				t.Fatalf("start %T: %v", svc, err)
			}
			t.Cleanup(func() { _ = svc.Stop(context.Background()) }) //nolint:contextcheck
		}
	}
	if !waitForCondition(t, 10*time.Second, func() bool {
		_, cached := deps.AuthzLookupHandler.cache.Get(cachedLookupKey)
		return !cached
	}) {
		t.Fatal("the authz change listener did not rebuild at start")
	}
	return &testGWE{deps: deps}
}

func TestRegisterRoutes_LoginPathWritersRecordAuthzOutboxEvents(t *testing.T) {
	t.Parallel()
	gwe := startTestGWE(t, connectedDevBroker(t), platformCoreDBURL(t))

	resolver, ok := gwe.deps.AuthzLookupHandler.userResolver.(*usersRepoIdentityResolver)
	if !ok || resolver.changes.repo == nil {
		t.Error("the identity resolver records no authz outbox events")
	}
	for _, svc := range gwe.deps.BackgroundServices {
		if h, ok := svc.(*UserUpsertHandler); ok && h.changes.repo != nil {
			return
		}
	}
	t.Error("the login upsert handler records no authz outbox events")
}

func TestRegisterRoutes_InitialRebuildFailureLevel(t *testing.T) {
	for _, tc := range []struct {
		name      string
		broker    bool
		wantLevel string
	}{
		{"with a broker", true, "WARN"},
		{"without a broker", false, "ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })

			core := platpkg.NewPlatformCore(
				map[string]any{"namespace": "test-ns", "database_url": platformCoreDBURL(t)},
				platpkg.ExtensionPoints{},
			)
			var brk broker.Broker
			if tc.broker {
				brk = connectedDevBroker(t)
			}
			core.SetServices(runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default())).Services())
			if err := core.Init(t.Context(), nil); err != nil {
				t.Fatalf("init platform core: %v", err)
			}
			if _, err := core.DB().ExecContext(t.Context(), `DROP TABLE rbac_roles`); err != nil {
				t.Fatalf("drop rbac_roles: %v", err)
			}
			RegisterRoutesWithDeps(http.NewServeMux(), core, &RouteDeps{IsRBACAuthority: true})

			var found bool
			for line := range bytes.SplitSeq(logs.Bytes(), []byte("\n")) {
				var rec map[string]any
				if json.Unmarshal(line, &rec) != nil {
					continue
				}
				if msg, _ := rec["msg"].(string); bytes.HasPrefix([]byte(msg), []byte("rbac composer initial rebuild failed")) {
					found = true
					if rec["level"] != tc.wantLevel {
						t.Errorf("level = %v, want %s: %s", rec["level"], tc.wantLevel, line)
					}
				}
			}
			if !found {
				t.Fatalf("no initial rebuild failure logged: %s", logs.String())
			}
		})
	}
}
