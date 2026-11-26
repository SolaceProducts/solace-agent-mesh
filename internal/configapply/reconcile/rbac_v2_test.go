// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func newV2TestClient(t *testing.T, mux *http.ServeMux) *Client {
	t.Helper()
	mux.HandleFunc("GET "+APIV2ProbePath, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[],"meta":{"pagination":{"pageNumber":1,"pageSize":100,"count":0,"totalPages":1}}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return NewClient(target.Resolved{URL: srv.URL, Token: "tok"})
}

func TestUpdateRBACGrant_PatchesOnlyTheSubjectOnV2(t *testing.T) {
	var body, id string
	mux := http.NewServeMux()
	mux.HandleFunc("PATCH "+platform.APIV2Prefix+"/rbac/grants/{id}", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body, id = string(raw), r.PathValue("id")
		writeV2Single(w, http.StatusOK, wire.GrantResponse{ID: id})
	})
	c := newV2TestClient(t, mux)

	if err := c.UpdateRBACGrant(t.Context(), "grant-1", apiv2.CreateGrantSubject{Type: "email", Value: "jane@example.com"}); err != nil {
		t.Fatalf("UpdateRBACGrant: %v", err)
	}
	if id != "grant-1" {
		t.Errorf("PATCH grant id = %q, want grant-1", id)
	}
	if want := `{"subject":{"type":"email","value":"jane@example.com","issuer":""}}`; body != want {
		t.Errorf("PATCH body = %s, want %s", body, want)
	}
}

func TestListRBACGrantsWithHolders_ReadsTheUsersOnV2(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+rbacGrantsResource.apiPath(), func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("expand") != "user" {
			t.Errorf("grant list query = %q, want expand=user", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"data":[` +
			`{"id":"g1","roleId":"r1","state":"concrete","origin":"custom","subject":{"type":"email","value":"jane@example.com"},` +
			`"user":{"email":"jane@example.com","emailShared":false,"sub":"jane-sub","issuer":"https://idp"}},` +
			`{"id":"g2","roleId":"r1","state":"pending","origin":"custom","subject":{"type":"email","value":"new@example.com"},"user":null}],` +
			`"meta":{"pagination":{"pageNumber":1,"pageSize":100,"count":2,"totalPages":1}}}`))
	})
	c := newV2TestClient(t, mux)

	grants, holders, err := c.listRBACGrantsWithHolders(t.Context())
	if err != nil {
		t.Fatalf("listRBACGrantsWithHolders: %v", err)
	}
	if len(grants) != 2 || ptrString(grants[0].State) != "concrete" {
		t.Fatalf("grants = %+v", grants)
	}
	if u := holders["g1"]; u == nil || u.Sub != "jane-sub" {
		t.Errorf("holder of g1 = %+v, want jane-sub", u)
	}
	if u, ok := holders["g2"]; ok {
		t.Errorf("pending g2 has holder %+v, want none", u)
	}
}

func TestListRBACGrantsWithHolders_RefusesAPlatformWithoutExpandUser(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+rbacGrantsResource.apiPath(), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"g1","roleId":"r1","state":"concrete","origin":"custom"}],` +
			`"meta":{"pagination":{"pageNumber":1,"pageSize":100,"count":1,"totalPages":1}}}`))
	})
	c := newV2TestClient(t, mux)

	_, _, err := c.listRBACGrantsWithHolders(t.Context())
	if err == nil || !strings.Contains(err.Error(), "predates GET /api/v2/platform/rbac/grants?expand=user") || !strings.Contains(err.Error(), upgradeToThisCLIRelease) {
		t.Fatalf("err = %v, want the refusal naming the upgrade", err)
	}
}

func TestUserCapability_ReadsTheServerCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		key       string
		wantAllow bool
	}{
		{"refuses rbac writes", `{"data":{"manageRbac":false,"createDecisionModels":true}}`, userCapabilityManageRbac, false},
		{"allows decision models", `{"data":{"manageRbac":false,"createDecisionModels":true}}`, userCapabilityCreateDecisionModels, true},
		{"leaves the key out", `{"data":{"manageRbac":false}}`, userCapabilityCreateDecisionModels, true},
		{"without the route", "", userCapabilityManageRbac, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("GET "+serverCapabilitiesPath, func(w http.ResponseWriter, r *http.Request) {
				if tc.body == "" {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(tc.body))
			})
			allowed, err := userCapability(t.Context(), newV2TestClient(t, mux), tc.key)
			if err != nil || allowed != tc.wantAllow {
				t.Errorf("userCapability(%s) = %t, %v; want %t", tc.key, allowed, err, tc.wantAllow)
			}
		})
	}
}

func TestApplyProfileProvider_KeepsTheStoredLookupKey(t *testing.T) {
	var body string
	mux := http.NewServeMux()
	mux.HandleFunc("PUT "+platform.APIV2Prefix+profileProviderResource.path, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.WriteHeader(http.StatusNoContent)
	})
	c := newV2TestClient(t, mux)
	toolsetID := "0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"
	plan := &profileProviderPlan{
		Verb: profileProviderVerbPut, ToolsetID: toolsetID,
		CurrentState: &wire.ProfileProviderResponse{LookupKey: "upn"},
	}
	out := applyProfileProvider(t.Context(), c, plan, ApplyOptions{})
	if out.Applied == nil {
		t.Fatalf("apply = %+v", out)
	}
	if want := `{"toolsetId":"` + toolsetID + `","lookupKey":"upn"}`; body != want {
		t.Errorf("PUT body = %s, want %s", body, want)
	}
}

func TestApplyProfileProvider_SendsTheLookupKeyStoredWhenThePUTRuns(t *testing.T) {
	toolsetID := "0190f5ea-2b3c-7d4e-9f5a-6b7c8d9e0f1a"
	var body string
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+profileProviderResource.path, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"toolsetId":"` + toolsetID + `","lookupKey":"mail"}}`))
	})
	mux.HandleFunc("PUT "+platform.APIV2Prefix+profileProviderResource.path, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		w.WriteHeader(http.StatusNoContent)
	})
	c := newV2TestClient(t, mux)
	plan := &profileProviderPlan{
		Verb: profileProviderVerbPut, ToolsetID: toolsetID,
		CurrentState: &wire.ProfileProviderResponse{LookupKey: "upn"},
	}
	if out := applyProfileProvider(t.Context(), c, plan, ApplyOptions{}); out.Applied == nil {
		t.Fatalf("apply = %+v", out)
	}
	if want := `{"toolsetId":"` + toolsetID + `","lookupKey":"mail"}`; body != want {
		t.Errorf("PUT body = %s, want %s", body, want)
	}
}

func TestRBACGrantDeletePath_NamesTheV2Route(t *testing.T) {
	if got, want := RBACGrantDeletePath(), platform.APIV2Prefix+"/rbac/grants/{id}"; got != want {
		t.Errorf("path = %s, want %s", got, want)
	}
}

func TestPlanDefaultRoles_YAMLFallbackMatchesItsNames(t *testing.T) {
	const viewerID = "0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40"
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/rbac/defaultRoles", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"roleIds":["` + viewerID + `"],"origin":"yaml"}}`))
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"` + viewerID + `","type":"role","name":"viewer","origin":"yaml","scopes":[],"inherits":[]}],` +
			`"meta":{"pagination":{"pageNumber":1,"pageSize":100,"count":1,"totalPages":1}}}`))
	})
	c := newV2TestClient(t, mux)

	plan, err := planDefaultRoles(t.Context(), c, &manifest.DefaultRolesSpec{Roles: []string{"viewer"}})
	if err != nil {
		t.Fatalf("planDefaultRoles: %v", err)
	}
	if plan.Verb != defaultRolesVerbUnchanged || !slices.Equal(plan.CurrentNames, []string{"viewer"}) {
		t.Errorf("plan = %s with current %v, want unchanged with current [viewer]", plan.Verb, plan.CurrentNames)
	}
}
