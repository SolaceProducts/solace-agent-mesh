// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
)

func TestGatewayExecutor_AuthExpectedGuard(t *testing.T) {
	const warnFragment = "without RBAC enforcement"

	newExecWithBuf := func(t *testing.T) (*GatewayExecutor, *bytes.Buffer) {
		t.Helper()
		exe := NewGatewayExecutor(testServices(t), fakeGatewayFactory)
		var buf bytes.Buffer
		exe.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
		return exe, &buf
	}

	nonEnforcing := map[string]any{"gateway_id": "gw1"}
	enforcing := map[string]any{
		"gateway_id":            "gw1",
		"authorization_service": map[string]any{"type": "default_rbac"},
	}

	tests := []struct {
		name         string
		authExpected bool
		appConfig    map[string]any
		wantWarn     bool
	}{
		{name: "auth expected + ungated entrypoint warns", authExpected: true, appConfig: nonEnforcing, wantWarn: true},
		{name: "auth expected + enforcing entrypoint stays quiet", authExpected: true, appConfig: enforcing, wantWarn: false},
		{name: "auth not expected: no warn even when ungated", authExpected: false, appConfig: nonEnforcing, wantWarn: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exe, buf := newExecWithBuf(t)
			exe.SetGatewayHooks(&GatewayHooks{AuthExpected: tc.authExpected})
			core, err := fakeGatewayFactory(tc.appConfig, exe.svc)
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			exe.applyGatewayHooks(core, tc.appConfig)

			gotWarn := strings.Contains(buf.String(), warnFragment)
			if gotWarn != tc.wantWarn {
				t.Fatalf("warn=%v, want %v; log: %s", gotWarn, tc.wantWarn, buf.String())
			}
		})
	}
}

func TestGatewayExecutor_BuildResolversFailure_FailsClosed(t *testing.T) {
	exe := NewGatewayExecutor(testServices(t), fakeGatewayFactory)
	var buf bytes.Buffer
	exe.logger = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	exe.SetGatewayHooks(&GatewayHooks{
		AuthExpected: true,
		BuildResolvers: func(string, map[string]any) (ScopeResolver, IdentityResolver, error) {
			return nil, nil, errors.New("boom")
		},
	})

	enforcing := map[string]any{
		"gateway_id":            "gw1",
		"authorization_service": map[string]any{"type": "default_rbac"},
	}
	core, err := fakeGatewayFactory(enforcing, exe.svc)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	exe.applyGatewayHooks(core, enforcing)

	if !strings.Contains(buf.String(), "deny all agent invocations") {
		t.Fatalf("expected ERROR about denying all invocations, got: %s", buf.String())
	}

	ctx := auth.ContextWithSubject(context.Background(), auth.NewTestSubject(auth.SubjectFields{
		UserID:        "alice",
		Authenticated: true,
	}))
	if core.CanDelegate(ctx, stagedCaller(ctx), "some-agent") {
		t.Fatal("CanDelegate allowed a scope-less identity after resolver-build failure; must fail closed")
	}
}
