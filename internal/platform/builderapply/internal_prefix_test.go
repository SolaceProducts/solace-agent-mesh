// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apikey"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func serveAs(mux *http.ServeMux, t *testing.T, path, body string, scopes []string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req = req.WithContext(auth.ContextWithSubject(t.Context(), identityWith(scopes...)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestInternalPrefix_BuildRouteIsGatedLikeV1(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	mux := http.NewServeMux()
	(&handler{svc: svc, logger: discardLogger()}).mount(mux)

	connectorBuild := `{"dryRun":true,"components":[
		{"kind":"connector","name":"test-db","content":"{\"name\":\"test-db\",\"description\":\"A connector used in tests.\",\"type\":\"sql\",\"subtype\":\"postgres\",\"values\":{\"hostname\":\"db\"}}"}
	]}`
	cases := []struct {
		name   string
		scopes []string
		want   int
	}{
		{"no scope", nil, http.StatusForbidden},
		{"read scopes only", buildReadScopes, http.StatusForbidden},
		{"passes the any-of gate but lacks the connector scope", withReads(rbac.ScopeAgentBuilderCreate), http.StatusForbidden},
		{"holds the connector scope", withReads(rbac.ScopeConnectorCreate), http.StatusOK},
	}
	for _, path := range []string{BuildsPath, InternalBuildsPath} {
		for _, tc := range cases {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				rec := serveAs(mux, t, path, connectorBuild, tc.scopes)
				if rec.Code != tc.want {
					t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
				}
			})
		}
	}
}

func TestInternalPrefix_RemovalRouteIsGatedLikeV1(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	mux := http.NewServeMux()
	(&handler{svc: svc, logger: discardLogger()}).mount(mux)

	buildInSession(t, svc, sessionA, false, agentComponent("internal_scoped_agent"))
	agent := agentNamed(t.Context(), t, svc, "internal_scoped_agent")
	body := `{"builderSessionId":"` + sessionA + `","resources":[{"kind":"agent","id":"` + agent.ID + `"}]}`

	for _, path := range []string{RemovalsPath, InternalRemovalsPath} {
		t.Run(path+"/no delete scope", func(t *testing.T) {
			for _, scopes := range [][]string{nil, allBuildScopes} {
				if rec := serveAs(mux, t, path, body, scopes); rec.Code != http.StatusForbidden {
					t.Errorf("status = %d, want 403: %s", rec.Code, rec.Body.String())
				}
			}
		})

		t.Run(path+"/passes the gate but lacks the kind's delete scope", func(t *testing.T) {
			rec := serveAs(mux, t, path, body, []string{rbac.ScopeWorkflowBuilderDelete})
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 with a per-resource refusal: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), rbac.ScopeAgentBuilderDelete) || !strings.Contains(rec.Body.String(), removalRefused) {
				t.Errorf("want the agent refused naming %s: %s", rbac.ScopeAgentBuilderDelete, rec.Body.String())
			}
			if got := storedAgentNames(t, svc); len(got) != 1 {
				t.Errorf("agents = %v, want the agent kept", got)
			}
		})
	}
}

func TestInternalPrefix_BuildRouteGateIsTheOnlyRefusalOfAnEmptyDryRun(t *testing.T) {
	t.Parallel()
	svc := newTestService(t)
	mux := http.NewServeMux()
	(&handler{svc: svc, logger: discardLogger()}).mount(mux)

	emptyDryRun := `{"builderSessionId":"` + sessionA + `","dryRun":true,"components":[]}`
	cases := []struct {
		name   string
		scopes []string
		want   int
	}{
		{"no scope", nil, http.StatusForbidden},
		{"read scopes only", buildReadScopes, http.StatusForbidden},
		{"one build entry scope with the reads", withReads(rbac.ScopeEntrypointCreate), http.StatusOK},
	}
	for _, path := range []string{BuildsPath, InternalBuildsPath} {
		for _, tc := range cases {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				rec := serveAs(mux, t, path, emptyDryRun, tc.scopes)
				if rec.Code != tc.want {
					t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
				}
			})
		}
	}
}

const presentedKey = "samk_AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"

type universalKeyAuthority struct{}

func (universalKeyAuthority) Validate(context.Context, string) (apikey.Resolution, error) {
	return apikey.Resolution{
		KeyID:    "key-1",
		OwnerID:  "0192f3aa-7c31-70b2-9e4d-9b1f2a5c8e77",
		OwnerSub: "system:ci-deployer",
		Scopes:   []string{"*:*:*"},
	}, nil
}

func TestInternalPrefix_APIKeyIsRefusedOnBuildsAndRemovals(t *testing.T) {
	t.Parallel()
	h := &handler{svc: newTestService(t), logger: discardLogger()}
	core := platpkg.NewPlatformCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": "sqlite:///" + filepath.Join(t.TempDir(), "platform.db"),
	}, platpkg.ExtensionPoints{
		RegisterRoutes: func(mux *http.ServeMux, _ *platpkg.PlatformCore) { h.mount(mux) },
		AuthMiddleware: entplatform.NewAuthMiddleware(nil, nil, universalKeyAuthority{}, discardLogger()),
	})
	core.SetServices(runtime.NewRuntime(nil, nil, runtime.WithLogger(discardLogger())).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("platform init: %v", err)
	}

	for _, path := range []string{BuildsPath, InternalBuildsPath, RemovalsPath, InternalRemovalsPath} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
			req.Header.Set("Authorization", "Bearer "+presentedKey)
			rec := httptest.NewRecorder()
			core.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "API keys can only call the Platform configuration API") {
				t.Errorf("status = %d, want the API-key refusal; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}
