// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package pull

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/reconcile"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"gopkg.in/yaml.v3"
)

func systemUserFakeServer() *rbacFakeServer {
	return &rbacFakeServer{
		roles: []wire.RoleResponse{
			{ID: "role-uuid-1", Name: "sam_builder", Origin: entplatform.RBACOriginCustom},
		},
		systemUsers: []wire.SystemUserResponse{
			{
				ID: "su-1", Name: "orders-bot", Sub: "system:orders-bot", DisplayName: new("Orders Bot"),
				RoleIDs: []string{"role-uuid-1"}, Origin: entplatform.RBACOriginCustom,
			},
			{
				ID: rbac.DefaultSystemUserID, Name: "default", Sub: rbac.DefaultSystemUserSub,
				DisplayName: new("Default"), RoleIDs: []string{rbac.RoleSystemInvokeAll},
				Origin: entplatform.RBACOriginBuiltin,
			},
		},
	}
}

func TestPullSystemUsers_EmitsCustomAndSkipsBuiltin(t *testing.T) {
	httpsrv := newRBACFakeServer(t, systemUserFakeServer())
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullServiceAccounts(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullServiceAccounts: %v", err)
	}
	if len(entries) != 1 || len(names) != 1 {
		t.Fatalf("entries=%v names=%v; want only the custom principal", relPaths(entries), names)
	}
	if names[0] != "orders-bot" {
		t.Errorf("manifest name = %q, want orders-bot", names[0])
	}
	if got := entries[0].relPath; !strings.HasPrefix(got, rbacServiceAccountsDir+"/") {
		t.Errorf("emitted to %q, want a file under %s/", got, rbacServiceAccountsDir)
	}

	body := string(entries[0].content)
	for _, want := range []string{"kind: serviceAccount", "name: orders-bot", "displayName: Orders Bot", "- sam_builder"} {
		if !strings.Contains(body, want) {
			t.Errorf("pulled spec missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "role-uuid-1") {
		t.Errorf("role UUID leaked into the pulled spec:\n%s", body)
	}
	if strings.Contains(body, "default") {
		t.Errorf("built-in principal leaked into the pulled repo:\n%s", body)
	}
}

func TestPullSystemUsers_ManifestBlockMatchesResolver(t *testing.T) {
	if got := pluralKind("systemUser"); got != "systemUsers" {
		t.Errorf("pluralKind(systemUser) = %q, want systemUsers", got)
	}
	if got := kindDirFor("systemUser"); got != rbacSystemUsersDir {
		t.Errorf("kindDirFor(systemUser) = %q, want %q", got, rbacSystemUsersDir)
	}
}

func TestPullSystemUsers_EmitsTheSpecShapeApplyReadsBack(t *testing.T) {
	fake := systemUserFakeServer()
	httpsrv := newRBACFakeServer(t, fake)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, _, err := pullServiceAccounts(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullServiceAccounts: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("pull warned: %v", res.Warnings)
	}

	var doc struct {
		Kind string         `yaml:"kind"`
		Name string         `yaml:"name"`
		Spec map[string]any `yaml:"spec"`
	}
	if err := yaml.Unmarshal(entries[0].content, &doc); err != nil {
		t.Fatalf("pulled spec is not valid YAML: %v\n%s", err, entries[0].content)
	}
	if doc.Kind != "serviceAccount" {
		t.Errorf("kind = %q, want serviceAccount", doc.Kind)
	}
	if doc.Name != "orders-bot" {
		t.Errorf("header name = %q, want orders-bot", doc.Name)
	}
	if doc.Spec["name"] != "orders-bot" {
		t.Errorf("spec.name = %v, want orders-bot", doc.Spec["name"])
	}
	if doc.Spec["displayName"] != "Orders Bot" {
		t.Errorf("spec.displayName = %v, want Orders Bot", doc.Spec["displayName"])
	}
	roleNames, ok := doc.Spec["roleNames"].([]any)
	if !ok || len(roleNames) != 1 || roleNames[0] != "sam_builder" {
		t.Errorf("spec.roleNames = %v, want [sam_builder]", doc.Spec["roleNames"])
	}
}

func TestPullSystemUsers_SkipsAPrincipalWhoseRolesCannotAllBeNamed(t *testing.T) {
	fake := systemUserFakeServer()
	fake.systemUsers[0].RoleIDs = []string{"role-uuid-1", "role-uuid-missing"}
	httpsrv := newRBACFakeServer(t, fake)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	entries, names, err := pullServiceAccounts(t.Context(), c, Options{}, res)
	if err != nil {
		t.Fatalf("pullServiceAccounts: %v", err)
	}

	if len(entries) != 0 || len(names) != 0 {
		t.Fatalf("emitted %v (names %v); want the principal skipped entirely rather than written with a partial role set",
			relPaths(entries), names)
	}
	assertLossy(t, res, "orders-bot")
}

func TestPullSystemUsers_NameFilterSelectsOnePrincipal(t *testing.T) {
	fake := systemUserFakeServer()
	fake.systemUsers = append(fake.systemUsers, wire.SystemUserResponse{
		ID: "su-2", Name: "reporting-bot", Sub: "system:reporting-bot",
		RoleIDs: []string{"role-uuid-1"}, Origin: entplatform.RBACOriginCustom,
	})
	httpsrv := newRBACFakeServer(t, fake)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	_, names, err := pullServiceAccounts(t.Context(), c, Options{Name: "reporting-bot"}, res)
	if err != nil {
		t.Fatalf("pullServiceAccounts: %v", err)
	}
	if len(names) != 1 || names[0] != "reporting-bot" {
		t.Errorf("names = %v, want only [reporting-bot]", names)
	}
}

func TestPullSystemUsers_SkipsAndWarnsOnARowShadowingABuiltin(t *testing.T) {
	for _, tc := range []struct {
		origin     string
		deletePath string
	}{
		{entplatform.RBACOriginBuiltin, "DELETE " + platform.APIV2Prefix + "/rbac/grants/{id}"},
		{entplatform.RBACOriginCustom, ""},
	} {
		origin := tc.origin
		t.Run(origin, func(t *testing.T) {
			fake := systemUserFakeServer()
			shadowID := "019f0000-0000-7000-8000-00000000dead"
			fake.systemUsers[1] = wire.SystemUserResponse{
				ID: shadowID, Name: "default", Sub: rbac.DefaultSystemUserSub,
				DisplayName: new("Shadow"), RoleIDs: []string{rbac.RoleSystemInvokeAll, "role-uuid-1"}, Origin: origin,
			}
			fake.grants = []wire.GrantResponse{{ID: "grant-1", RoleID: "role-uuid-1", UserID: &shadowID, Origin: entplatform.RBACOriginCustom}}
			httpsrv := newRBACFakeServer(t, fake)
			c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

			res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
			entries, names, err := pullServiceAccounts(t.Context(), c, Options{}, res)
			if err != nil {
				t.Fatalf("pullServiceAccounts: %v", err)
			}

			if slices.Contains(names, "default") {
				t.Errorf("names = %v; the shadow row was emitted and the next plan would refuse the file", names)
			}
			for _, p := range relPaths(entries) {
				if strings.Contains(p, "default") {
					t.Errorf("emitted %q for a shadow row", p)
				}
			}
			if !slices.Contains(names, "orders-bot") {
				t.Errorf("names = %v; the ordinary principal must still be emitted", names)
			}
			if !strings.Contains(strings.Join(res.Warnings, "\n"), rbac.DefaultSystemUserSub) {
				t.Errorf("warnings = %v, want the shadow row named", res.Warnings)
			}
			if tc.deletePath != "" && !strings.Contains(strings.Join(res.Warnings, "\n"), tc.deletePath) {
				t.Errorf("warnings = %v, want the grant delete named as %s", res.Warnings, tc.deletePath)
			}
		})
	}
}

func TestPullSystemUsers_NoWarningForAConfiguredDBRoleOnV2(t *testing.T) {
	fake := systemUserFakeServer()
	fake.systemUsers[1].RoleIDs = []string{"role-uuid-1"}
	httpsrv := newRBACFakeServer(t, fake)
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	if _, _, err := pullServiceAccounts(t.Context(), c, Options{}, res); err != nil {
		t.Fatalf("pullServiceAccounts: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %v, want none for a built-in holding no DB grant", res.Warnings)
	}
}

func TestPullSystemUsers_NoWarningForABuiltinWithoutDBGrants(t *testing.T) {
	httpsrv := newRBACFakeServer(t, systemUserFakeServer())
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})

	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	if _, _, err := pullServiceAccounts(t.Context(), c, Options{}, res); err != nil {
		t.Fatalf("pullServiceAccounts: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %v, want none for a built-in holding only its shipped roles", res.Warnings)
	}
}

func TestPullServiceAccounts_KeepsARepositoryOnThePreRenameLayout(t *testing.T) {
	httpsrv := newRBACFakeServer(t, systemUserFakeServer())
	c := reconcile.NewClient(target.Resolved{URL: httpsrv.URL})
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, rbacSystemUsersDir), 0o755); err != nil {
		t.Fatal(err)
	}
	opts := Options{OutputDir: repo}
	res := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}

	if entries, _, err := pullServiceAccounts(t.Context(), c, opts, res); err != nil || len(entries) != 0 {
		t.Fatalf("new-layout fetcher on a legacy repo = %v, %v; want nothing written", relPaths(entries), err)
	}
	entries, names, err := pullLegacySystemUsers(t.Context(), c, opts, res)
	if err != nil || len(entries) != 1 || !slices.Equal(names, []string{"orders-bot"}) {
		t.Fatalf("legacy fetcher = %v %v, %v; want the custom principal under the old layout", relPaths(entries), names, err)
	}
	if got := entries[0].relPath; !strings.HasPrefix(got, rbacSystemUsersDir+"/") || !strings.Contains(string(entries[0].content), "kind: systemUser") {
		t.Errorf("emitted %q with kind %q; want rbac/system-users/ and kind: systemUser so an older CLI still reads it", got, string(entries[0].content))
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %v; a repository on one layout gets none", res.Warnings)
	}

	if err := os.MkdirAll(filepath.Join(repo, rbacServiceAccountsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := pullServiceAccounts(t.Context(), c, opts, res); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, rbacSystemUsersDir+"/ is no longer written") }) {
		t.Errorf("warnings = %v; want the old directory and its migration steps named once both exist", res.Warnings)
	}

	forced := &Result{Counts: map[string]int{}, Skipped: map[string]int{}}
	if _, _, err := pullServiceAccounts(t.Context(), c, Options{OutputDir: repo, Force: true}, forced); err != nil {
		t.Fatalf("pullServiceAccounts with --force on a repo holding both directories: %v", err)
	}
	if want := []string{rbacSystemUsersDir + "/ was removed: service accounts are written to " + rbacServiceAccountsDir + "/"}; !slices.Equal(forced.Warnings, want) {
		t.Errorf("warnings under --force = %v, want %v: the wipe deletes the old directory, so no advice to move it", forced.Warnings, want)
	}
}

func TestRun_OnlyEitherServiceAccountSpellingPullsTheServiceAccounts(t *testing.T) {
	httpsrv := newRBACFakeServer(t, systemUserFakeServer())
	for _, only := range []string{resolve.ServiceAccountKind, resolve.LegacyServiceAccountKind} {
		t.Run(only, func(t *testing.T) {
			out := t.TempDir()
			res, err := Run(t.Context(), target.Target{URL: httpsrv.URL}, Options{OutputDir: out, OnlyKind: only})
			if err != nil {
				t.Fatalf("Run --only %s: %v", only, err)
			}
			if _, err := os.Stat(filepath.Join(out, rbacServiceAccountsDir, "orders-bot.yaml")); err != nil || res.Counts[resolve.ServiceAccountKind] != 1 {
				t.Errorf("Run --only %s into an empty directory: stat = %v, counts = %v; want orders-bot written under %s", only, err, res.Counts, rbacServiceAccountsDir)
			}
		})
	}
}
