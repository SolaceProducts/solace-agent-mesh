// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/httpsse/chatv2"
)

const identityPackage = "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"

func TestChatV2RegistrationsMatchTheRouteTable(t *testing.T) {
	want := map[string]RouteRegistration{}
	for _, rt := range chatv2.Routes(chatv2.Deps{}) {
		reg := chatV2Registration(rt)
		want[reg.Method+" "+reg.Path] = reg
	}
	got := map[string]RouteRegistration{}
	for _, r := range newCoverageAdapter(t).Routes() {
		if strings.HasPrefix(r.Path, chatv2.Prefix+"/") {
			got[r.Method+" "+r.Path] = r
		}
	}
	if len(got) != len(want) {
		t.Fatalf("registry holds %d chat routes, the table has %d", len(got), len(want))
	}
	for key, reg := range want {
		if got[key] != reg {
			t.Errorf("%s: registry has %+v, the table maps to %+v", key, got[key], reg)
		}
		if reg.IsPublic == publicReasonNoUserData && reg.Scope != "" {
			t.Errorf("%s: a NoUserData route must not carry a scope", key)
		}
	}
}

func TestChatV2RoutesResolveNoIdentity(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("chatv2", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no chatv2 sources found: %v", err)
	}
	fset := token.NewFileSet()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == identityPackage {
				t.Errorf("%s imports %s, so its NoUserData routes could read the caller; give such a route a per-caller marker and a walk that checks it", path, p)
			}
		}
	}
}

func TestChatV2ConfigIsServedUnderItsPrefix(t *testing.T) {
	mux := newCoverageAdapter(t).Mux()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, chatv2.Prefix+"/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"peopleSearchAvailable"`) {
		t.Errorf("body %s lacks peopleSearchAvailable", rec.Body)
	}
}

func TestChatV2ConfigRegistersAsSignedInWithNoScope(t *testing.T) {
	var got RouteRegistration
	for _, r := range newCoverageAdapter(t).Routes() {
		if r.Method == http.MethodGet && r.Path == chatv2.Prefix+"/config" {
			got = r
		}
	}
	if got.IsPublic != publicReasonNoUserData || got.Scope != "" {
		t.Errorf("GET %s/config registers %+v, want publicReasonNoUserData and no scope", chatv2.Prefix, got)
	}
}

func TestChatV2GuardEnforcesTheScopeOfAScopedRoute(t *testing.T) {
	const scope = "agent:_:read"
	rt := apicontract.Route{Method: http.MethodGet, Path: "/scoped", Scope: scope}
	served := false
	h := chatV2Guard(rt, func(http.ResponseWriter, *http.Request) { served = true })

	denied := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, chatv2.Prefix+"/scoped", nil)
	h(denied, req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: "u"}))))
	if denied.Code != http.StatusForbidden || served {
		t.Fatalf("caller without the scope got %d, served=%v; want 403 and not served", denied.Code, served)
	}

	allowed := httptest.NewRecorder()
	h(allowed, req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{UserID: "u", Scopes: []string{scope}}))))
	if allowed.Code != http.StatusOK || !served {
		t.Fatalf("caller with the scope got %d, served=%v; want it served", allowed.Code, served)
	}
	if got := chatV2Registration(rt); got.Scope != scope || got.IsPublic != "" {
		t.Errorf("registration = %+v, want scope %q and no marker", got, scope)
	}
}

func TestChatV2RefusesARouteWithNoAuthorization(t *testing.T) {
	for name, rt := range map[string]apicontract.Route{
		"no gate and no scope": {Method: http.MethodGet, Path: "/open"},
		"unknown gate":         {Method: http.MethodGet, Path: "/open", Gate: "mystery"},
		"only AnyOfScopes":     {Method: http.MethodGet, Path: "/open", AnyOfScopes: []string{"agent:_:read", "agent:_:create"}},
	} {
		t.Run(name, func(t *testing.T) {
			assertPanics(t, func() { chatV2Registration(rt) })
			assertPanics(t, func() { chatV2Guard(rt, func(http.ResponseWriter, *http.Request) {}) })
		})
	}
}

func assertPanics(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error("expected a panic")
		}
	}()
	fn()
}
