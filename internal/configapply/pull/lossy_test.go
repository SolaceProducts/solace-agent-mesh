// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/sources"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type pullFunc func(context.Context, *reconcile.Client, Options, *Result) ([]fileEntry, []string, error)

func newListFakeServer(t *testing.T, path, rows string) *reconcile.Client {
	t.Helper()
	var listed []map[string]any
	if err := json.Unmarshal([]byte("["+rows+"]"), &listed); err != nil {
		t.Fatalf("rows %s: %v", rows, err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, listed)
	})
	mux.HandleFunc("GET "+path+"/{id}", func(w http.ResponseWriter, r *http.Request) {
		i := slices.IndexFunc(listed, func(row map[string]any) bool { return row["id"] == r.PathValue("id") })
		if i < 0 {
			writeV2Error(w, http.StatusNotFound, "not found")
			return
		}
		writeV2Single(w, listed[i])
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Page(w, []struct{}{})
	})
	return newV2PlatformClient(t, mux)
}

func TestPull_UnsafeNameGetsMappedFileName(t *testing.T) {
	for _, tc := range []struct {
		kind string
		path string
		dir  string
		row  string
		pull pullFunc
	}{
		{"agent", platform.APIV2Prefix + "/agents", "agents", `{"id":"a1","name":%q}`, pullAgents},
		{"connector", platform.APIV2Prefix + "/connectors", "connectors", `{"id":"c1","name":%q,"connectorType":"mcp"}`, pullConnectors},
		{"dataset", platform.APIV2Prefix + "/evaluations/datasets", "datasets", `{"id":"d1","name":%q}`, pullDatasets},
		{"evaluator", platform.APIV2Prefix + "/evaluations/evaluators", "evaluators", `{"id":"e1","name":%q}`, pullEvaluators},
		{"experiment", platform.APIV2Prefix + "/evaluations/experiments", "experiments", `{"id":"x1","name":%q}`, pullExperiments},
		{"entrypoint", platform.APIV2Prefix + "/entrypoints", "entrypoints", `{"id":"g1","name":%q}`, pullGateways},
		{"model", platform.APIV2Prefix + "/models", "models", `{"id":"m1","alias":%q,"provider":"openai","modelName":"gpt-4o"}`, pullModels},
		{"workflow", platform.APIV2Prefix + "/workflows", "workflows", `{"id":"w1","name":%q}`, pullWorkflows},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			c := newListFakeServer(t, tc.path, fmt.Sprintf(tc.row, "Sales/EMEA"))
			res := &Result{}
			entries, _, err := tc.pull(t.Context(), c, Options{}, res)
			if err != nil {
				t.Fatalf("pull: %v", err)
			}
			if len(res.lossy) != 0 {
				t.Fatalf("lossy = %v; want none", res.lossy)
			}
			want := filepath.Join(tc.dir, "Sales_EMEA.yaml")
			i := slices.IndexFunc(entries, func(e fileEntry) bool { return e.relPath == want })
			if i < 0 {
				t.Fatalf("entries = %v; want %s", relPaths(entries), want)
			}
			if !strings.Contains(string(entries[i].content), "name: Sales/EMEA") {
				t.Errorf("%s does not keep the real name:\n%s", want, entries[i].content)
			}

			c = newListFakeServer(t, tc.path, fmt.Sprintf(tc.row, ".."))
			res = &Result{}
			entries, _, err = tc.pull(t.Context(), c, Options{}, res)
			if err != nil {
				t.Fatalf("pull: %v", err)
			}
			if len(entries) != 0 {
				t.Errorf("entries = %v; want none", relPaths(entries))
			}
			assertLossy(t, res, "not a safe file name")
		})
	}
}

func TestPull_UnsafeRBACNameGetsMappedFileName(t *testing.T) {
	rbacServer := func(roleName, origin string) *reconcile.Client {
		srv := newRBACFakeServer(t, &rbacFakeServer{
			roles: []wire.RoleResponse{{ID: "r1", Name: roleName, Origin: origin}},
			grants: []wire.GrantResponse{
				{ID: "g1", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "alice"}, RoleID: "r1", Origin: entplatform.RBACOriginCustom},
			},
		})
		return reconcile.NewClient(target.Resolved{URL: srv.URL})
	}
	for _, tc := range []struct {
		kind   string
		origin string
		dir    string
		pull   pullFunc
	}{
		{"rbacRole", entplatform.RBACOriginCustom, rbacRolesDir, pullRBACRoles},
		{"rbacGrant", entplatform.RBACOriginBuiltin, rbacGrantsDir, pullRBACGrants},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			res := &Result{}
			entries, _, err := tc.pull(t.Context(), rbacServer("Sales/EMEA", tc.origin), Options{}, res)
			if err != nil {
				t.Fatalf("pull: %v", err)
			}
			if len(res.lossy) != 0 {
				t.Fatalf("lossy = %v; want none", res.lossy)
			}
			want := filepath.Join(tc.dir, "Sales_EMEA.yaml")
			i := slices.IndexFunc(entries, func(e fileEntry) bool { return e.relPath == want })
			if i < 0 {
				t.Fatalf("entries = %v; want %s", relPaths(entries), want)
			}
			if !strings.Contains(string(entries[i].content), "name: Sales/EMEA") {
				t.Errorf("%s does not keep the real name:\n%s", want, entries[i].content)
			}

			res = &Result{}
			entries, _, err = tc.pull(t.Context(), rbacServer("..", tc.origin), Options{}, res)
			if err != nil {
				t.Fatalf("pull: %v", err)
			}
			if len(entries) != 0 {
				t.Errorf("entries = %v; want none", relPaths(entries))
			}
			assertLossy(t, res, "not a safe file name")
		})
	}
}

func TestPull_UnsafeDirectoryNameIsLossy(t *testing.T) {
	for _, tc := range []struct {
		kind string
		path string
		pull pullFunc
	}{
		{"skill", platform.APIV2Prefix + "/skills", pullSkills},
		{"toolset", platform.APIV2Prefix + "/toolsets", pullToolsets},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			c := newListFakeServer(t, tc.path, `{"id":"s1","name":"../evil"}`)
			res := &Result{}
			entries, _, err := tc.pull(t.Context(), c, Options{}, res)
			if err != nil {
				t.Fatalf("pull: %v", err)
			}
			if len(entries) != 0 {
				t.Errorf("entries = %v; want none", relPaths(entries))
			}
			assertLossy(t, res, "not a safe file name")
		})
	}
}

func TestPull_FileNameCollisionIsLossy(t *testing.T) {
	c := newListFakeServer(t, platform.APIV2Prefix+"/agents", `{"id":"a1","name":"Sales/EMEA"},{"id":"a2","name":"sales_emea"}`)
	_, res, err := fetchAndTranslate(t.Context(), c, Options{OnlyKind: "agent"})
	if err == nil {
		t.Fatal("want an error when two agents map to one file")
	}
	assertLossy(t, res, "agents/sales_emea.yaml: two resources map to this file")
}

func TestPull_ImportedAndLocalNamesMappingToOneFileFail(t *testing.T) {
	c := newListFakeServer(t, platform.APIV2Prefix+"/agents", `{"id":"a1","name":"Sales/EMEA"},{"id":"a2","name":"Sales_EMEA"}`)
	ref := &manifest.Manifest{
		Sources:   map[string]sources.Source{"lib": {Raw: "file:///lib"}},
		Resources: map[string][]manifest.ResourceEntry{"agents": {{Name: "Sales/EMEA", From: "Sales/EMEA", Source: "lib"}}},
	}
	_, res, err := fetchAndTranslate(t.Context(), c, Options{OnlyKind: "agent", ReferenceManifest: ref})
	if err == nil {
		t.Fatal("want an error when an imported and a local agent map to one file")
	}
	assertLossy(t, res, "agents/Sales_EMEA.yaml: two resources map to this file")
}

func TestPullRBACClaimMappings_CaseVariantsGetTheirOwnFiles(t *testing.T) {
	srv := &rbacFakeServer{
		roles: []wire.RoleResponse{{ID: "r-ok", Name: "ok", Origin: entplatform.RBACOriginCustom}},
		claimMaps: []wire.ClaimMappingResponse{
			{ID: "m1", OIDCProvider: new("azure"), ClaimValue: "Admins", RoleIDs: []string{"r-ok"}, Origin: entplatform.RBACOriginCustom},
			{ID: "m2", OIDCProvider: new("azure"), ClaimValue: "admins", RoleIDs: []string{"r-ok"}, Origin: entplatform.RBACOriginCustom},
		},
	}
	c := reconcile.NewClient(target.Resolved{URL: newRBACFakeServer(t, srv).URL})
	res := &Result{}
	entries, names, err := pullRBACClaimMappings(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	claimFiles(entries, map[string]struct{}{}, res)
	if len(names) != 2 || len(res.lossy) != 0 {
		t.Fatalf("names = %v, files = %v, lossy = %v; want two mappings with distinct files", names, relPaths(entries), res.lossy)
	}
}

func TestClaimFiles_PreviewClashWarnsElseFails(t *testing.T) {
	preview := filepath.Join("rbac", "roles", rbacYAMLPreviewSubdir)
	res := &Result{}
	out := claimFiles([]fileEntry{
		{relPath: filepath.Join(preview, "Viewer.yaml")},
		{relPath: filepath.Join(preview, "viewer.yaml")},
		{relPath: filepath.Join("agents", "A.yaml")},
		{relPath: filepath.Join("agents", "a.yaml")},
	}, map[string]struct{}{}, res)
	if len(out) != 2 {
		t.Errorf("kept %v; want the first of each clashing pair", relPaths(out))
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "viewer.yaml") {
		t.Errorf("warnings = %v; want the preview clash", res.Warnings)
	}
	if len(res.lossy) != 1 {
		t.Errorf("lossy = %v; want only the agent clash", res.lossy)
	}
}

func TestPullRBAC_UnreproducibleRowsWarnInPreviewElseFail(t *testing.T) {
	srv := &rbacFakeServer{
		roles: []wire.RoleResponse{
			{ID: "r-db", Name: "..", Origin: entplatform.RBACOriginCustom},
			{ID: "...", Name: "...", Origin: entplatform.RBACOriginYAML},
			{ID: "r-builtin", Name: "....", Origin: entplatform.RBACOriginBuiltin},
			{ID: "r-ok", Name: "ok", Origin: entplatform.RBACOriginCustom},
		},
		grants: []wire.GrantResponse{
			{ID: "g1", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "alice"}, RoleID: "r-builtin", Origin: entplatform.RBACOriginCustom},
		},
		claimMaps: []wire.ClaimMappingResponse{
			{ID: "m1", OIDCProvider: new("azure"), ClaimValue: "admins", RoleIDs: []string{"missing-role"}, Origin: entplatform.RBACOriginCustom},
		},
		systemUsers: []wire.SystemUserResponse{
			{ID: "su-1", Name: ".....", Sub: "system:bot", RoleIDs: []string{"r-ok"}, Origin: entplatform.RBACOriginCustom},
		},
	}
	c := reconcile.NewClient(target.Resolved{URL: newRBACFakeServer(t, srv).URL})
	opts := Options{IncludeYAMLPreview: true}

	res := &Result{}
	for _, pull := range []pullFunc{pullRBACRoles, pullRBACGrants, pullRBACClaimMappings, pullServiceAccounts} {
		if _, _, err := pull(t.Context(), c, opts, res); err != nil {
			t.Fatalf("pull: %v", err)
		}
	}

	assertLossy(t, res, `rbacRole "..": name is not a safe file name`)
	assertLossy(t, res, `rbacGrant: role name "...." is not a safe file name`)
	assertLossy(t, res, `rbacClaimMapping azure:admins: roleId "missing-role"`)
	assertLossy(t, res, `serviceAccount ".....": name is not a safe file name`)
	if !slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, `"..."`) }) {
		t.Errorf("warnings = %v; want the YAML preview role skipped with a warning", res.Warnings)
	}
	for _, msg := range res.lossy {
		if strings.Contains(msg, `"..."`) {
			t.Errorf("YAML preview row recorded as lossy: %q", msg)
		}
	}
}
