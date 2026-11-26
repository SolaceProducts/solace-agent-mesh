// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"slices"
	"strconv"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func TestBudgetSubjects_Paging(t *testing.T) {
	api := newBudgetAPI(t)
	for i := range 21 {
		n := strconv.Itoa(100 + i)
		api.seedUser("zq-paged-"+n, "Zq paged "+n, "")
	}
	page := decodeList[BudgetSubject](t, api.call(http.MethodGet, "/budgetSubjects?subjectType=user&search=zq%20paged", nil))
	if len(page.Data) != defaultPageSize || page.Meta.Pagination.Count != 21 {
		t.Errorf("default page = %d of %d, want %d of 21", len(page.Data), page.Meta.Pagination.Count, defaultPageSize)
	}
	page = decodeList[BudgetSubject](t, api.call(http.MethodGet, "/budgetSubjects?subjectType=user&search=zq%20paged&sort=displayName:desc&pageSize=100", nil))
	if len(page.Data) != 21 || *page.Data[0].DisplayName != "Zq paged 120" {
		t.Errorf("sorted desc starts with %v of %d", page.Data[0].DisplayName, len(page.Data))
	}
	first := page.Data[0]
	if first.Type != "budgetSubject" || first.SubjectType != "user" || first.User == nil || first.Budget == nil ||
		first.Budget.ID != store.SeededDefaultUserBudgetID || first.Budget.Source != "default" || first.TopUp != nil {
		t.Errorf("a user on the default = %+v", first)
	}
	if page := decodeList[BudgetSubject](t, api.call(http.MethodGet, "/budgetSubjects?subjectType=mesh&pageNumber=2", nil)); len(page.Data) != 0 || page.Meta.Pagination.Count != 1 {
		t.Errorf("mesh page 2 = %d items of %d, want none of 1", len(page.Data), page.Meta.Pagination.Count)
	}
	expectDetail(t, api.call(http.MethodGet, "/budgetSubjects?subjectType=user&sort=email:asc", nil), http.StatusUnprocessableEntity, "sort")
	expectDetail(t, api.call(http.MethodGet, "/budgetSubjects?subjectType=user&pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
	expectDetail(t, api.call(http.MethodGet, "/budgetSubjects?subjectType=user&pageSize=0", nil), http.StatusUnprocessableEntity, "pageSize")
	expectDetail(t, api.call(http.MethodGet, "/budgetSubjects?subjectType=user&pageSize=ten", nil), http.StatusBadRequest, "pageSize")
	expectDetail(t, api.call(http.MethodGet, "/budgetSubjects?subjectType=mesh&search=x", nil), http.StatusUnprocessableEntity, "search")
}

func TestBudgetSubjects_SubjectTypeRequired(t *testing.T) {
	api := newBudgetAPI(t)
	expectDetail(t, api.call(http.MethodGet, "/budgetSubjects", nil), http.StatusUnprocessableEntity, "subjectType")
	expectDetail(t, api.call(http.MethodGet, "/budgetSubjects?subjectType=agent", nil), http.StatusUnprocessableEntity, "subjectType")
	expectDetail(t, api.call(http.MethodGet, "/budgetSubjects?subjectType=", nil), http.StatusBadRequest, "subjectType")
}

func TestBudgetSubjects_EveryListedIDIsFetchable(t *testing.T) {
	api := newBudgetAPI(t)
	team := api.createBudget("Zq team", "user", "100")
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	a := api.assign(jane, team.ID)
	api.seedUser("zq-bob", "Bob Zq", "")

	listed := decodeList[BudgetSubject](t, api.call(http.MethodGet, "/budgetSubjects?subjectType=user&search=zq", nil)).Data
	listed = append(listed, decodeList[BudgetSubject](t, api.call(http.MethodGet, "/budgetSubjects?subjectType=mesh", nil)).Data...)
	if len(listed) != 3 {
		t.Fatalf("listed %d subjects, want jane, bob and the mesh", len(listed))
	}
	for _, s := range listed {
		got := decodeData[BudgetSubject](t, api.call(http.MethodGet, "/budgetSubjects/"+s.ID, nil))
		if got.ID != s.ID || got.SubjectType != s.SubjectType || (got.Budget == nil) != (s.Budget == nil) {
			t.Errorf("GET %s = %+v, want the listed %+v", s.ID, got, s)
		}
	}

	janeSubject := decodeData[BudgetSubject](t, api.call(http.MethodGet, "/budgetSubjects/"+jane, nil))
	if b := janeSubject.Budget; b == nil || b.ID != team.ID || b.Source != "assigned" || b.AssignmentID == nil || *b.AssignmentID != a.ID {
		t.Errorf("jane's budget = %+v, want the team through %s", b, a.ID)
	}
	mesh := rawData(t, api.call(http.MethodGet, "/budgetSubjects/mesh", nil))
	expectKeys(t, "mesh", mesh, "id", "type", "subjectType", "displayName", "user", "budget", "topUp", "effectiveAmount")
	if mesh["user"] != nil || mesh["displayName"] != nil || mesh["id"] != "mesh" {
		t.Errorf("mesh subject = %v, want id mesh and null user and displayName", mesh)
	}

	if hidden := rbac.UserListingHiddenIDs(); len(hidden) > 0 {
		expectStatus(t, api.call(http.MethodGet, "/budgetSubjects/"+hidden[0], nil), http.StatusOK)
	}
	expectStatus(t, api.call(http.MethodGet, "/budgetSubjects/"+missingID, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodGet, "/budgetSubjects/nobody", nil), http.StatusNotFound)
	expectDetail(t, api.call(http.MethodGet, "/budgetSubjects/Not%20An%20Id", nil), http.StatusBadRequest, "id")
}

func TestBudgetSubjects_RefusesWhatItDoesNotOffer(t *testing.T) {
	api := newBudgetAPI(t)
	expectDetail(t, api.call(http.MethodGet, "/budgetSubjects?subjectType=group", nil), http.StatusUnprocessableEntity, "subjectType")
	expectDetail(t, api.call(http.MethodGet, "/budgetSubjects?subjectType=user&sort=displayName:up", nil), http.StatusBadRequest, "sort")
}

func TestBudgetSubjects_ReadsTakeBudgetRead(t *testing.T) {
	api := newBudgetAPI(t)
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	for _, path := range []string{"/budgetSubjects?subjectType=user", "/budgetSubjects/" + jane, "/budgetSubjects/mesh"} {
		expectMessage(t, api.as(rbac.ScopeBudgetAssign, rbac.ScopeBudgetUpdate).call(http.MethodGet, path, nil), http.StatusForbidden, msgNoPermission)
		expectMessage(t, api.as(rbac.ScopeRBACRead).call(http.MethodGet, path, nil), http.StatusForbidden, msgNoPermission)
		expectStatus(t, api.as(rbac.ScopeBudgetRead).call(http.MethodGet, path, nil), http.StatusOK)
	}
}

func TestBudgetSubjects_SourceOfTheBudget(t *testing.T) {
	api := newBudgetAPI(t)
	api.setAmount(store.SeededDefaultUserBudgetID, "250")
	team := api.createBudget("Zq team", "user", "100")
	onTeam := api.seedUser("zq-team", "Zq Team", "")
	onDefault := api.seedUser("zq-default", "Zq Default", "")
	pinned := api.seedUser("zq-pinned", "Zq Pinned", "")
	teamAssignment := api.assign(onTeam, team.ID)
	pin := api.assign(pinned, store.SeededDefaultUserBudgetID)

	byID := map[string]BudgetSubject{}
	for _, s := range decodeList[BudgetSubject](t, api.call(http.MethodGet, "/budgetSubjects?subjectType=user&search=zq", nil)).Data {
		byID[s.ID] = s
	}
	for _, c := range []struct {
		id, budgetID, source, effective string
		assignmentID                    *string
	}{
		{onTeam, team.ID, "assigned", "100.00", &teamAssignment.ID},
		{onDefault, store.SeededDefaultUserBudgetID, "default", "250.00", nil},
		{pinned, store.SeededDefaultUserBudgetID, "assigned", "250.00", &pin.ID},
	} {
		got, ok := byID[c.id]
		if !ok || got.Budget == nil || got.EffectiveAmount == nil {
			t.Errorf("subject %s = %+v", c.id, got)
			continue
		}
		if got.Budget.ID != c.budgetID || got.Budget.Source != c.source || *got.EffectiveAmount != c.effective ||
			(c.assignmentID == nil) != (got.Budget.AssignmentID == nil) || (c.assignmentID != nil && *got.Budget.AssignmentID != *c.assignmentID) {
			t.Errorf("subject %s budget = %+v effective %s, want %s %s %s", c.id, got.Budget, *got.EffectiveAmount, c.budgetID, c.source, c.effective)
		}
	}
}

func TestBudgetSubjects_NoBudgetAtAll(t *testing.T) {
	api := newBudgetAPI(t)
	bob := api.seedUser("zq-bob", "Bob Zq", "")
	if _, err := api.core.DB().ExecContext(t.Context(), `UPDATE budgets SET is_default = ? WHERE subject_type = ?`, 0, store.BudgetSubjectUser); err != nil {
		t.Fatal(err)
	}
	listed := decodeList[map[string]any](t, api.call(http.MethodGet, "/budgetSubjects?subjectType=user&search=zq", nil)).Data
	if len(listed) != 1 || listed[0]["id"] != bob {
		t.Fatalf("listed = %v, want bob", listed)
	}
	expectNull(t, listed[0], "budget", "effectiveAmount", "topUp")
	expectNull(t, rawData(t, api.call(http.MethodGet, "/budgetSubjects/"+bob, nil)), "budget", "effectiveAmount", "topUp")
}

func TestBudgetSubjects_ListHidesWhatTheUsersListingHides(t *testing.T) {
	api := newBudgetAPI(t)
	page := decodeList[BudgetSubject](t, api.call(http.MethodGet, "/budgetSubjects?subjectType=user&pageSize=100", nil))
	ids := make([]string, len(page.Data))
	for i, s := range page.Data {
		ids[i] = s.ID
	}
	for _, hidden := range rbac.UserListingHiddenIDs() {
		if slices.Contains(ids, hidden) {
			t.Errorf("hidden system user %s is listed", hidden)
		}
	}
	if i := slices.Index(ids, rbac.DefaultSystemUserID); i < 0 || page.Data[i].User == nil || !page.Data[i].User.IsSystemUser || !page.Data[i].User.IsServiceAccount {
		t.Errorf("system:default must be listed with isSystemUser and isServiceAccount both true: %v", ids)
	}
	if page.Meta.Pagination.Count != len(ids) {
		t.Errorf("count = %d, want %d: a hidden user is left out of the total too", page.Meta.Pagination.Count, len(ids))
	}
}
