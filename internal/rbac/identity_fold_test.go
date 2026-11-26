// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import "testing"

func TestEmailFoldIndex_ExactMatchNeverNeedsIndex(t *testing.T) {
	users := map[string]UserAssignment{
		"alice@example.org": {Roles: []string{"sam_user"}},
	}
	idx := BuildEmailFoldIndex(users)
	assignment, ok := idx.ResolveBare(users, "alice@example.org")
	if !ok || assignment.Roles[0] != "sam_user" {
		t.Errorf("exact match failed: ok=%v assignment=%v", ok, assignment)
	}
}

func TestEmailFoldIndex_BareFallbackIsCaseAndWhitespaceInsensitive(t *testing.T) {
	users := map[string]UserAssignment{
		"Mixed.Case@example.com": {Roles: []string{"sam_admin"}},
	}
	idx := BuildEmailFoldIndex(users)

	assignment, ok := idx.ResolveBare(users, "mixed.case@example.com")
	if !ok || assignment.Roles[0] != "sam_admin" {
		t.Errorf("expected fallback match: ok=%v assignment=%v", ok, assignment)
	}

	usersWithSpace := map[string]UserAssignment{
		" Mixed.Case@example.com ": {Roles: []string{"sam_admin"}},
	}
	idx = BuildEmailFoldIndex(usersWithSpace)
	assignment, ok = idx.ResolveBare(usersWithSpace, "mixed.case@example.com")
	if !ok || assignment.Roles[0] != "sam_admin" {
		t.Errorf("expected whitespace-tolerant fallback match: ok=%v assignment=%v", ok, assignment)
	}
}

func TestEmailFoldIndex_NonEmailIdentityNeverFuzzyMatches(t *testing.T) {
	users := map[string]UserAssignment{
		"alice@example.org": {Roles: []string{"sam_admin"}},
	}
	idx := BuildEmailFoldIndex(users)
	if _, ok := idx.ResolveBare(users, "bob"); ok {
		t.Error("a non-email-shaped identity must never match via the fallback")
	}
}

func TestEmailFoldIndex_SystemPrincipalNeverCaseFolds(t *testing.T) {
	users := map[string]UserAssignment{
		"system:bob@corp.example": {Roles: []string{"system_agent_invoke"}},
	}
	idx := BuildEmailFoldIndex(users)

	if _, ok := idx.ResolveBare(users, "system:bob@corp.example"); !ok {
		t.Error("the system principal's own exact case must still resolve")
	}
	if _, ok := idx.ResolveBare(users, "system:BOB@corp.example"); ok {
		t.Error("a system: principal must never be case-folded, even when it's itself email-shaped")
	}
}

func TestEmailFoldIndex_GatewayCompositeEmailDoesNotLeakIntoBareBucket(t *testing.T) {
	users := map[string]UserAssignment{
		"gateway_specific_identities:slack:Bob@example.org": {Roles: []string{"sam_user"}},
	}
	idx := BuildEmailFoldIndex(users)

	if _, ok := idx.ResolveBare(users, "bob@example.org"); ok {
		t.Error("a gateway composite key's embedded email must not leak into the bare fallback bucket")
	}
}

func TestEmailFoldIndex_CompositeFallbackIsCaseInsensitive(t *testing.T) {
	users := map[string]UserAssignment{
		"gateway_specific_identities:slack:Bob@example.org": {Roles: []string{"sam_user"}},
	}
	idx := BuildEmailFoldIndex(users)

	assignment, ok := idx.ResolveComposite(users, "gateway_specific_identities:slack:bob@example.org")
	if !ok || assignment.Roles[0] != "sam_user" {
		t.Errorf("expected composite fallback match: ok=%v assignment=%v", ok, assignment)
	}
}

func TestEmailFoldIndex_CompositeFallbackRequiresEmailShape(t *testing.T) {
	users := map[string]UserAssignment{
		"gateway_specific_identities:gw-1:Alice": {Roles: []string{"sam_user"}},
	}
	idx := BuildEmailFoldIndex(users)

	if _, ok := idx.ResolveComposite(users, "gateway_specific_identities:gw-1:Alice"); !ok {
		t.Error("the composite's own exact case must still resolve")
	}
	if _, ok := idx.ResolveComposite(users, "gateway_specific_identities:gw-1:ALICE"); ok {
		t.Error("a plain-username composite (not email-shaped) must not be case-folded")
	}
}

func TestEmailFoldIndex_CollisionResolvesToSortedFirstDeterministically(t *testing.T) {
	users := map[string]UserAssignment{
		"Alice@example.org": {Roles: []string{"sam_admin"}},
		"alice@example.org": {Roles: []string{"sam_user"}},
	}
	idx := BuildEmailFoldIndex(users)

	if a, ok := idx.ResolveBare(users, "Alice@example.org"); !ok || a.Roles[0] != "sam_admin" {
		t.Errorf("exact match for Alice@example.org = %v, %v", a, ok)
	}
	if a, ok := idx.ResolveBare(users, "alice@example.org"); !ok || a.Roles[0] != "sam_user" {
		t.Errorf("exact match for alice@example.org = %v, %v", a, ok)
	}

	a, ok := idx.ResolveBare(users, "ALICE@EXAMPLE.ORG")
	if !ok || a.Roles[0] != "sam_admin" {
		t.Errorf("fallback for a third case variant = %v, %v, want the sorted-first (sam_admin) variant", a, ok)
	}
}

func TestEmailFoldIndex_PaddedCompositeKeyStillReachable(t *testing.T) {
	users := map[string]UserAssignment{
		" gateway_specific_identities:gw-1:Alice@example.org ": {Roles: []string{"sam_user"}},
	}
	idx := BuildEmailFoldIndex(users)

	if _, ok := idx.ResolveComposite(users, "gateway_specific_identities:gw-1:alice@example.org"); !ok {
		t.Error("a padded composite key must still be reachable via the composite fallback")
	}
	if _, ok := idx.ResolveBare(users, "gateway_specific_identities:gw-1:alice@example.org"); ok {
		t.Error("a padded composite key must not leak into the bare bucket")
	}
}

func TestEmailFoldIndex_PaddedSystemPrincipalStillExcluded(t *testing.T) {
	users := map[string]UserAssignment{
		" system:bob@corp.example ": {Roles: []string{"system_agent_invoke"}},
	}
	idx := BuildEmailFoldIndex(users)

	if _, ok := idx.ResolveBare(users, "system:BOB@corp.example"); ok {
		t.Error("a padded system: principal must still be excluded from case-folding")
	}
}
