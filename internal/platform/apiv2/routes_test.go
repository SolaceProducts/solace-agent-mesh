// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/openapispec"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

var pathWildcard = regexp.MustCompile(`\{[^}]*\}`)

func TestRegisterRoutes_MountsEveryRow(t *testing.T) {
	core, deps := newTestCore(t)
	logger := slog.New(slog.DiscardHandler)
	mux := http.NewServeMux()
	RegisterRoutes(mux, deps, core, logger)

	routes := routeTable(deps, core, logger)
	if len(routes) == 0 {
		t.Fatal("routeTable is empty, so nothing would be checked")
	}
	for _, rt := range routes {
		path := platpkg.APIV2Prefix + pathWildcard.ReplaceAllString(rt.Path, "x")
		if _, pattern := mux.Handler(httptest.NewRequest(rt.Method, path, nil)); pattern == "" {
			t.Errorf("%s %s is not mounted", rt.Method, path)
		}
	}

	nilDepsMux := http.NewServeMux()
	RegisterRoutes(nilDepsMux, nil, core, logger)
	if _, pattern := nilDepsMux.Handler(httptest.NewRequest(http.MethodGet, platpkg.APIV2Prefix+"/health", nil)); pattern != "" {
		t.Errorf("nil deps mounted %q", pattern)
	}
}

func TestRegisterRoutes_MountsTheRoutes(t *testing.T) {
	core, deps := newTestCore(t)
	mux := http.NewServeMux()
	RegisterRoutes(mux, deps, core, slog.New(slog.DiscardHandler))

	for _, path := range []string{platpkg.APIV2Prefix + "/health", openapispec.Path, openapispec.ViewerPath, openapispec.ViewerAssetPath(openapispec.ViewerAssets[0].Name)} {
		if _, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, path, nil)); pattern == "" {
			t.Errorf("%s not mounted", path)
		}
	}
}

func TestRouteTable_BuildsWithoutAFeatureRegistry(t *testing.T) {
	core, deps := newTestCore(t)
	deps.FeatureRegistry = nil

	if len(routeTable(deps, core, slog.New(slog.DiscardHandler))) == 0 {
		t.Error("routeTable is empty with a nil FeatureRegistry")
	}
	mux := http.NewServeMux()
	RegisterRoutes(mux, deps, core, slog.New(slog.DiscardHandler))
	if _, pattern := mux.Handler(httptest.NewRequest(http.MethodGet, platpkg.APIV2Prefix+"/health", nil)); pattern == "" {
		t.Error("v2 not mounted with a nil FeatureRegistry")
	}
}

func TestRegister_PrefixesPathAndGatesScope(t *testing.T) {
	const id = "019f9b46-77bc-76ba-a494-017487b5f5c4"
	mux := http.NewServeMux()
	register(mux, nil, false, []Route{{
		Method: http.MethodGet,
		Path:   "/widgets/{id}",
		Scope:  rbac.ScopeAgentBuilderRead,
		Handler: Handle(func(_ context.Context, req getWidgetRequest) (widget, error) {
			return widget{ID: req.ID, Type: "widget"}, nil
		}, "Unable to get widget"),
	}})

	call := func(path string, scopes ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{
			UserID: "u", Authenticated: true, Scopes: scopes,
		})))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	if rec := call(platpkg.APIV2Prefix+"/widgets/"+id, rbac.ScopeAgentBuilderRead); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), id) {
		t.Errorf("with scope: status %d body %s, want 200 naming %s", rec.Code, rec.Body.String(), id)
	}
	if rec := call(platpkg.APIV2Prefix+"/widgets/"+id, rbac.ScopeConnectorRead); rec.Code != http.StatusForbidden {
		t.Errorf("without scope: status %d, want 403", rec.Code)
	}
	if rec := call("/widgets/"+id, rbac.ScopeAgentBuilderRead); rec.Code != http.StatusNotFound {
		t.Errorf("unprefixed path: status %d, want 404", rec.Code)
	}
}

func TestRegister_OpensMarkedRoutesToAPIKeys(t *testing.T) {
	noContent := Handle(func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil }, "Unable to answer")
	key := auth.NewTestSubject(auth.SubjectFields{UserID: "system:ci", Authenticated: true, AuthMethod: auth.APIKeyAuthMethod, Scopes: []string{"*"}})
	core := platpkg.NewPlatformCore(
		map[string]any{"namespace": "test-ns", "database_url": testTemplate.Clone(t)},
		platpkg.ExtensionPoints{
			RegisterRoutes: func(mux *http.ServeMux, c *platpkg.PlatformCore) {
				register(mux, c, false, []Route{
					{Method: http.MethodGet, Path: "/open", Scope: rbac.ScopeAgentBuilderRead, AllowAPIKey: true, Published: true, Handler: noContent},
					{Method: http.MethodGet, Path: "/unpublished", Scope: rbac.ScopeAgentBuilderRead, AllowAPIKey: true, Handler: noContent},
					{Method: http.MethodGet, Path: "/closed", Scope: rbac.ScopeAgentBuilderRead, Handler: noContent},
				})
			},
			AuthMiddleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					next.ServeHTTP(w, r.WithContext(auth.ContextWithSubject(r.Context(), key)))
				})
			},
		},
	)
	core.SetServices(runtime.NewRuntime(nil, nil, runtime.WithLogger(slog.New(slog.DiscardHandler))).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(context.Background()) })

	for path, want := range map[string]int{
		"/open":        http.StatusNoContent,
		"/unpublished": http.StatusForbidden,
		"/closed":      http.StatusForbidden,
	} {
		rec := httptest.NewRecorder()
		core.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, platpkg.APIV2Prefix+path, nil))
		if rec.Code != want {
			t.Errorf("key on %s: status %d, want %d", path, rec.Code, want)
		}
	}
}

func TestRegister_PublicRouteNeedsNoIdentity(t *testing.T) {
	mux := http.NewServeMux()
	nothing := Handle(func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil }, "Unable to read")
	register(mux, nil, false, []Route{
		{Method: http.MethodGet, Path: "/open", Gate: GatePublic, Handler: nothing},
		{Method: http.MethodGet, Path: "/signed", Gate: GateSignedIn, Handler: nothing},
	})

	for path, want := range map[string]int{
		"/open":   http.StatusNoContent,
		"/signed": http.StatusUnauthorized,
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, platpkg.APIV2Prefix+path, nil))
		if rec.Code != want {
			t.Errorf("anonymous GET %s: status %d, want %d", path, rec.Code, want)
		}
	}
}

func TestRoute_GatedRouteNamesNoScopes(t *testing.T) {
	open := Route{Gate: GatePublic}
	if got := open.Scopes(); len(got) > 0 {
		t.Errorf("scopes() = %q, want none for a route gated outside the catalog", got)
	}
}

func TestRegister_AnyOfScopesAdmitsEither(t *testing.T) {
	const id = "019f9b46-77bc-76ba-a494-017487b5f5c4"
	mux := http.NewServeMux()
	register(mux, nil, false, []Route{{
		Method:      http.MethodGet,
		Path:        "/widgets/{id}",
		AnyOfScopes: []string{rbac.ScopeRBACRead, rbac.ScopeAgentBuilderRead},
		Handler: Handle(func(_ context.Context, req getWidgetRequest) (widget, error) {
			return widget{ID: req.ID, Type: "widget"}, nil
		}, "Unable to get widget"),
	}})

	for scope, want := range map[string]int{
		rbac.ScopeRBACRead:         http.StatusOK,
		rbac.ScopeAgentBuilderRead: http.StatusOK,
		rbac.ScopeConnectorRead:    http.StatusForbidden,
	} {
		req := httptest.NewRequest(http.MethodGet, platpkg.APIV2Prefix+"/widgets/"+id, nil)
		req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{
			UserID: "u", Authenticated: true, Scopes: []string{scope},
		})))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("scope %s: status %d, want %d", scope, rec.Code, want)
		}
	}
}

func TestRegister_RBACWriteGate(t *testing.T) {
	logs := captureLogs(t)
	core, _ := newTestCore(t)
	noContent := Handle(func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil }, "Unable to answer")
	muxFor := func(miniIDPMode bool) *http.ServeMux {
		mux := http.NewServeMux()
		register(mux, core, miniIDPMode, []Route{
			{Method: http.MethodPut, Path: "/admin", Gate: GateRBACWrite, AuditResource: entplatform.AuditResourceRole, Handler: noContent},
		})
		return mux
	}
	muxes := map[bool]*http.ServeMux{false: muxFor(false), true: muxFor(true)}
	user := func(scopes ...string) *auth.Subject {
		return auth.NewTestSubject(auth.SubjectFields{UserID: "u", Authenticated: true, Scopes: scopes})
	}

	for name, tc := range map[string]struct {
		miniIDPMode bool
		identity    *auth.Subject
		body        string
		want        int
		wantMessage string
	}{
		"rbac write with *":         {false, user("*"), "{}", http.StatusNoContent, ""},
		"rbac write with *:*:*":     {false, user("*:*:*"), "{}", http.StatusNoContent, ""},
		"rbac write with rbac read": {false, user(rbac.ScopeRBACRead), "{}", http.StatusForbidden, superAdminRequired},
		"rbac write with rbac:*:*":  {false, user("rbac:*:*"), "{}", http.StatusForbidden, superAdminRequired},
		"rbac write anonymous":      {false, nil, "{}", http.StatusUnauthorized, ""},
		"mini IdP non-admin":        {true, user(rbac.ScopeRBACRead), "{}", http.StatusForbidden, superAdminRequired},
		"mini IdP admin":            {true, user("*"), "{}", http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled},
		"mini IdP admin bad body":   {true, user("*"), "{not json", http.StatusForbidden, entplatform.MsgMiniIDPWriteDisabled},
	} {
		req := httptest.NewRequest(http.MethodPut, platpkg.APIV2Prefix+"/admin", strings.NewReader(tc.body))
		if tc.identity != nil {
			req = req.WithContext(auth.ContextWithSubject(req.Context(), tc.identity))
		}
		rec := httptest.NewRecorder()
		muxes[tc.miniIDPMode].ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d", name, rec.Code, tc.want)
		}
		if tc.wantMessage != "" && !strings.Contains(rec.Body.String(), tc.wantMessage) {
			t.Errorf("%s: body %s, want %q", name, rec.Body.String(), tc.wantMessage)
		}
	}

	if !loggedRBACRefusal(logs.String(), entplatform.AuditResourceRole) {
		t.Errorf("no rbac audit refusal for %s logged under logger_name=platform:\n%s", entplatform.AuditResourceRole, logs.String())
	}
}

const superAdminRequired = "RBAC management requires a super administrator"

func loggedRBACRefusal(logs, resource string) bool {
	for line := range strings.Lines(logs) {
		var entry struct {
			Msg        string `json:"msg"`
			LoggerName string `json:"logger_name"`
			Event      string `json:"event"`
			Action     string `json:"action"`
			Resource   string `json:"resource"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Msg == "rbac audit" && entry.LoggerName == "platform" &&
			entry.Event == "rbac_mutation" && entry.Action == "rejected" && entry.Resource == resource {
			return true
		}
	}
	return false
}

func TestServer_RemovedV1AnswersGoneAndRetainedV1StillServes(t *testing.T) {
	srv := newTestServer(t)
	for path, want := range map[string]int{
		"/api/v1/platform/agents":          http.StatusGone,
		"/api/v1/platform/budgets":         http.StatusGone,
		"/api/v1/platform/usage/summary":   http.StatusGone,
		"/api/v1/platform/health":          http.StatusOK,
		platpkg.APIV2Prefix + "/health":    http.StatusOK,
		platpkg.APIV2Prefix + "/notARoute": http.StatusNotFound,
	} {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
	}
}

func TestRegister_MaxBodyBytesCapsTheBody(t *testing.T) {
	mux := http.NewServeMux()
	register(mux, nil, false, []Route{{
		Method: http.MethodPut, Path: "/notes", Gate: GatePublic, MaxBodyBytes: 16,
		Handler: Handle(func(context.Context, noteRequest) (struct{}, error) { return struct{}{}, nil }, "Unable to save note"),
	}})
	put := func(body string) int {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, platpkg.APIV2Prefix+"/notes", strings.NewReader(body)))
		return rec.Code
	}

	if got := put(`{"text":"` + strings.Repeat("x", 8) + `"}`); got != http.StatusRequestEntityTooLarge {
		t.Errorf("17-byte body: status %d, want 413", got)
	}
	if got := put(`{"text":"a"}`); got == http.StatusRequestEntityTooLarge {
		t.Errorf("under-cap body answered 413")
	}
}

func gateRequest(mux *http.ServeMux, scopes ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, platpkg.APIV2Prefix+"/gated", nil)
	if scopes != nil {
		req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: "u", Authenticated: true, Scopes: scopes})))
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func gatedMux(core *platpkg.PlatformCore, gate Gate) *http.ServeMux {
	mux := http.NewServeMux()
	register(mux, core, false, []Route{{
		Method: http.MethodGet, Path: "/gated", Gate: gate,
		Handler: Handle(func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil }, "Unable to answer"),
	}})
	return mux
}

func TestRegister_BudgetAssignGateNeedsBoth(t *testing.T) {
	mux := gatedMux(nil, GateBudgetAssign)
	for name, tc := range map[string]struct {
		scopes []string
		want   int
	}{
		"assign only": {[]string{rbac.ScopeBudgetAssign}, http.StatusForbidden},
		"read only":   {[]string{rbac.ScopeBudgetRead}, http.StatusForbidden},
		"both":        {[]string{rbac.ScopeBudgetAssign, rbac.ScopeBudgetRead}, http.StatusNoContent},
		"none":        {nil, http.StatusUnauthorized},
	} {
		if rec := gateRequest(mux, tc.scopes...); rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d", name, rec.Code, tc.want)
		}
	}
}

func TestRegister_BudgetDeleteGateNeedsBoth(t *testing.T) {
	mux := gatedMux(nil, GateBudgetDelete)
	for name, tc := range map[string]struct {
		scopes []string
		want   int
	}{
		"delete only": {[]string{rbac.ScopeBudgetDelete}, http.StatusForbidden},
		"assign only": {[]string{rbac.ScopeBudgetAssign}, http.StatusForbidden},
		"both":        {[]string{rbac.ScopeBudgetDelete, rbac.ScopeBudgetAssign}, http.StatusNoContent},
		"none":        {nil, http.StatusUnauthorized},
	} {
		if rec := gateRequest(mux, tc.scopes...); rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d", name, rec.Code, tc.want)
		}
	}
}

func TestRegister_LinkAgentRotateTokenGateNeedsBoth(t *testing.T) {
	mux := gatedMux(nil, GateLinkAgentRotateToken)
	for name, tc := range map[string]struct {
		scopes []string
		want   int
	}{
		"deploy only": {[]string{rbac.ScopeAgentBuilderDeploy}, http.StatusForbidden},
		"update only": {[]string{rbac.ScopeAgentBuilderUpdate}, http.StatusForbidden},
		"both":        {[]string{rbac.ScopeAgentBuilderDeploy, rbac.ScopeAgentBuilderUpdate}, http.StatusNoContent},
		"none":        {nil, http.StatusUnauthorized},
	} {
		if rec := gateRequest(mux, tc.scopes...); rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d", name, rec.Code, tc.want)
		}
	}
}

func TestRegister_UsageReadGateIsStrict(t *testing.T) {
	logs := captureLogs(t)
	core, _ := newTestCore(t)
	mux := gatedMux(core, GateUsageRead)
	for name, tc := range map[string]struct {
		scope string
		want  int
	}{
		"exact read_usage": {rbac.ScopeReadUsage, http.StatusNoContent},
		"universal grant":  {"*:*:*", http.StatusNoContent},
		"partial glob":     {"a*:*:read_usage", http.StatusForbidden},
	} {
		if rec := gateRequest(mux, tc.scope); rec.Code != tc.want {
			t.Errorf("%s: status %d, want %d", name, rec.Code, tc.want)
		}
	}
	if !strings.Contains(logs.String(), "spend read refused") {
		t.Errorf("no refusal logged:\n%s", logs.String())
	}
}

type noteRequest struct {
	Text string `json:"text"`
}
