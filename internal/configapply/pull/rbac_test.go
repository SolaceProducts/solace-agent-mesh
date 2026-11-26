// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

type rbacFakeServer struct {
	roles       []wire.RoleResponse
	grants      []wire.GrantResponse
	claimMaps   []wire.ClaimMappingResponse
	systemUsers []wire.SystemUserResponse
	rolesHits   int
}

func yamlRoleID(name string) string {
	return entplatform.YAMLRowID(entplatform.YAMLKindRole, name)
}

func newRBACFakeServer(t *testing.T, srv *rbacFakeServer) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for _, prefix := range []string{"/api/v1/platform", platform.APIV2Prefix} {
		mux.HandleFunc("GET "+prefix+"/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
			srv.rolesHits++
			writeV2Page(w, srv.roles)
		})
		mux.HandleFunc("GET "+prefix+"/rbac/grants", func(w http.ResponseWriter, _ *http.Request) {
			writeV2Page(w, srv.grants)
		})
		mux.HandleFunc("GET "+prefix+"/rbac/claimMappings", func(w http.ResponseWriter, _ *http.Request) {
			writeV2Page(w, srv.claimMaps)
		})
		mux.HandleFunc("GET "+prefix+"/rbac/serviceAccounts", func(w http.ResponseWriter, _ *http.Request) {
			writeV2Page(w, srv.systemUsers)
		})
		mux.HandleFunc("GET "+prefix+"/rbac/systemUsers", func(w http.ResponseWriter, _ *http.Request) {
			writeV2Page(w, srv.systemUsers)
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if v2Singletons[r.URL.Path] {
			writeV2Single(w, struct{}{})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"meta":{"pagination":{"pageNumber":1,"pageSize":100,"count":0,"totalPages":1}}}`))
	})
	return newV2Platform(t, mux)
}

var v2Singletons = map[string]bool{
	platform.APIV2Prefix + "/profileProvider":    true,
	platform.APIV2Prefix + "/webuiSettings":      true,
	platform.APIV2Prefix + "/rbac/defaultRoles":  true,
	platform.APIV2Prefix + "/serverCapabilities": true,
}

func fixtureRolesMixed() []wire.RoleResponse {
	return []wire.RoleResponse{
		{
			ID:     "role-uuid-1",
			Name:   "sam_admin",
			Scopes: []string{"rbac:_:*", "agent:*:*"},
			Origin: entplatform.RBACOriginCustom,
		},
		{
			ID:     yamlRoleID("yaml_viewer"),
			Name:   "yaml_viewer",
			Scopes: []string{"agent:*:read"},
			Origin: entplatform.RBACOriginYAML,
		},
	}
}

func TestPullRBACRoles_DefaultEmitsDBOnly(t *testing.T) {
	srv := &rbacFakeServer{roles: fixtureRolesMixed()}
	httpsrv := newRBACFakeServer(t, srv)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullRBACRoles(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullRBACRoles: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (DB only), got %d: %+v", len(entries), relPaths(entries))
	}
	if names[0] != "sam_admin" {
		t.Errorf("manifest names = %v; want [sam_admin]", names)
	}
	if !strings.HasSuffix(entries[0].relPath, "rbac/roles/sam_admin.yaml") {
		t.Errorf("rel path = %q; want rbac/roles/sam_admin.yaml", entries[0].relPath)
	}
	if strings.Contains(string(entries[0].content), "yaml_viewer") {
		t.Errorf("default pull leaked YAML row into spec output:\n%s", entries[0].content)
	}
}

func TestPullRBACRoles_SkipsBuiltinRolesEvenWithYAMLPreview(t *testing.T) {
	roles := append(fixtureRolesMixed(), wire.RoleResponse{
		ID:     "builtin-uuid-1",
		Name:   "sam_manager",
		Scopes: []string{"agent:*:*"},
		Origin: entplatform.RBACOriginBuiltin,
	})
	srv := &rbacFakeServer{roles: roles}
	httpsrv := newRBACFakeServer(t, srv)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullRBACRoles(t.Context(), c, Options{IncludeYAMLPreview: true}, res)
	if err != nil {
		t.Fatalf("pullRBACRoles: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.relPath, "sam_manager") || strings.Contains(string(e.content), "sam_manager") {
			t.Errorf("pull emitted built-in role sam_manager as a spec file (%q):\n%s", e.relPath, e.content)
		}
	}
	for _, n := range names {
		if n == "sam_manager" {
			t.Errorf("manifest names include built-in role sam_manager: %v", names)
		}
	}
}

func TestPullRBACRoles_IncludeYAMLPreviewEmitsBoth(t *testing.T) {
	srv := &rbacFakeServer{roles: fixtureRolesMixed()}
	httpsrv := newRBACFakeServer(t, srv)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullRBACRoles(t.Context(), c, Options{IncludeYAMLPreview: true}, res)
	if err != nil {
		t.Fatalf("pullRBACRoles: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (DB + preview), got %d: %+v", len(entries), relPaths(entries))
	}
	if len(names) != 1 || names[0] != "sam_admin" {
		t.Errorf("manifest names = %v; want [sam_admin] only (YAML preview rows are not in the manifest)", names)
	}

	var preview *fileEntry
	for i := range entries {
		if strings.Contains(entries[i].relPath, "_yaml-preview/") {
			preview = &entries[i]
		}
	}
	if preview == nil {
		t.Fatalf("missing preview file: %v", relPaths(entries))
	}
	wantSuffix := filepath.Join("rbac", "roles", "_yaml-preview", "yaml_viewer.yaml")
	if !strings.HasSuffix(preview.relPath, wantSuffix) {
		t.Errorf("preview path = %q; want suffix %q", preview.relPath, wantSuffix)
	}
	content := string(preview.content)
	if !strings.Contains(content, "operator-owned") {
		t.Errorf("preview missing banner:\n%s", content)
	}
	assertAllCommented(t, preview.content)
}

func TestPullRBACRoles_FoldsUsersFromDBAssignments(t *testing.T) {
	srv := &rbacFakeServer{
		roles: []wire.RoleResponse{
			{ID: "role-admin", Name: "sam_admin", Scopes: []string{"rbac:_:*"}, Origin: entplatform.RBACOriginCustom},
			{ID: "role-builder", Name: "sam_builder", Scopes: []string{"agent_builder:*:*"}, Origin: entplatform.RBACOriginCustom},
		},
		grants: []wire.GrantResponse{
			{ID: "a1", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "zoe@example.com"}, RoleID: "role-builder", Origin: entplatform.RBACOriginCustom},
			{ID: "a2", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "trent@example.com"}, RoleID: "role-builder", Origin: entplatform.RBACOriginCustom},
			{ID: "a3", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "trent@example.com"}, RoleID: "role-admin", Origin: entplatform.RBACOriginCustom},
			{ID: "y1", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "yaml-bob@example.com"}, RoleID: yamlRoleID("yaml_role"), Origin: entplatform.RBACOriginYAML},
		},
	}
	httpsrv := newRBACFakeServer(t, srv)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullRBACRoles(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullRBACRoles: %v", err)
	}
	body := func(role string) string {
		for i := range entries {
			if strings.HasSuffix(entries[i].relPath, "rbac/roles/"+role+".yaml") {
				return string(entries[i].content)
			}
		}
		t.Fatalf("missing role file for %q: %v", role, relPaths(entries))
		return ""
	}

	builder := body("sam_builder")
	trentIdx := strings.Index(builder, "trent@example.com")
	zoeIdx := strings.Index(builder, "zoe@example.com")
	if trentIdx < 0 || zoeIdx < 0 || trentIdx > zoeIdx {
		t.Errorf("sam_builder users not present and sorted:\n%s", builder)
	}
	if !strings.Contains(builder, "users:") {
		t.Errorf("sam_builder missing users: block:\n%s", builder)
	}

	admin := body("sam_admin")
	if !strings.Contains(admin, "trent@example.com") {
		t.Errorf("multi-role identity trent did not land in sam_admin:\n%s", admin)
	}
	if strings.Contains(admin, "yaml-bob@example.com") {
		t.Errorf("YAML-origin assignment folded into users:\n%s", admin)
	}
	for _, e := range entries {
		if strings.Contains(e.relPath, "rbac/assignments/") {
			t.Errorf("pull wrote a rbac/assignments file; that path is removed: %s", e.relPath)
		}
	}
}

func assertAllCommented(t *testing.T, content []byte) {
	t.Helper()
	for line := range strings.SplitSeq(string(content), "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			t.Errorf("preview content not fully commented; offending line %q in:\n%s", line, content)
			return
		}
	}
}

func TestPullRBACClaimMappings_DefaultEmitsDBOnly(t *testing.T) {
	srv := &rbacFakeServer{
		roles: []wire.RoleResponse{
			{ID: "role-uuid-1", Name: "sam_admin", Origin: entplatform.RBACOriginCustom},
		},
		claimMaps: []wire.ClaimMappingResponse{
			{ID: "m1", OIDCProvider: new("azure"), ClaimValue: "sam-admins", RoleIDs: []string{"role-uuid-1"}, Origin: entplatform.RBACOriginCustom},
			{ID: "y1", OIDCProvider: new("okta"), ClaimValue: "viewers", RoleIDs: []string{yamlRoleID("yaml_viewer")}, Origin: entplatform.RBACOriginYAML},
		},
	}
	httpsrv := newRBACFakeServer(t, srv)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullRBACClaimMappings(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullRBACClaimMappings: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 DB entry, got %d: %+v", len(entries), relPaths(entries))
	}
	if len(names) != 1 {
		t.Errorf("manifest names = %v; want one DB entry", names)
	}
	body := string(entries[0].content)
	for _, want := range []string{"oidcProvider: azure", "claimValue: sam-admins", "roleNames:", "- sam_admin"} {
		if !strings.Contains(body, want) {
			t.Errorf("pulled claim mapping missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "role-uuid-1") {
		t.Errorf("UUID leaked into claim-mapping spec:\n%s", body)
	}
}

func TestPullRBACClaimMappings_ResolvesBuiltinRole(t *testing.T) {
	srv := &rbacFakeServer{
		roles: []wire.RoleResponse{
			{ID: "builtin-1", Name: "sam_manager", Origin: entplatform.RBACOriginBuiltin},
		},
		claimMaps: []wire.ClaimMappingResponse{
			{ID: "m1", OIDCProvider: new("azure"), ClaimValue: "sam-managers", RoleIDs: []string{"builtin-1"}, Origin: entplatform.RBACOriginCustom},
		},
	}
	httpsrv := newRBACFakeServer(t, srv)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullRBACClaimMappings(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullRBACClaimMappings: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("built-in-targeting mapping was dropped: got %d entries %+v", len(entries), relPaths(entries))
	}
	body := string(entries[0].content)
	if !strings.Contains(body, "- sam_manager") {
		t.Errorf("mapping did not resolve built-in roleId → name:\n%s", body)
	}
	if strings.Contains(body, "builtin-1") {
		t.Errorf("raw built-in UUID leaked into claim-mapping spec:\n%s", body)
	}
}

func TestPullRBACClaimMappings_YAMLPreviewSkipsUnknownRoleID(t *testing.T) {
	srv := &rbacFakeServer{
		roles: []wire.RoleResponse{
			{ID: yamlRoleID("yaml_viewer"), Name: "yaml_viewer", Origin: entplatform.RBACOriginYAML},
		},
		claimMaps: []wire.ClaimMappingResponse{
			{ID: "y1", OIDCProvider: new("okta"), ClaimValue: "viewers", RoleIDs: []string{yamlRoleID("yaml_viewer")}, Origin: entplatform.RBACOriginYAML},
			{ID: "y2", OIDCProvider: new("okta"), ClaimValue: "ghosts", RoleIDs: []string{yamlRoleID("yaml_missing")}, Origin: entplatform.RBACOriginYAML},
		},
	}
	httpsrv := newRBACFakeServer(t, srv)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullRBACClaimMappings(t.Context(), c, Options{IncludeYAMLPreview: true}, res)
	if err != nil {
		t.Fatalf("pullRBACClaimMappings: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want only the viewers mapping", relPaths(entries))
	}
	if body := string(entries[0].content); !strings.Contains(body, "- yaml_viewer") {
		t.Errorf("viewers mapping did not resolve its YAML role id to the name:\n%s", body)
	}
	if !slices.ContainsFunc(res.Warnings, func(w string) bool {
		return strings.Contains(w, "okta:ghosts") && strings.Contains(w, "names no role")
	}) {
		t.Errorf("warnings = %q, want one naming okta:ghosts and its unknown role", res.Warnings)
	}
}

func TestPullRBACRoles_InheritsFromBuiltinResolvesToName(t *testing.T) {
	srv := &rbacFakeServer{
		roles: []wire.RoleResponse{
			{ID: "builtin-1", Name: "sam_manager", Origin: entplatform.RBACOriginBuiltin},
			{ID: "child-uuid", Name: "regional_manager", Inherits: []string{"builtin-1"}, Origin: entplatform.RBACOriginCustom},
		},
	}
	httpsrv := newRBACFakeServer(t, srv)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullRBACRoles(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullRBACRoles: %v", err)
	}
	var child *fileEntry
	for i := range entries {
		if strings.Contains(entries[i].relPath, "regional_manager") {
			child = &entries[i]
		}
	}
	if child == nil {
		t.Fatalf("missing child role file: %+v", relPaths(entries))
	}
	body := string(child.content)
	if !strings.Contains(body, "- sam_manager") {
		t.Errorf("inherits did not resolve built-in UUID → name:\n%s", body)
	}
	if strings.Contains(body, "builtin-1") {
		t.Errorf("raw built-in inherits UUID leaked into spec:\n%s", body)
	}
}

func TestPullRBACClaimMappings_SlugCollisionDedupes(t *testing.T) {
	srv := &rbacFakeServer{
		roles: []wire.RoleResponse{
			{ID: "role-uuid-1", Name: "sam_admin", Origin: entplatform.RBACOriginCustom},
		},
		claimMaps: []wire.ClaimMappingResponse{
			{ID: "m1", OIDCProvider: new("azure"), ClaimValue: "sam:admins", RoleIDs: []string{"role-uuid-1"}, Origin: entplatform.RBACOriginCustom},
			{ID: "m2", OIDCProvider: new("azure"), ClaimValue: "sam/admins", RoleIDs: []string{"role-uuid-1"}, Origin: entplatform.RBACOriginCustom},
		},
	}
	httpsrv := newRBACFakeServer(t, srv)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullRBACClaimMappings(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullRBACClaimMappings: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries despite slug collision, got %d: %+v", len(entries), relPaths(entries))
	}
	if len(names) != 2 {
		t.Fatalf("expected 2 manifest names despite slug collision, got %v", names)
	}

	const base = "azure__sam-admins"
	wantBase := filepath.Join(rbacClaimMappingsDir, base+".yaml")
	wantSuffixed := filepath.Join(rbacClaimMappingsDir, base+"-2.yaml")

	var baseEntry, suffixedEntry *fileEntry
	for i := range entries {
		switch entries[i].relPath {
		case wantBase:
			baseEntry = &entries[i]
		case wantSuffixed:
			suffixedEntry = &entries[i]
		}
	}
	if baseEntry == nil {
		t.Fatalf("missing base file %q: %v", wantBase, relPaths(entries))
	}
	if suffixedEntry == nil {
		t.Fatalf("missing deduped file %q (dedupeName suffix scheme): %v", wantSuffixed, relPaths(entries))
	}
	if !strings.Contains(string(baseEntry.content), "sam:admins") {
		t.Errorf("base file lost its claimValue spec:\n%s", baseEntry.content)
	}
	if strings.Contains(string(baseEntry.content), "sam/admins") {
		t.Errorf("base file picked up the other row's claimValue:\n%s", baseEntry.content)
	}
	if !strings.Contains(string(suffixedEntry.content), "sam/admins") {
		t.Errorf("deduped file lost its claimValue spec:\n%s", suffixedEntry.content)
	}
	if strings.Contains(string(suffixedEntry.content), "sam:admins") {
		t.Errorf("deduped file picked up the other row's claimValue:\n%s", suffixedEntry.content)
	}
}

func TestPullRBACRoles_DBToDBInheritsResolvesToNames(t *testing.T) {
	srv := &rbacFakeServer{
		roles: []wire.RoleResponse{
			{ID: "parent-uuid", Name: "sam_user", Origin: entplatform.RBACOriginCustom},
			{ID: "child-uuid", Name: "incident_responder", Inherits: []string{"parent-uuid"}, Origin: entplatform.RBACOriginCustom},
		},
	}
	httpsrv := newRBACFakeServer(t, srv)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullRBACRoles(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullRBACRoles: %v", err)
	}
	var child *fileEntry
	for i := range entries {
		if strings.Contains(entries[i].relPath, "incident_responder") {
			child = &entries[i]
		}
	}
	if child == nil {
		t.Fatalf("missing child role file: %+v", relPaths(entries))
	}
	body := string(child.content)
	if !strings.Contains(body, "- sam_user") {
		t.Errorf("inherits did not resolve UUID → role name:\n%s", body)
	}
	if strings.Contains(body, "parent-uuid") {
		t.Errorf("raw inherits UUID leaked into spec:\n%s", body)
	}
}

func TestPullRBACRoles_DanglingAssignmentRoleIDIsLossy(t *testing.T) {
	srv := &rbacFakeServer{
		roles: []wire.RoleResponse{
			{ID: "role-admin", Name: "sam_admin", Origin: entplatform.RBACOriginCustom},
		},
		grants: []wire.GrantResponse{
			{ID: "a1", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "alice"}, RoleID: "missing-role", Origin: entplatform.RBACOriginCustom},
		},
	}
	httpsrv := newRBACFakeServer(t, srv)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullRBACRoles(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullRBACRoles: %v", err)
	}
	for i := range entries {
		if strings.Contains(string(entries[i].content), "alice") {
			t.Errorf("dangling assignment folded a user despite unresolved roleId:\n%s", entries[i].content)
		}
	}
	assertLossy(t, res, "missing-role")
}

func TestRun_LossyResourceFailsWithoutWriting(t *testing.T) {
	srv := &rbacFakeServer{
		roles: []wire.RoleResponse{
			{ID: "role-admin", Name: "sam_admin", Origin: entplatform.RBACOriginCustom},
		},
		grants: []wire.GrantResponse{
			{ID: "a1", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "alice"}, RoleID: "missing-role", Origin: entplatform.RBACOriginCustom},
		},
	}
	httpsrv := newRBACFakeServer(t, srv)

	out := filepath.Join(t.TempDir(), "repo")
	_, err := Run(context.Background(), target.Target{URL: httpsrv.URL}, Options{OutputDir: out})
	if err == nil {
		t.Fatal("Run succeeded; want an error for the unresolvable grant")
	}
	for _, want := range []string{"missing-role", "nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Errorf("output dir exists after a failed pull (stat err: %v)", statErr)
	}
}

func TestRun_LossyResourceLeavesExistingTreeUntouched(t *testing.T) {
	srv := &rbacFakeServer{
		roles: []wire.RoleResponse{
			{ID: "role-admin", Name: "sam_admin", Origin: entplatform.RBACOriginCustom},
		},
		grants: []wire.GrantResponse{
			{ID: "a1", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "alice"}, RoleID: "missing-role", Origin: entplatform.RBACOriginCustom},
		},
	}
	httpsrv := newRBACFakeServer(t, srv)

	cases := []struct {
		name string
		opts Options
	}{
		{"force", Options{Force: true}},
		{"merge", Options{Merge: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := t.TempDir()
			seed := map[string]string{
				"README.md": "user notes\n",
				filepath.Join(rbacRolesDir, "stale.yaml"):       "kind: rbacRole\nname: stale\n",
				filepath.Join("skills", "web", skillMDFilename): "---\nname: web\n---\n",
			}
			for rel, content := range seed {
				full := filepath.Join(out, rel)
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			before := readTree(t, out)

			tc.opts.OutputDir = out
			if _, err := Run(t.Context(), target.Target{URL: httpsrv.URL}, tc.opts); err == nil || !strings.Contains(err.Error(), "nothing was written") {
				t.Fatalf("Run error = %v; want the lossy-pull error", err)
			}
			if after := readTree(t, out); !maps.Equal(before, after) {
				t.Errorf("failed pull changed the output dir:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		tree[rel] = string(content)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestRun_NameScopedPullSkipsUnrelatedBrokenRBACRows(t *testing.T) {
	srv := &rbacFakeServer{
		roles: []wire.RoleResponse{
			{ID: "role-ok", Name: "ok-role", Origin: entplatform.RBACOriginCustom},
		},
		grants: []wire.GrantResponse{
			{ID: "g1", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "alice"}, RoleID: "role-ok", Origin: entplatform.RBACOriginCustom},
			{ID: "g2", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "bob"}, RoleID: "missing-role", Origin: entplatform.RBACOriginCustom},
		},
		claimMaps: []wire.ClaimMappingResponse{
			{ID: "m1", OIDCProvider: new("azure"), ClaimValue: "ok", RoleIDs: []string{"role-ok"}, Origin: entplatform.RBACOriginCustom},
			{ID: "m2", OIDCProvider: new("azure"), ClaimValue: "broken", RoleIDs: []string{"missing-role"}, Origin: entplatform.RBACOriginCustom},
		},
	}
	httpsrv := newRBACFakeServer(t, srv)

	cases := []struct {
		name     string
		opts     Options
		wantFile string
	}{
		{"role", Options{OnlyKind: "rbacRole", Name: "ok-role"}, filepath.Join(rbacRolesDir, "ok-role.yaml")},
		{"claim mapping", Options{OnlyKind: "rbacClaimMapping", Name: "azure__ok"}, filepath.Join(rbacClaimMappingsDir, "azure__ok.yaml")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.opts.OutputDir = filepath.Join(t.TempDir(), "repo")
			if _, err := Run(t.Context(), target.Target{URL: httpsrv.URL}, tc.opts); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if _, err := os.Stat(filepath.Join(tc.opts.OutputDir, tc.wantFile)); err != nil {
				t.Errorf("scoped pull did not write %s: %v", tc.wantFile, err)
			}
		})
	}
}

func TestPullRBACGrants_BuiltinRoleGrantsEmitGrantFiles(t *testing.T) {
	srv := &rbacFakeServer{
		roles: []wire.RoleResponse{
			{ID: "builtin-1", Name: "sam_manager", Origin: entplatform.RBACOriginBuiltin},
			{ID: "db-1", Name: "editor", Origin: entplatform.RBACOriginCustom},
		},
		grants: []wire.GrantResponse{
			{ID: "g1", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "ops@corp.com"}, RoleID: "builtin-1", Origin: entplatform.RBACOriginCustom},
			{ID: "g2", Subject: &apiv2.GrantSubject{Type: "sub", Value: "u-9", Issuer: new("https://idp")}, RoleID: "builtin-1", Origin: entplatform.RBACOriginCustom},
			{ID: "g3", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "someone@x"}, RoleID: "db-1", Origin: entplatform.RBACOriginCustom},
		},
	}
	httpsrv := newRBACFakeServer(t, srv)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullRBACGrants(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullRBACGrants: %v", err)
	}
	if len(entries) != 1 || len(names) != 1 || names[0] != "sam_manager" {
		t.Fatalf("expected one grant file for sam_manager, got entries=%v names=%v", relPaths(entries), names)
	}
	if !strings.HasSuffix(entries[0].relPath, "rbac/grants/sam_manager.yaml") {
		t.Errorf("rel path = %q; want rbac/grants/sam_manager.yaml", entries[0].relPath)
	}
	body := string(entries[0].content)
	for _, want := range []string{"kind: rbacGrant", "roleName: sam_manager", "ops@corp.com", "type: sub", "value: u-9", "issuer: https://idp"} {
		if !strings.Contains(body, want) {
			t.Errorf("grant file missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "someone@x") {
		t.Errorf("db-role grant leaked into built-in grant file:\n%s", body)
	}
}

func TestPullRBAC_RoundTripIsNoOp(t *testing.T) {
	srv := &rbacFakeServer{
		roles: []wire.RoleResponse{
			{
				ID:       "role-uuid-admin",
				Name:     "sam_admin",
				Scopes:   []string{"rbac:_:*", "agent:*:*"},
				Inherits: nil,
				Origin:   entplatform.RBACOriginCustom,
			},
			{
				ID:       "role-uuid-incident",
				Name:     "incident_responder",
				Scopes:   []string{"agent:incident-bot:invoke", "agent:incident-bot:read"},
				Inherits: []string{"role-uuid-admin"},
				Origin:   entplatform.RBACOriginCustom,
			},
			{
				ID:     yamlRoleID("yaml_viewer"),
				Name:   "yaml_viewer",
				Scopes: []string{"agent:*:read"},
				Origin: entplatform.RBACOriginYAML,
			},
		},
		grants: []wire.GrantResponse{
			{ID: "a1", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "alice@example.com"}, RoleID: "role-uuid-incident", Origin: entplatform.RBACOriginCustom},
			{ID: "a2", Subject: &apiv2.GrantSubject{Type: "sub", Value: "sub-xyz", Issuer: new("https://idp")}, RoleID: "role-uuid-incident", Origin: entplatform.RBACOriginCustom},
		},
		claimMaps: []wire.ClaimMappingResponse{
			{ID: "m1", Name: new("Admins"), OIDCProvider: new("azure"), ClaimValue: "sam-admins", RoleIDs: []string{"role-uuid-admin"}, Origin: entplatform.RBACOriginCustom},
		},
	}
	httpsrv := newRBACFakeServer(t, srv)
	tg := target.Target{URL: httpsrv.URL}

	out := t.TempDir()
	res, err := Run(context.Background(), tg, Options{OutputDir: out})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Counts["rbacRole"] != 2 {
		t.Errorf("rbacRole count = %d; want 2 (yaml row excluded)", res.Counts["rbacRole"])
	}
	if res.Counts["rbacGrant"] != 0 {
		t.Errorf("db-role grants must fold into users:, not standalone files; got rbacGrant count %d", res.Counts["rbacGrant"])
	}
	incidentFile := filepath.Join(out, "rbac", "roles", "incident_responder.yaml")
	incidentBody, readErr := os.ReadFile(incidentFile)
	if readErr != nil {
		t.Fatalf("read incident_responder.yaml: %v", readErr)
	}
	if !strings.Contains(string(incidentBody), "alice@example.com") {
		t.Errorf("expected folded users: on incident_responder role file:\n%s", incidentBody)
	}
	for _, want := range []string{"type: sub", "value: sub-xyz", "issuer: https://idp"} {
		if !strings.Contains(string(incidentBody), want) {
			t.Errorf("expected typed sub entry (%q) in role file:\n%s", want, incidentBody)
		}
	}
	if res.Counts["rbacClaimMapping"] != 1 {
		t.Errorf("rbacClaimMapping count = %d; want 1", res.Counts["rbacClaimMapping"])
	}

	m, err := manifest.Load(res.ManifestPath)
	if err != nil {
		t.Fatalf("load pulled manifest at %s: %v", res.ManifestPath, err)
	}

	plan, err := reconcile.Plan(context.Background(), tg, out, m)
	if err != nil {
		t.Fatalf("re-Plan: %v", err)
	}
	assertRBACPlanIsClean(t, plan, struct{ Roles, Grants, ClaimMappings int }{Roles: 2, Grants: 2, ClaimMappings: 1})
}

func TestPullRBAC_RoundTripIsNoOp_WithYAMLPreview(t *testing.T) {
	srv := &rbacFakeServer{
		roles: []wire.RoleResponse{
			{ID: "role-uuid-admin", Name: "sam_admin", Scopes: []string{"rbac:_:*"}, Origin: entplatform.RBACOriginCustom},
			{ID: yamlRoleID("yaml_viewer"), Name: "yaml_viewer", Scopes: []string{"agent:*:read"}, Origin: entplatform.RBACOriginYAML},
		},
		grants: []wire.GrantResponse{
			{ID: "a1", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "alice@example.com"}, RoleID: "role-uuid-admin", Origin: entplatform.RBACOriginCustom},
			{ID: "y1", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "yaml-bob@example.com"}, RoleID: yamlRoleID("yaml_viewer"), Origin: entplatform.RBACOriginYAML},
		},
		claimMaps: []wire.ClaimMappingResponse{
			{ID: "m1", Name: new("Admins"), OIDCProvider: new("azure"), ClaimValue: "sam-admins", RoleIDs: []string{"role-uuid-admin"}, Origin: entplatform.RBACOriginCustom},
			{ID: "y2", OIDCProvider: new("okta"), ClaimValue: "viewers", RoleIDs: []string{yamlRoleID("yaml_viewer")}, Origin: entplatform.RBACOriginYAML},
		},
	}
	httpsrv := newRBACFakeServer(t, srv)
	tg := target.Target{URL: httpsrv.URL}

	out := t.TempDir()
	res, err := Run(context.Background(), tg, Options{OutputDir: out, IncludeYAMLPreview: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, glob := range []string{
		filepath.Join(out, "rbac", "roles", "_yaml-preview", "*.yaml"),
		filepath.Join(out, "rbac", "claim-mappings", "_yaml-preview", "*.yaml"),
	} {
		matches, _ := filepath.Glob(glob)
		if len(matches) == 0 {
			t.Errorf("expected at least one preview file matching %s", glob)
		}
	}

	m, err := manifest.Load(res.ManifestPath)
	if err != nil {
		t.Fatalf("load pulled manifest: %v", err)
	}

	plan, err := reconcile.Plan(context.Background(), tg, out, m)
	if err != nil {
		t.Fatalf("re-Plan: %v", err)
	}
	assertRBACPlanIsClean(t, plan, struct{ Roles, Grants, ClaimMappings int }{Roles: 1, Grants: 1, ClaimMappings: 1})
}

func assertRBACPlanIsClean(t *testing.T, plan *reconcile.PlanResult, wantUnchanged struct {
	Roles, Grants, ClaimMappings int
}) {
	t.Helper()
	if plan.RBACRoles == nil {
		t.Fatal("plan.RBACRoles is nil")
	}
	if n := len(plan.RBACRoles.Creates) + len(plan.RBACRoles.Updates) + len(plan.RBACRoles.Deletes); n != 0 {
		t.Errorf("rbacRoles round-trip not clean: creates=%d updates=%d deletes=%d unchanged=%d",
			len(plan.RBACRoles.Creates), len(plan.RBACRoles.Updates), len(plan.RBACRoles.Deletes), len(plan.RBACRoles.Unchanged))
	}
	if got := len(plan.RBACRoles.Unchanged); got != wantUnchanged.Roles {
		t.Errorf("rbacRoles unchanged = %d; want %d", got, wantUnchanged.Roles)
	}
	if plan.RBACGrants == nil {
		t.Fatal("plan.RBACGrants is nil")
	}
	if n := len(plan.RBACGrants.Creates) + len(plan.RBACGrants.Deletes); n != 0 {
		t.Errorf("rbacGrants round-trip not clean: creates=%d deletes=%d unchanged=%d",
			len(plan.RBACGrants.Creates), len(plan.RBACGrants.Deletes), len(plan.RBACGrants.Unchanged))
	}
	if got := len(plan.RBACGrants.Unchanged); got != wantUnchanged.Grants {
		t.Errorf("rbacGrants unchanged = %d; want %d", got, wantUnchanged.Grants)
	}
	if plan.RBACClaimMappings == nil {
		t.Fatal("plan.RBACClaimMappings is nil")
	}
	if n := len(plan.RBACClaimMappings.Creates) + len(plan.RBACClaimMappings.Deletes); n != 0 {
		t.Errorf("rbacClaimMappings round-trip not clean: creates=%d deletes=%d unchanged=%d",
			len(plan.RBACClaimMappings.Creates), len(plan.RBACClaimMappings.Deletes), len(plan.RBACClaimMappings.Unchanged))
	}
	if got := len(plan.RBACClaimMappings.Unchanged); got != wantUnchanged.ClaimMappings {
		t.Errorf("rbacClaimMappings unchanged = %d; want %d", got, wantUnchanged.ClaimMappings)
	}
}

func TestPullRBAC_FeatureDisabledIsWarning(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(platform.APIV2Prefix+"/rbac/", func(w http.ResponseWriter, _ *http.Request) {
		writeV2Error(w, http.StatusNotImplemented, "feature off")
	})
	c := newV2PlatformClient(t, mux)

	for _, tc := range []struct {
		name string
		fn   kindFetcher
	}{
		{"rbacRole", pullRBACRoles},
		{"rbacClaimMapping", pullRBACClaimMappings},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
			entries, names, err := tc.fn(t.Context(), c, Options{}, res)
			if err != nil {
				t.Fatalf("%s: unexpected error %v", tc.name, err)
			}
			if len(entries) != 0 || len(names) != 0 {
				t.Errorf("%s: expected empty result on 501; got entries=%v names=%v", tc.name, relPaths(entries), names)
			}
			if len(res.Warnings) == 0 {
				t.Errorf("%s: expected a warning on 501; got none", tc.name)
			}
		})
	}
}

func relPaths(entries []fileEntry) []string {
	out := make([]string, len(entries))
	for i := range entries {
		out[i] = entries[i].relPath
	}
	return out
}
