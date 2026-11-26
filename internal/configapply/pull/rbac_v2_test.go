// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func TestPullYAMLPreview_NamesRolesOnV2(t *testing.T) {
	const (
		operatorID = "0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e40"
		viewerID   = "0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e41"
	)
	page := func(rows string) string {
		return `{"data":[` + rows + `],"meta":{"pagination":{"pageNumber":1,"pageSize":100,"count":1,"totalPages":1}}}`
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/agents", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[],"meta":{"pagination":{"pageNumber":1,"pageSize":100,"count":0,"totalPages":1}}}`))
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(page(
			`{"id":"` + operatorID + `","type":"role","name":"yaml_operator","origin":"yaml","scopes":[],"inherits":[]},` +
				`{"id":"` + viewerID + `","type":"role","name":"yaml_viewer","origin":"yaml","scopes":[],"inherits":["` + operatorID + `"]}`)))
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/rbac/grants", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[],"meta":{"pagination":{"pageNumber":1,"pageSize":100,"count":0,"totalPages":1}}}`))
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/rbac/claimMappings", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(page(
			`{"id":"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e42","type":"claimMapping","name":"admins","oidcProvider":"azure",` +
				`"claimKey":null,"claimValue":"admins","roleIds":["` + viewerID + `"],"origin":"yaml"}`)))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := reconcile.NewClient(target.Resolved{URL: srv.URL})

	roles, _, err := pullRBACRoles(t.Context(), c, Options{IncludeYAMLPreview: true}, &Result{})
	if err != nil {
		t.Fatalf("pullRBACRoles: %v", err)
	}
	mappings, _, err := pullRBACClaimMappings(t.Context(), c, Options{IncludeYAMLPreview: true}, &Result{})
	if err != nil {
		t.Fatalf("pullRBACClaimMappings: %v", err)
	}
	var content strings.Builder
	for _, e := range append(roles, mappings...) {
		content.Write(e.content)
	}
	for _, id := range []string{operatorID, viewerID} {
		if strings.Contains(content.String(), id) {
			t.Errorf("YAML preview names a role by its v2 id %s, not its name:\n%s", id, content.String())
		}
	}
	for _, name := range []string{"- yaml_operator", "- yaml_viewer"} {
		if !strings.Contains(content.String(), name) {
			t.Errorf("YAML preview lacks %q:\n%s", name, content.String())
		}
	}
}

func TestPullYAMLPreview_WarnsOnAnUnlistedRoleOnV2(t *testing.T) {
	const unlistedID = "0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e49"
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/agents", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[],"meta":{"pagination":{"pageNumber":1,"pageSize":100,"count":0,"totalPages":1}}}`))
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[],"meta":{"pagination":{"pageNumber":1,"pageSize":100,"count":0,"totalPages":1}}}`))
	})
	mux.HandleFunc("GET "+platform.APIV2Prefix+"/rbac/claimMappings", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"0190f5ec-6f7a-7b8c-ad0e-8a9b0c1d2e42","type":"claimMapping","name":"admins","oidcProvider":"azure",` +
			`"claimKey":null,"claimValue":"admins","roleIds":["` + unlistedID + `"],"origin":"yaml"}],` +
			`"meta":{"pagination":{"pageNumber":1,"pageSize":100,"count":1,"totalPages":1}}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := reconcile.NewClient(target.Resolved{URL: srv.URL})

	res := &Result{}
	mappings, _, err := pullRBACClaimMappings(t.Context(), c, Options{IncludeYAMLPreview: true}, res)
	if err != nil {
		t.Fatalf("pullRBACClaimMappings: %v", err)
	}
	if len(mappings) != 0 {
		t.Errorf("emitted %d preview files, want the mapping skipped", len(mappings))
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), unlistedID) {
		t.Errorf("warnings = %v, want the unlisted role id named", res.Warnings)
	}
}
