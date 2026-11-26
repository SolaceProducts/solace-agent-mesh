// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apiv2"
)

func resolvedRole(name string, scopes, inherits []string) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Kind:       "rbacRole",
		Name:       name,
		SourceFile: "rbac/roles/" + name + ".yaml",
		Spec: map[string]any{
			"scopes":   asAnySlice(scopes),
			"inherits": asAnySlice(inherits),
		},
	}
}

func resolvedGrant(header, identity, roleName string) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Kind:       "rbacGrant",
		Name:       header,
		SourceFile: "rbac/roles/" + roleName + ".yaml",
		Spec: map[string]any{
			"identity": identity,
			"roleName": roleName,
		},
	}
}

func resolvedClaimMapping(header, oidcProvider, claimValue string, roleNames ...string) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Kind:       "rbacClaimMapping",
		Name:       header,
		SourceFile: "rbac/claim-mappings/" + header + ".yaml",
		Spec: map[string]any{
			"oidcProvider": oidcProvider,
			"claimValue":   claimValue,
			"roleNames":    asAnySlice(roleNames),
		},
	}
}

func asAnySlice(s []string) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

func TestPlanRBACRoles_CreateAndDelete(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedRole("incident_responder", []string{"agent:incident-bot:invoke"}, nil)}
	actual := []wire.RoleResponse{{
		ID:     "uuid-old",
		Name:   "obsolete",
		Origin: entplatform.RBACOriginCustom,
	}}
	plan, err := planRBACRoles(desired, actual)
	if err != nil {
		t.Fatalf("planRBACRoles: %v", err)
	}
	if len(plan.Creates) != 1 || plan.Creates[0].Name != "incident_responder" {
		t.Errorf("Creates = %+v", plan.Creates)
	}
	if len(plan.Deletes) != 1 || plan.Deletes[0].Name != "obsolete" {
		t.Errorf("Deletes = %+v", plan.Deletes)
	}
}

func TestPlanRBACRoles_NoOpWhenUnchanged(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedRole("sam_user", []string{"agent:*:read"}, nil)}
	actual := []wire.RoleResponse{{
		ID:     "uuid-1",
		Name:   "sam_user",
		Scopes: []string{"agent:*:read"},
		Origin: entplatform.RBACOriginCustom,
	}}
	plan, err := planRBACRoles(desired, actual)
	if err != nil {
		t.Fatalf("planRBACRoles: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("Unchanged = %+v (creates=%v updates=%v deletes=%v)", plan.Unchanged, plan.Creates, plan.Updates, plan.Deletes)
	}
}

func TestPlanRBACRoles_NoOpWhenScopesReordered(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedRole("sam_builder_prod", []string{"agent:*:read", "agent:*:invoke", "artifact:*:read"}, nil)}
	actual := []wire.RoleResponse{{
		ID:     "uuid-1",
		Name:   "sam_builder_prod",
		Scopes: []string{"artifact:*:read", "agent:*:invoke", "agent:*:read"},
		Origin: entplatform.RBACOriginCustom,
	}}
	plan, err := planRBACRoles(desired, actual)
	if err != nil {
		t.Fatalf("planRBACRoles: %v", err)
	}
	if len(plan.Unchanged) != 1 || len(plan.Updates) != 0 {
		t.Errorf("Unchanged = %+v updates=%+v; same scope set in a different order must not plan an update", plan.Unchanged, plan.Updates)
	}
}

func TestPlanRBACRoles_UpdatesWhenScopeSetDiffers(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedRole("sam_builder_prod", []string{"agent:*:read", "agent:*:invoke"}, nil)}
	actual := []wire.RoleResponse{{
		ID:     "uuid-1",
		Name:   "sam_builder_prod",
		Scopes: []string{"agent:*:invoke", "artifact:*:read"},
		Origin: entplatform.RBACOriginCustom,
	}}
	plan, err := planRBACRoles(desired, actual)
	if err != nil {
		t.Fatalf("planRBACRoles: %v", err)
	}
	if len(plan.Updates) != 1 {
		t.Errorf("Updates = %+v, want one for a changed scope set", plan.Updates)
	}
}

func TestPlanRBACRoles_RejectsInvalidScope(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedRole("bad_role", []string{"NOT A VALID SCOPE"}, nil)}
	_, err := planRBACRoles(desired, nil)
	if err == nil {
		t.Fatalf("expected error for invalid scope, got nil")
	}
}

func TestPlanRBACRoles_RejectsCycle(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedRole("a", nil, []string{"b"}),
		resolvedRole("b", nil, []string{"a"}),
	}
	_, err := planRBACRoles(desired, nil)
	if err == nil {
		t.Fatalf("expected error for inheritance cycle, got nil")
	}
}

func TestPlanRBACRoles_RejectsCycleThroughExistingPlatformRole(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedRole("a", nil, []string{"b"}),
	}
	actual := []wire.RoleResponse{
		{
			ID:       "uuid-a",
			Name:     "a",
			Inherits: nil,
			Origin:   entplatform.RBACOriginCustom,
		},
		{
			ID:       "uuid-b",
			Name:     "b",
			Inherits: []string{"uuid-a"},
			Origin:   entplatform.RBACOriginCustom,
		},
	}
	_, err := planRBACRoles(desired, actual)
	if err == nil {
		t.Fatalf("expected error for inheritance cycle through existing platform role, got nil")
	}
}

func TestPlanRBACRoles_RejectsYAMLNameCollision(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedRole("sam_user", nil, nil)}
	actual := []wire.RoleResponse{{
		Name:   "sam_user",
		Origin: entplatform.RBACOriginYAML,
	}}
	_, err := planRBACRoles(desired, actual)
	if err == nil {
		t.Fatalf("expected error for YAML name collision, got nil")
	}
}

func TestPlanRBACRoles_ListFilterDropsYAMLRows(t *testing.T) {
	desired := []resolve.ResolvedResource{resolvedRole("incident_responder", []string{"agent:incident-bot:invoke"}, nil)}
	actual := []wire.RoleResponse{
		{
			ID:     "uuid-1",
			Name:   "yaml_admin",
			Origin: entplatform.RBACOriginYAML,
		},
	}
	plan, err := planRBACRoles(desired, actual)
	if err != nil {
		t.Fatalf("planRBACRoles: %v", err)
	}
	for _, op := range plan.Deletes {
		if op.Name == "yaml_admin" {
			t.Errorf("YAML row %q was proposed for delete; filter is broken", op.Name)
		}
	}
}

func roleWithUsers(name string, users []any) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Kind:       "rbacRole",
		Name:       name,
		SourceFile: "rbac/roles/" + name + ".yaml",
		Spec:       map[string]any{"scopes": []any{"agent:*:read"}, "users": users},
	}
}

func TestExtractRoleUsers_FanOutShapeAndNaming(t *testing.T) {
	desired := []resolve.ResolvedResource{
		roleWithUsers("sam_builder", []any{" victor@x ", "peggy@x"}),
	}
	got, err := extractRoleUsers(desired)
	if err != nil {
		t.Fatalf("extractRoleUsers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 synthetic rows, got %d: %+v", len(got), got)
	}
	first := got[0]
	if first.Kind != "rbacGrant" {
		t.Errorf("synthetic row kind = %q, want rbacGrant", first.Kind)
	}
	if first.Name != "sam_builder/users/victor@x" {
		t.Errorf("display name = %q, want sam_builder/users/victor@x (trimmed)", first.Name)
	}
	if first.SourceFile != "rbac/roles/sam_builder.yaml" {
		t.Errorf("source file = %q, want the role file", first.SourceFile)
	}
	if first.Spec["identity"] != "victor@x" || first.Spec["roleName"] != "sam_builder" {
		t.Errorf("spec = %+v, want identity=victor@x roleName=sam_builder (trimmed)", first.Spec)
	}
}

func TestExtractRoleUsers_TypedSubjects(t *testing.T) {
	desired := []resolve.ResolvedResource{
		roleWithUsers("ops", []any{
			"alice@x",
			map[string]any{"type": "email", "value": "bob@x"},
			map[string]any{"type": "sub", "issuer": "https://idp", "value": "abc-123"},
		}),
	}
	got, err := extractRoleUsers(desired)
	if err != nil {
		t.Fatalf("extractRoleUsers: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 rows, got %d: %+v", len(got), got)
	}
	if got[0].Spec["identity"] != "alice@x" || got[0].Spec["subjectType"] != nil {
		t.Errorf("bare string row = %+v, want identity=alice@x and no subjectType", got[0].Spec)
	}
	if got[1].Spec["identity"] != "bob@x" || got[1].Spec["subjectType"] != "email" {
		t.Errorf("typed email row = %+v", got[1].Spec)
	}
	if got[2].Spec["identity"] != "abc-123" || got[2].Spec["subjectType"] != "sub" || got[2].Spec["subjectIssuer"] != "https://idp" {
		t.Errorf("typed sub row = %+v", got[2].Spec)
	}
	if got[2].Name != "ops/users/sub:https://idp:abc-123" {
		t.Errorf("typed sub name = %q, want a distinct key", got[2].Name)
	}
}

func TestExtractRoleUsers_TypedValidation(t *testing.T) {
	cases := map[string][]any{
		"unknown type":       {map[string]any{"type": "phone", "value": "x"}},
		"sub without issuer": {map[string]any{"type": "sub", "value": "abc"}},
		"unknown key":        {map[string]any{"type": "email", "value": "x", "bogus": "y"}},
		"empty value":        {map[string]any{"type": "email", "value": ""}},
	}
	for name, users := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := extractRoleUsers([]resolve.ResolvedResource{roleWithUsers("r", users)}); err == nil {
				t.Fatalf("expected error for %q", name)
			}
		})
	}
}

func TestLegacyGrantAdvisories(t *testing.T) {
	desired, err := extractRoleUsers([]resolve.ResolvedResource{
		roleWithUsers("ops", []any{
			"alice@x",
			map[string]any{"type": "legacy", "value": "bob@x"},
			map[string]any{"type": "sub", "issuer": "https://idp", "value": "abc"},
			map[string]any{"type": "email", "value": "carol@x"},
		}),
	})
	if err != nil {
		t.Fatalf("extractRoleUsers: %v", err)
	}
	warns := legacyGrantAdvisories(desired)
	if len(warns) != 1 {
		t.Fatalf("want 1 aggregated advisory, got %d: %v", len(warns), warns)
	}
	w := warns[0]
	if !strings.Contains(w, "2 grant") {
		t.Errorf("advisory should count the 2 legacy grants: %q", w)
	}
	if !strings.Contains(w, "alice@x") || !strings.Contains(w, "bob@x") {
		t.Errorf("advisory should name the legacy identities: %q", w)
	}
	if strings.Contains(w, "abc") || strings.Contains(w, "carol@x") {
		t.Errorf("advisory must not name typed subjects: %q", w)
	}
}

func TestExtractRoleUsers_EmptyOrAbsentYieldsNoRows(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolvedRole("no_users", []string{"agent:*:read"}, nil),
		roleWithUsers("empty_users", []any{}),
	}
	got, err := extractRoleUsers(desired)
	if err != nil {
		t.Fatalf("extractRoleUsers: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no synthetic rows, got %+v", got)
	}
}

func TestExtractRoleUsers_RejectsDuplicateIdentity(t *testing.T) {
	desired := []resolve.ResolvedResource{roleWithUsers("dup", []any{"a@x", "a@x"})}
	_, err := extractRoleUsers(desired)
	if err == nil {
		t.Fatal("expected duplicate-identity error")
	}
	if !strings.Contains(err.Error(), "rbac/roles/dup.yaml") {
		t.Errorf("error must name the role file, got: %v", err)
	}
}

func TestExtractRoleUsers_RejectsNonStringAndEmptyEntries(t *testing.T) {
	for name, users := range map[string][]any{
		"non-string": {42},
		"blank":      {"  "},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := extractRoleUsers([]resolve.ResolvedResource{roleWithUsers("r", users)})
			if err == nil {
				t.Fatalf("expected error for %s users entry", name)
			}
		})
	}
}

func TestExtractRoleUsers_RejectsNonListUsers(t *testing.T) {
	r := resolve.ResolvedResource{
		Kind: "rbacRole", Name: "r", SourceFile: "rbac/roles/r.yaml",
		Spec: map[string]any{"users": "victor@x"},
	}
	if _, err := extractRoleUsers([]resolve.ResolvedResource{r}); err == nil {
		t.Fatal("expected error for non-list spec.users")
	}
}

func TestExtractRoleUsers_RejectsGroups(t *testing.T) {
	r := resolve.ResolvedResource{
		Kind: "rbacRole", Name: "r", SourceFile: "rbac/roles/r.yaml",
		Spec: map[string]any{"groups": []any{"eng"}},
	}
	_, err := extractRoleUsers([]resolve.ResolvedResource{r})
	if err == nil {
		t.Fatal("expected error for spec.groups")
	}
	if !strings.Contains(err.Error(), "rbacClaimMapping") {
		t.Errorf("groups error must redirect to kind: rbacClaimMapping, got: %v", err)
	}
	if !strings.Contains(err.Error(), "rbac/roles/r.yaml") {
		t.Errorf("groups error must name the source file, got: %v", err)
	}
}

func TestExtractRoleUsers_RejectsUnknownSpecKey(t *testing.T) {
	r := resolve.ResolvedResource{
		Kind: "rbacRole", Name: "sam_builder", SourceFile: "rbac/roles/sam_builder.yaml",
		Spec: map[string]any{
			"scopes": []any{"agent:*:read"},
			"usres":  []any{"victor@x"},
		},
	}
	_, err := extractRoleUsers([]resolve.ResolvedResource{r})
	if err == nil {
		t.Fatal("expected error for unknown spec key")
	}
	if !strings.Contains(err.Error(), "rbac/roles/sam_builder.yaml") {
		t.Errorf("error must name the source file, got: %v", err)
	}
	if !strings.Contains(err.Error(), "usres") {
		t.Errorf("error must name the offending key, got: %v", err)
	}
}

func TestExtractRoleUsers_MultiRoleSameIdentity(t *testing.T) {
	desired := []resolve.ResolvedResource{
		roleWithUsers("sam_builder", []any{"victor@x"}),
		roleWithUsers("sam_admin", []any{"victor@x"}),
	}
	got, err := extractRoleUsers(desired)
	if err != nil {
		t.Fatalf("extractRoleUsers: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected two independent rows (one per role), got %d: %+v", len(got), got)
	}
	names := []string{got[0].Name, got[1].Name}
	if names[0] == names[1] {
		t.Errorf("synthetic names must be distinct per role, both were %q", names[0])
	}
	want := map[string]struct{}{
		"sam_builder/users/victor@x": {},
		"sam_admin/users/victor@x":   {},
	}
	for _, n := range names {
		if _, ok := want[n]; !ok {
			t.Errorf("unexpected synthetic name %q", n)
		}
	}
	for _, row := range got {
		if row.Spec["identity"] != "victor@x" {
			t.Errorf("identity = %v, want victor@x (no dedup across roles)", row.Spec["identity"])
		}
	}
}

func TestExtractRoleUsers_ThenPlanEmptyProducesPruneGatedDeletes(t *testing.T) {
	desired, err := extractRoleUsers([]resolve.ResolvedResource{
		resolvedRole("sam_admin", []string{"rbac:_:*"}, nil),
	})
	if err != nil {
		t.Fatalf("extractRoleUsers: %v", err)
	}
	xref := rbacRoleXRefAdapter{
		nameToID: map[string]string{"sam_admin": "uuid-admin"},
		idToName: map[string]string{"uuid-admin": "sam_admin"},
		desired:  map[string]struct{}{"sam_admin": {}},
		yaml:     map[string]struct{}{},
	}
	actual := []wire.GrantResponse{
		{ID: "a1", Subject: &apiv2.GrantSubject{Type: "legacy", Value: "stale@x"}, RoleID: "uuid-admin", Origin: entplatform.RBACOriginCustom},
	}
	plan, err := planRBACGrants(desired, actual, nil, xref)
	if err != nil {
		t.Fatalf("planRBACGrants: %v", err)
	}
	if len(plan.Creates) != 0 {
		t.Errorf("expected no creates, got %+v", plan.Creates)
	}
	if len(plan.Deletes) != 1 || plan.Deletes[0].Identity != "stale@x" {
		t.Errorf("expected one pending delete for stale@x, got %+v", plan.Deletes)
	}
}

func TestPlanRBACGrants_RoleChangeIsCreatePlusDelete(t *testing.T) {
	xref := rbacRoleXRefAdapter{
		nameToID: map[string]string{
			"sam_user":  "uuid-user",
			"sam_admin": "uuid-admin",
		},
		idToName: map[string]string{"uuid-user": "sam_user", "uuid-admin": "sam_admin"},
		desired:  map[string]struct{}{},
		yaml:     map[string]struct{}{},
	}
	desired := []resolve.ResolvedResource{resolvedGrant("alice-admin", "alice", "sam_admin")}
	actual := []wire.GrantResponse{{
		ID:      "grant-1",
		Subject: &apiv2.GrantSubject{Type: "legacy", Value: "alice"},
		RoleID:  "uuid-user",
		Origin:  entplatform.RBACOriginCustom,
	}}
	plan, err := planRBACGrants(desired, actual, nil, xref)
	if err != nil {
		t.Fatalf("planRBACGrants: %v", err)
	}
	if len(plan.Creates) != 1 {
		t.Errorf("expected one Create, got %+v", plan.Creates)
	}
	if len(plan.Deletes) != 1 {
		t.Errorf("expected one Delete, got %+v", plan.Deletes)
	}
	if plan.Creates[0].RoleName != "sam_admin" {
		t.Errorf("Create RoleName = %q, want sam_admin", plan.Creates[0].RoleName)
	}
	if plan.Deletes[0].RoleName != "sam_user" {
		t.Errorf("Delete RoleName = %q, want sam_user (via xref idToName)", plan.Deletes[0].RoleName)
	}
}

func TestPlanRBACGrants_RejectsYAMLRoleRef(t *testing.T) {
	xref := rbacRoleXRefAdapter{
		nameToID: map[string]string{},
		idToName: map[string]string{},
		desired:  map[string]struct{}{},
		yaml:     map[string]struct{}{"yaml_admin": {}},
	}
	desired := []resolve.ResolvedResource{resolvedGrant("alice-admin", "alice", "yaml_admin")}
	_, err := planRBACGrants(desired, nil, nil, xref)
	if err == nil {
		t.Fatalf("expected error for YAML role reference, got nil")
	}
}

func TestPlanRBACClaimMappings_ServerStampedClaimKeyIsNoOp(t *testing.T) {
	xref := rbacRoleXRefAdapter{
		nameToID: map[string]string{"sam_admin": "uuid-admin"},
		idToName: map[string]string{"uuid-admin": "sam_admin"},
		desired:  map[string]struct{}{},
		yaml:     map[string]struct{}{},
	}
	desired := []resolve.ResolvedResource{resolvedClaimMapping("azure-admins", "azure", "sam-admins", "sam_admin")}
	actual := []wire.ClaimMappingResponse{{
		ID:           "map-1",
		Name:         new("azure-admins"),
		OIDCProvider: new("azure"),
		ClaimKey:     new("groups"),
		ClaimValue:   "sam-admins",
		RoleIDs:      []string{"uuid-admin"},
		Origin:       entplatform.RBACOriginCustom,
	}}
	plan, err := planRBACClaimMappings(desired, actual, xref)
	if err != nil {
		t.Fatalf("planRBACClaimMappings: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("Unchanged = %+v (creates=%v deletes=%v)", plan.Unchanged, plan.Creates, plan.Deletes)
	}
}

func TestReadClaimMappingSpec_RejectsUnknownSpecKey(t *testing.T) {
	r := resolvedClaimMapping("azure-admins", "azure", "sam-admins", "sam_admin")
	r.Spec["roleId"] = "uuid-admin"
	_, _, err := readClaimMappingSpec(r)
	if err == nil {
		t.Fatal("expected unknown spec key error, got nil")
	}
	for _, want := range []string{"roleId", r.SourceFile, "allowed:"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestGrantKey_Format(t *testing.T) {
	got := grantKey("legacy", "", "alice", "uuid-admin")
	want := "legacy||alice|uuid-admin"
	if got != want {
		t.Errorf("grantKey = %q, want %q", got, want)
	}
}

func TestRBACClaimMappingKey_Format(t *testing.T) {
	got := claimMappingKey("azure", "sam-admins")
	want := "azure|sam-admins"
	if got != want {
		t.Errorf("claimMappingKey = %q, want %q", got, want)
	}
}

func TestDetectInheritsCycle_Linear(t *testing.T) {
	if err := detectInheritsCycle(map[string][]string{
		"a": {"b"},
		"b": {"c"},
		"c": nil,
	}); err != nil {
		t.Errorf("expected nil for linear chain, got %v", err)
	}
}

func TestDetectInheritsCycle_SelfReference(t *testing.T) {
	if err := detectInheritsCycle(map[string][]string{
		"a": {"a"},
	}); err == nil {
		t.Errorf("expected cycle error for self-reference")
	}
}

func roleOpNames(ops []rbacRoleOp) []string {
	names := make([]string, len(ops))
	for i, op := range ops {
		names[i] = op.Name
	}
	return names
}

func indexOfRoleOp(ops []rbacRoleOp, name string) int {
	for i, op := range ops {
		if op.Name == name {
			return i
		}
	}
	return -1
}

func TestSortRoleCreatesByInherits_ParentBeforeChildDespiteAlphabeticalOrder(t *testing.T) {
	creates := []rbacRoleOp{
		{Name: "aaa_child", Create: wire.CreateRoleRequest{Name: "aaa_child", Inherits: []string{"zzz_parent"}}},
		{Name: "zzz_parent", Create: wire.CreateRoleRequest{Name: "zzz_parent"}},
	}
	sorted := sortRoleCreatesByInherits(creates)
	parentIdx, childIdx := indexOfRoleOp(sorted, "zzz_parent"), indexOfRoleOp(sorted, "aaa_child")
	if parentIdx == -1 || childIdx == -1 || parentIdx > childIdx {
		t.Errorf("expected zzz_parent before aaa_child, got order %v", roleOpNames(sorted))
	}
}

func TestSortRoleCreatesByInherits_TransitiveChainReversedAlphabetically(t *testing.T) {
	creates := []rbacRoleOp{
		{Name: "a_grandchild", Create: wire.CreateRoleRequest{Name: "a_grandchild", Inherits: []string{"b_child"}}},
		{Name: "b_child", Create: wire.CreateRoleRequest{Name: "b_child", Inherits: []string{"c_parent"}}},
		{Name: "c_parent", Create: wire.CreateRoleRequest{Name: "c_parent"}},
	}
	want := []string{"c_parent", "b_child", "a_grandchild"}
	if got := roleOpNames(sortRoleCreatesByInherits(creates)); !slices.Equal(got, want) {
		t.Errorf("expected %v, got %v", want, got)
	}
}

func TestSortRoleCreatesByInherits_NoDependenciesPreservesOrder(t *testing.T) {
	creates := []rbacRoleOp{
		{Name: "alpha", Create: wire.CreateRoleRequest{Name: "alpha"}},
		{Name: "beta", Create: wire.CreateRoleRequest{Name: "beta"}},
	}
	want := []string{"alpha", "beta"}
	if got := roleOpNames(sortRoleCreatesByInherits(creates)); !slices.Equal(got, want) {
		t.Errorf("expected order preserved for independent roles: want %v, got %v", want, got)
	}
}

func TestSortRoleCreatesByInherits_BothParentsOfADiamondOrderedFirst(t *testing.T) {
	creates := []rbacRoleOp{
		{Name: "aaa_child", Create: wire.CreateRoleRequest{Name: "aaa_child", Inherits: []string{"bbb_parent", "ccc_parent"}}},
		{Name: "bbb_parent", Create: wire.CreateRoleRequest{Name: "bbb_parent"}},
		{Name: "ccc_parent", Create: wire.CreateRoleRequest{Name: "ccc_parent"}},
	}
	sorted := sortRoleCreatesByInherits(creates)
	if got := roleOpNames(sorted); len(got) != 3 {
		t.Fatalf("expected all 3 ops preserved, got %v", got)
	}
	childIdx := indexOfRoleOp(sorted, "aaa_child")
	if indexOfRoleOp(sorted, "bbb_parent") > childIdx || indexOfRoleOp(sorted, "ccc_parent") > childIdx {
		t.Errorf("expected both bbb_parent and ccc_parent before aaa_child, got %v", roleOpNames(sorted))
	}
}

func TestSortRoleCreatesByInherits_UUIDInheritsEntryDoesNotForceOrdering(t *testing.T) {
	creates := []rbacRoleOp{
		{Name: "aaa_child", Create: wire.CreateRoleRequest{Name: "aaa_child", Inherits: []string{"11111111-1111-1111-1111-111111111111", "zzz_parent"}}},
		{Name: "zzz_parent", Create: wire.CreateRoleRequest{Name: "zzz_parent"}},
	}
	sorted := sortRoleCreatesByInherits(creates)
	if got := roleOpNames(sorted); len(got) != 2 {
		t.Fatalf("expected both ops preserved, got %v", got)
	}
	if indexOfRoleOp(sorted, "zzz_parent") > indexOfRoleOp(sorted, "aaa_child") {
		t.Errorf("expected the same-batch name parent (zzz_parent) before the child despite the UUID-shaped sibling entry, got %v", roleOpNames(sorted))
	}
}

func TestRBACRoleXRefAdapter_DesiredSentinel(t *testing.T) {
	xref := rbacRoleXRefAdapter{
		nameToID: map[string]string{},
		idToName: map[string]string{},
		desired:  map[string]struct{}{"new_role": {}},
		yaml:     map[string]struct{}{},
	}
	id, err := xref.LookupID("rbacRole", "new_role")
	if err != nil {
		t.Fatalf("LookupID: %v", err)
	}
	if id != "new_role" {
		t.Errorf("expected sentinel placeholder %q, got %q", "new_role", id)
	}
}

func TestRBACRoleXRefAdapter_PassThroughForUnknownKind(t *testing.T) {
	xref := rbacRoleXRefAdapter{nameToID: map[string]string{}, idToName: map[string]string{}}
	got, err := xref.LookupID("other", "x")
	if err != nil || got != "x" {
		t.Errorf("expected pass-through for unknown kind, got (%q, %v)", got, err)
	}
}
