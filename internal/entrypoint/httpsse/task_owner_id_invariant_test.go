// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestTaskOwnerID_WriterAndReaderAgree(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	defaultIdentity := core.Config().DefaultUserIdentity
	if defaultIdentity == "" {
		t.Fatal("test core has no default_user_identity; the fallback rung would be untested")
	}

	reachable := []struct {
		name     string
		identity *auth.Subject
	}{
		{"sub and email", auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Email: "alice@example.com", Authenticated: true})},
		{"sub only", auth.NewTestSubject(auth.SubjectFields{UserID: "alice", Authenticated: true})},
		{"email-derived sub, as oidcauth produces", auth.NewTestSubject(auth.SubjectFields{UserID: "alice@example.com", Email: "alice@example.com", Authenticated: true})},
		{"system principal", auth.NewTestSubject(auth.SubjectFields{UserID: "system:scheduler", Authenticated: true})},
	}
	for _, tc := range reachable {
		t.Run(tc.name, func(t *testing.T) {
			r := requestWithIdentity(tc.identity)
			writer := auth.UserIDFromRequest(r, defaultIdentity)
			reader := resolveUserID(r, core)
			if writer != reader {
				t.Errorf("writer resolved %q but reader resolved %q — a task written by this identity would be unreadable by it",
					writer, reader)
			}
		})
	}

	t.Run("empty sub with an email is the divergent shape oidcauth rejects", func(t *testing.T) {
		r := requestWithIdentity(auth.NewTestSubject(auth.SubjectFields{Email: "alice@example.com", Authenticated: true}))
		writer := auth.UserIDFromRequest(r, defaultIdentity)
		reader := resolveUserID(r, core)
		if writer == reader {
			t.Fatalf("both resolvers returned %q — this shape no longer diverges, so this test and its comment are stale and should be deleted", writer)
		}
		if writer != defaultIdentity {
			t.Errorf("writer resolved %q, want the configured default %q", writer, defaultIdentity)
		}
		if reader != "alice@example.com" {
			t.Errorf("reader resolved %q, want the email rung", reader)
		}
	})
}

func TestTaskOwnerID_NoIdentityFallsBackAlike(t *testing.T) {
	_, core, _, _, _ := setupTestAdapterWithDB(t)
	defaultIdentity := core.Config().DefaultUserIdentity

	r := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	if writer, reader := auth.UserIDFromRequest(r, defaultIdentity), resolveUserID(r, core); writer != reader {
		t.Errorf("with no identity: writer %q, reader %q", writer, reader)
	}
}

func requestWithIdentity(id *auth.Subject) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	return r.WithContext(auth.ContextWithSubject(r.Context(), id))
}

func TestTaskOwnerID_NoDefaultIdentityDivergesSafely(t *testing.T) {
	core := coreWithoutDefaultIdentity(t)
	if core.Config().DefaultUserIdentity != "" {
		t.Fatalf("fixture has default_user_identity %q; this test needs it empty",
			core.Config().DefaultUserIdentity)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	writer := auth.UserIDFromRequest(r, core.Config().DefaultUserIdentity)
	reader := resolveUserID(r, core)

	if writer == "" && reader == "" {
		t.Fatal("both resolvers returned the empty string — task.UserID == callerUserID would then grant every caller every unattributed task")
	}
	if writer == reader {
		t.Fatalf("both resolvers returned %q — the chains no longer diverge here, so this test and its comment are stale and should be deleted", writer)
	}
	if writer != "" {
		t.Errorf("writer resolved %q, want the empty default rung", writer)
	}
	if reader != "anonymous" {
		t.Errorf("reader resolved %q, want its own anonymous literal", reader)
	}
}

func coreWithoutDefaultIdentity(t *testing.T) *entrypoint.GatewayCore {
	t.Helper()

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	core := entrypoint.NewGatewayCore(NewHTTPSSEAdapter(), map[string]any{
		"namespace":             "test-ns",
		"session_secret_key":    "test-secret",
		"gateway_id":            "test-gw",
		"fastapi_host":          "127.0.0.1",
		"fastapi_port":          0,
		"default_user_identity": "",
	})

	flagRegistry := features.NewRegistry()
	if err := features.LoadFlags(flagRegistry); err != nil {
		t.Fatalf("load community flags: %v", err)
	}
	core.SetServices(runtime.NewRuntime(brk, nil,
		runtime.WithLogger(slog.Default()),
		runtime.WithFeatureRegistry(flagRegistry),
	).Services())

	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("init core: %v", err)
	}
	return core
}
