// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"strconv"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

func (a *budgetAPI) assign(subjectID, budgetID string) BudgetAssignment {
	a.t.Helper()
	rec := a.call(http.MethodPost, "/budgetAssignments", map[string]any{"subjectType": "user", "subjectId": subjectID, "budgetId": budgetID})
	expectStatus(a.t, rec, http.StatusCreated)
	return decodeData[BudgetAssignment](a.t, rec)
}

func TestBudgetAssignments_Lifecycle(t *testing.T) {
	api := newBudgetAPI(t)
	team := api.createBudget("Zq team", "user", "100")
	other := api.createBudget("Zq other", "user", "50")
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")

	rec := api.call(http.MethodPost, "/budgetAssignments", map[string]any{"subjectType": "user", "subjectId": jane, "budgetId": team.ID})
	expectStatus(t, rec, http.StatusCreated)
	concrete := decodeData[BudgetAssignment](t, rec)
	expectLocation(t, rec, "/budgetAssignments/"+concrete.ID)
	if concrete.Type != "budgetAssignment" || concrete.State != "concrete" || concrete.Subject.ID == nil || *concrete.Subject.ID != jane ||
		concrete.Subject.User == nil || concrete.Budget.ID != team.ID || concrete.Budget.Name != "Zq team" {
		t.Errorf("concrete assignment = %+v", concrete)
	}
	raw := rawData(t, api.call(http.MethodGet, "/budgetAssignments/"+concrete.ID, nil))
	expectISOTimes(t, raw, "createdTime", "updatedTime")
	subject, _ := raw["subject"].(map[string]any)
	expectKeys(t, "subject", subject, "type", "id", "displayName", "user", "identity")
	user, _ := subject["user"].(map[string]any)
	expectKeys(t, "subject.user", user, "email", "isSystemUser", "isServiceAccount", "emailShared", "sub", "issuer")
	for _, key := range []string{"emailShared", "sub", "issuer"} {
		if user[key] != nil {
			t.Errorf("subject.user.%s = %v without expand, want null", key, user[key])
		}
	}
	if subject["identity"] != nil {
		t.Errorf("subject.identity = %v without expand, want null", subject["identity"])
	}

	rec = api.call(http.MethodPost, "/budgetAssignments", map[string]any{
		"subjectType": "user", "identity": map[string]any{"type": "email", "value": "later.zq@example.com"}, "budgetId": team.ID,
	})
	expectStatus(t, rec, http.StatusCreated)
	pending := decodeData[BudgetAssignment](t, rec)
	if pending.State != "pending" || pending.Subject.ID != nil || pending.Subject.User != nil {
		t.Errorf("pending assignment = %+v, want no subject id or user", pending)
	}
	if got := decodeData[Budget](t, api.call(http.MethodGet, "/budgets/"+team.ID, nil)); got.AssignedCount != 1 || got.PendingCount != 1 {
		t.Errorf("team counts = %d assigned, %d pending; want 1 and 1", got.AssignedCount, got.PendingCount)
	}

	rec = api.call(http.MethodPatch, "/budgetAssignments/"+concrete.ID, map[string]any{"budgetId": other.ID})
	expectStatus(t, rec, http.StatusOK)
	if moved := decodeData[BudgetAssignment](t, rec); moved.ID != concrete.ID || moved.Budget.ID != other.ID {
		t.Errorf("moved assignment = %+v, want %s on %s", moved, concrete.ID, other.ID)
	}
	rec = api.call(http.MethodPatch, "/budgetAssignments/"+concrete.ID, map[string]any{})
	expectStatus(t, rec, http.StatusOK)
	if same := decodeData[BudgetAssignment](t, rec); same.Budget.ID != other.ID {
		t.Errorf("an empty patch moved the assignment to %s", same.Budget.ID)
	}
	rec = api.call(http.MethodPatch, "/budgetAssignments/"+concrete.ID, map[string]any{"budgetId": nil, "subjectId": jane, "subjectType": "user"})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if body := decodeError(t, rec); len(body.ValidationDetails["budgetId"]) == 0 || len(body.ValidationDetails["subjectId"]) == 0 || len(body.ValidationDetails["subjectType"]) == 0 {
		t.Errorf("patch with three problems has details %v", body.ValidationDetails)
	}
	expectDetail(t, api.call(http.MethodPatch, "/budgetAssignments/"+concrete.ID, map[string]any{"budgetId": store.SeededMeshBudgetID}), http.StatusUnprocessableEntity, "budgetId")

	expectStatus(t, api.call(http.MethodDelete, "/budgetAssignments/"+concrete.ID, nil), http.StatusNoContent)
	expectStatus(t, api.call(http.MethodGet, "/budgetAssignments/"+concrete.ID, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodDelete, "/budgetAssignments/"+concrete.ID, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodDelete, "/budgetAssignments/"+pending.ID, nil), http.StatusNoContent)
}

func TestBudgetAssignments_UpdateRerecordsIdentity(t *testing.T) {
	api := newBudgetAPI(t)
	team := api.createBudget("Zq team", "user", "100")
	other := api.createBudget("Zq other", "user", "50")
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	api.seedUser("zq-bob", "Bob Zq", "bob.zq@example.com")
	a := api.assign(jane, team.ID)
	identityOf := func() *BudgetAssignmentIdentity {
		t.Helper()
		return decodeData[BudgetAssignment](t, api.call(http.MethodGet, "/budgetAssignments/"+a.ID+"?expand=identity", nil)).Subject.Identity
	}

	rec := api.call(http.MethodPatch, "/budgetAssignments/"+a.ID, map[string]any{
		"identity": map[string]any{"type": "sub", "value": "zq-jane", "issuer": "https://idp.example.com"},
	})
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[BudgetAssignment](t, rec); got.Budget.ID != team.ID || got.Subject.ID == nil || *got.Subject.ID != jane {
		t.Errorf("re-recorded assignment = %+v, want jane's, still on %s", got, team.ID)
	}
	if i := identityOf(); i == nil || i.Type != "sub" || i.Value != "zq-jane" || i.Issuer == nil || *i.Issuer != "https://idp.example.com" {
		t.Errorf("identity after re-record = %+v, want jane's sub", i)
	}

	rec = api.call(http.MethodPatch, "/budgetAssignments/"+a.ID, map[string]any{
		"budgetId": other.ID, "identity": map[string]any{"type": "email", "value": "JANE.zq@example.com"},
	})
	expectStatus(t, rec, http.StatusOK)
	if got := decodeData[BudgetAssignment](t, rec); got.Budget.ID != other.ID {
		t.Errorf("move with re-record left the assignment on %s, want %s", got.Budget.ID, other.ID)
	}
	if i := identityOf(); i == nil || i.Type != "email" || i.Value != "jane.zq@example.com" || i.Issuer != nil {
		t.Errorf("identity after move = %+v, want the normalised email", i)
	}

	expectDetail(t, api.call(http.MethodPatch, "/budgetAssignments/"+a.ID, map[string]any{
		"identity": map[string]any{"type": "email", "value": "bob.zq@example.com"},
	}), http.StatusUnprocessableEntity, "identity.value")
	expectDetail(t, api.call(http.MethodPatch, "/budgetAssignments/"+a.ID, map[string]any{"identity": nil}), http.StatusUnprocessableEntity, "identity")
	if i := identityOf(); i == nil || i.Value != "jane.zq@example.com" {
		t.Errorf("identity after refused re-records = %+v, want it unchanged", i)
	}

	rec = api.call(http.MethodPost, "/budgetAssignments", map[string]any{
		"subjectType": "user", "identity": map[string]any{"type": "email", "value": "later.zq@example.com"}, "budgetId": team.ID,
	})
	pending := expectCreated[BudgetAssignment](t, rec, "budgetAssignments")
	expectDetail(t, api.call(http.MethodPatch, "/budgetAssignments/"+pending.ID, map[string]any{
		"identity": map[string]any{"type": "email", "value": "sooner.zq@example.com"},
	}), http.StatusUnprocessableEntity, "identity")
}

func TestBudgetAssignments_CreateValidation(t *testing.T) {
	api := newBudgetAPI(t)
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	rec := api.call(http.MethodPost, "/budgetAssignments", map[string]any{
		"subjectType": "mesh", "subjectId": jane, "identity": map[string]any{"type": "email", "value": "x@example.com"},
	})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	body := decodeError(t, rec)
	for _, field := range []string{"subjectType", "identity", "budgetId"} {
		if len(body.ValidationDetails[field]) == 0 {
			t.Errorf("create has nothing for %s: %v", field, body.ValidationDetails)
		}
	}
	expectDetail(t, api.call(http.MethodPost, "/budgetAssignments", map[string]any{"subjectType": "user", "subjectId": jane, "budgetId": store.SeededDefaultUserBudgetID, "state": "pending"}), http.StatusBadRequest, "state")
}

func TestBudgetAssignments_ExpandIdentityNeedsRBACRead(t *testing.T) {
	api := newBudgetAPI(t)
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	a := api.assign(jane, store.SeededDefaultUserBudgetID)
	budgetOnly := api.as(rbac.ScopeBudgetRead)
	expectMessage(t, budgetOnly.call(http.MethodGet, "/budgetAssignments?expand=identity", nil), http.StatusForbidden, msgNoPermission)
	expectMessage(t, budgetOnly.call(http.MethodGet, "/budgetAssignments/"+a.ID+"?expand=identity", nil), http.StatusForbidden, msgNoPermission)
	expectDetail(t, budgetOnly.call(http.MethodGet, "/budgetAssignments?expand=budget", nil), http.StatusUnprocessableEntity, "expand")

	both := api.as(rbac.ScopeBudgetRead, rbac.ScopeRBACRead)
	got := decodeData[BudgetAssignment](t, both.call(http.MethodGet, "/budgetAssignments/"+a.ID+"?expand=identity", nil))
	u := got.Subject.User
	if u == nil || u.Sub == nil || *u.Sub != "zq-jane" || u.Issuer == nil || *u.Issuer != "https://idp.example.com" || u.EmailShared == nil || *u.EmailShared {
		t.Errorf("expanded user = %+v", u)
	}
	if i := got.Subject.Identity; i == nil || i.Type != "email" || i.Value != "jane.zq@example.com" || i.Issuer != nil {
		t.Errorf("expanded identity = %+v, want the email with a null issuer", i)
	}
	page := decodeList[BudgetAssignment](t, both.call(http.MethodGet, "/budgetAssignments?expand=identity&subjectId="+jane, nil))
	if len(page.Data) != 1 || page.Data[0].Subject.Identity == nil {
		t.Errorf("expanded list = %+v", page.Data)
	}
}

func TestBudgetAssignments_WritesNeedAssignAndRead(t *testing.T) {
	api := newBudgetAPI(t)
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	team := api.createBudget("Zq team", "user", "100")
	a := api.assign(jane, store.SeededDefaultUserBudgetID)
	for _, scopes := range [][]string{{rbac.ScopeBudgetAssign}, {rbac.ScopeBudgetRead}} {
		caller := api.as(scopes...)
		for _, write := range []struct {
			method, path string
			body         any
		}{
			{http.MethodPost, "/budgetAssignments", map[string]any{"subjectType": "user", "subjectId": jane, "budgetId": team.ID}},
			{http.MethodPatch, "/budgetAssignments/" + a.ID, map[string]any{"budgetId": team.ID}},
			{http.MethodDelete, "/budgetAssignments/" + a.ID, nil},
			{http.MethodPut, "/budgets/" + team.ID + "/updateAssignments", map[string]any{"subjectType": "user", "add": []string{jane}}},
		} {
			if rec := caller.call(write.method, write.path, write.body); rec.Code != http.StatusForbidden {
				t.Errorf("%s %s with %v = %d, want 403", write.method, write.path, scopes, rec.Code)
			}
		}
	}
	both := api.as(rbac.ScopeBudgetAssign, rbac.ScopeBudgetRead)
	expectStatus(t, both.call(http.MethodPatch, "/budgetAssignments/"+a.ID, map[string]any{"budgetId": team.ID}), http.StatusOK)
	expectStatus(t, both.call(http.MethodDelete, "/budgetAssignments/"+a.ID, nil), http.StatusNoContent)
}

func TestBudgetAssignments_UpdateAssignments(t *testing.T) {
	api := newBudgetAPI(t)
	team := api.createBudget("Zq team", "user", "100")
	other := api.createBudget("Zq other", "user", "50")
	newcomer := api.seedUser("zq-new", "New Zq", "new.zq@example.com")
	mover := api.seedUser("zq-mover", "Mover Zq", "mover.zq@example.com")
	stayer := api.seedUser("zq-stayer", "Stayer Zq", "stayer.zq@example.com")
	leaver := api.seedUser("zq-leaver", "Leaver Zq", "leaver.zq@example.com")
	stranger := api.seedUser("zq-stranger", "Stranger Zq", "stranger.zq@example.com")
	moved := api.assign(mover, other.ID)
	api.assign(stayer, team.ID)
	left := api.assign(leaver, team.ID)

	rec := api.call(http.MethodPut, "/budgets/"+team.ID+"/updateAssignments", map[string]any{
		"subjectType": "user", "add": []string{newcomer, mover, stayer}, "remove": []string{leaver, stranger},
	})
	expectStatus(t, rec, http.StatusOK)
	changes := decodeData[BudgetAssignmentChanges](t, rec)
	if len(changes.Added) != 1 || changes.Added[0].Subject.ID != newcomer {
		t.Errorf("added = %+v", changes.Added)
	}
	if len(changes.Moved) != 1 || changes.Moved[0].AssignmentID != moved.ID || changes.Moved[0].FromBudget.ID != other.ID || changes.Moved[0].FromBudget.Name != "Zq other" {
		t.Errorf("moved = %+v", changes.Moved)
	}
	if len(changes.Removed) != 1 || changes.Removed[0].AssignmentID != left.ID {
		t.Errorf("removed = %+v", changes.Removed)
	}
	reasons := map[string]string{}
	for _, u := range changes.Unchanged {
		reasons[u.Subject.ID] = u.Reason
		if u.Subject.ID == stranger && u.AssignmentID != nil {
			t.Errorf("an unassigned subject has assignment id %s", *u.AssignmentID)
		}
	}
	if reasons[stayer] != "already_assigned" || reasons[stranger] != "not_assigned" {
		t.Errorf("unchanged reasons = %v", reasons)
	}

	rec = api.call(http.MethodPut, "/budgets/"+team.ID+"/updateAssignments", map[string]any{"subjectType": "user"})
	expectStatus(t, rec, http.StatusOK)
	raw := rawData(t, rec)
	for _, key := range []string{"added", "moved", "removed", "unchanged"} {
		if list, ok := raw[key].([]any); !ok || len(list) != 0 {
			t.Errorf("an empty save sends %s = %v, want []", key, raw[key])
		}
	}

	tooMany := make([]string, 1001)
	for i := range tooMany {
		tooMany[i] = newcomer
	}
	expectDetail(t, api.call(http.MethodPut, "/budgets/"+team.ID+"/updateAssignments", map[string]any{"subjectType": "user", "add": tooMany}), http.StatusUnprocessableEntity, "add")
	expectDetail(t, api.call(http.MethodPut, "/budgets/"+team.ID+"/updateAssignments", map[string]any{"subjectType": "user", "add": []string{"nope"}}), http.StatusUnprocessableEntity, "add[0]")
	expectStatus(t, api.call(http.MethodPut, "/budgets/"+missingID+"/updateAssignments", map[string]any{"subjectType": "user"}), http.StatusNotFound)
}

func TestBudgetAssignments_Filters(t *testing.T) {
	api := newBudgetAPI(t)
	team := api.createBudget("Zq team", "user", "100")
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	bob := api.seedUser("zq-bob", "Bob Zq", "bob.zq@example.com")
	api.assign(jane, team.ID)
	api.assign(bob, store.SeededDefaultUserBudgetID)

	page := decodeList[BudgetAssignment](t, api.call(http.MethodGet, "/budgetAssignments?budgetId="+team.ID, nil))
	if len(page.Data) != 1 || *page.Data[0].Subject.ID != jane {
		t.Errorf("team's assignments = %+v", page.Data)
	}
	page = decodeList[BudgetAssignment](t, api.call(http.MethodGet, "/budgetAssignments?search=bob&state=concrete&subjectType=user", nil))
	if len(page.Data) != 1 || *page.Data[0].Subject.ID != bob {
		t.Errorf("search bob = %+v", page.Data)
	}
	expectDetail(t, api.call(http.MethodGet, "/budgetAssignments?budgetId=nope", nil), http.StatusBadRequest, "budgetId")
	expectDetail(t, api.call(http.MethodGet, "/budgetAssignments?subjectId=nope", nil), http.StatusBadRequest, "subjectId")
	expectDetail(t, api.call(http.MethodGet, "/budgetAssignments?state=gone", nil), http.StatusUnprocessableEntity, "state")
	expectDetail(t, api.call(http.MethodGet, "/budgetAssignments?subjectType=mesh", nil), http.StatusUnprocessableEntity, "subjectType")
	expectDetail(t, api.call(http.MethodGet, "/budgetAssignments?state=", nil), http.StatusBadRequest, "state")
	expectDetail(t, api.call(http.MethodGet, "/budgetAssignments?budget=x", nil), http.StatusBadRequest, "budget")
}

func TestBudgetAssignments_HiddenUsersExcluded(t *testing.T) {
	hidden := rbac.UserListingHiddenIDs()
	if len(hidden) == 0 {
		t.Skip("no reserved system user is hidden from the user listings")
	}
	api := newBudgetAPI(t)
	team := api.createBudget("Zq team", "user", "100")
	a := api.assign(hidden[0], team.ID)
	page := decodeList[BudgetAssignment](t, api.call(http.MethodGet, "/budgetAssignments?budgetId="+team.ID, nil))
	if len(page.Data) != 0 {
		t.Errorf("a hidden user's assignment is listed: %+v", page.Data)
	}
	page = decodeList[BudgetAssignment](t, api.call(http.MethodGet, "/budgetAssignments?subjectId="+hidden[0], nil))
	if len(page.Data) != 1 || page.Data[0].ID != a.ID {
		t.Errorf("naming the hidden user lists %+v, want %s", page.Data, a.ID)
	}
	if got := decodeData[BudgetAssignment](t, api.call(http.MethodGet, "/budgetAssignments/"+a.ID, nil)); got.ID != a.ID {
		t.Errorf("GET the hidden user's assignment = %+v, want %s", got, a.ID)
	}
	if b := decodeData[Budget](t, api.call(http.MethodGet, "/budgets/"+team.ID, nil)); b.HiddenAssignedCount != 1 || b.AssignedCount != 0 {
		t.Errorf("team counts = %d assigned, %d hidden; want 0 and 1", b.AssignedCount, b.HiddenAssignedCount)
	}
}

func TestBudgetAssignments_ListNamesEveryBadQueryParam(t *testing.T) {
	api := newBudgetAPI(t)
	syntax := api.call(http.MethodGet, "/budgetAssignments?budgetId=x&subjectId=y", nil)
	expectDetail(t, syntax, http.StatusBadRequest, "budgetId")
	expectDetail(t, syntax, http.StatusBadRequest, "subjectId")
	semantic := api.call(http.MethodGet, "/budgetAssignments?state=bogus&expand=nope", nil)
	expectDetail(t, semantic, http.StatusUnprocessableEntity, "state")
	expectDetail(t, semantic, http.StatusUnprocessableEntity, "expand")
	expectStatus(t, api.call(http.MethodGet, "/budgetAssignments?budgetId=x&state=bogus", nil), http.StatusBadRequest)
}

func TestBudgetAssignments_Paging(t *testing.T) {
	api := newBudgetAPI(t)
	team := api.createBudget("Zq team", "user", "100")
	for i := range 21 {
		n := strconv.Itoa(100 + i)
		api.assign(api.seedUser("zq-paged-"+n, "Zq paged "+n, ""), team.ID)
	}
	listed := "/budgetAssignments?search=zq%20paged"
	page := decodeList[BudgetAssignment](t, api.call(http.MethodGet, listed, nil))
	if len(page.Data) != defaultPageSize || page.Meta.Pagination.Count != 21 {
		t.Errorf("default page = %d of %d, want %d of 21", len(page.Data), page.Meta.Pagination.Count, defaultPageSize)
	}
	if page := decodeList[BudgetAssignment](t, api.call(http.MethodGet, listed+"&pageSize=100", nil)); len(page.Data) != 21 {
		t.Errorf("one page of 100 has %d items, want 21", len(page.Data))
	}
	if page := decodeList[BudgetAssignment](t, api.call(http.MethodGet, listed+"&pageNumber=9", nil)); len(page.Data) != 0 || page.Meta.Pagination.Count != 21 {
		t.Errorf("a page past the end = %d items of %d, want none of 21", len(page.Data), page.Meta.Pagination.Count)
	}
	expectDetail(t, api.call(http.MethodGet, "/budgetAssignments?pageSize=101", nil), http.StatusUnprocessableEntity, "pageSize")
	expectDetail(t, api.call(http.MethodGet, "/budgetAssignments?pageSize=0", nil), http.StatusUnprocessableEntity, "pageSize")
	expectDetail(t, api.call(http.MethodGet, "/budgetAssignments?pageSize=ten", nil), http.StatusBadRequest, "pageSize")
}

func TestBudgetAssignments_UnknownIDs(t *testing.T) {
	api := newBudgetAPI(t)
	team := api.createBudget("Zq team", "user", "100")
	move := map[string]any{"budgetId": team.ID}
	expectDetail(t, api.call(http.MethodGet, "/budgetAssignments/not-a-uuid", nil), http.StatusBadRequest, "id")
	expectDetail(t, api.call(http.MethodPatch, "/budgetAssignments/not-a-uuid", move), http.StatusBadRequest, "id")
	expectDetail(t, api.call(http.MethodDelete, "/budgetAssignments/not-a-uuid", nil), http.StatusBadRequest, "id")
	expectStatus(t, api.call(http.MethodGet, "/budgetAssignments/"+missingID, nil), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodPatch, "/budgetAssignments/"+missingID, move), http.StatusNotFound)
	expectStatus(t, api.call(http.MethodDelete, "/budgetAssignments/"+missingID, nil), http.StatusNotFound)
}

func TestBudgetAssignments_CreateRefusesASecondAssignment(t *testing.T) {
	api := newBudgetAPI(t)
	team := api.createBudget("Zq team", "user", "100")
	other := api.createBudget("Zq other", "user", "50")
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	first := api.assign(jane, team.ID)

	expectMessage(t, api.call(http.MethodPost, "/budgetAssignments", map[string]any{"subjectType": "user", "subjectId": jane, "budgetId": other.ID}),
		http.StatusConflict, budgetConflictSentences[entplatform.BudgetConflictSubjectAssigned])
	if got := decodeData[BudgetAssignment](t, api.call(http.MethodGet, "/budgetAssignments/"+first.ID, nil)); got.Budget.ID != team.ID {
		t.Errorf("the refused create moved the assignment to %s", got.Budget.ID)
	}
}

func TestBudgetAssignments_RefusesKeysTheRequestDoesNotTake(t *testing.T) {
	api := newBudgetAPI(t)
	team := api.createBudget("Zq team", "user", "100")
	jane := api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com")
	a := api.assign(jane, team.ID)
	updateAssignments := "/budgets/" + team.ID + "/updateAssignments"

	for _, c := range []struct {
		name, method, path, body, field string
	}{
		{"the old subjectRef key", http.MethodPost, "/budgetAssignments",
			`{"subjectType":"user","subjectRef":"` + jane + `","budgetId":"` + team.ID + `"}`, "subjectRef"},
		{"an unknown identity key on create", http.MethodPost, "/budgetAssignments",
			`{"subjectType":"user","identity":{"type":"email","value":"x.zq@example.com","extra":1},"budgetId":"` + team.ID + `"}`, ""},
		{"an unknown identity key on update", http.MethodPatch, "/budgetAssignments/" + a.ID,
			`{"identity":{"type":"email","value":"jane.zq@example.com","extra":1}}`, ""},
		{"an unknown key on a save", http.MethodPut, updateAssignments, `{"subjectType":"user","replace":[]}`, "replace"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := api.call(c.method, c.path, c.body)
			if c.field != "" {
				expectDetail(t, rec, http.StatusBadRequest, c.field)
				return
			}
			expectStatus(t, rec, http.StatusBadRequest)
			if len(decodeError(t, rec).ValidationDetails) == 0 {
				t.Errorf("%s %s names no field: %s", c.method, c.path, rec.Body.String())
			}
		})
	}
	if got := decodeList[BudgetAssignment](t, api.call(http.MethodGet, "/budgetAssignments?budgetId="+team.ID, nil)); len(got.Data) != 1 {
		t.Errorf("refused requests left %d assignments on the team, want jane's alone", len(got.Data))
	}
}

func TestBudgetAssignments_GetRefusesAnUnknownExpansion(t *testing.T) {
	api := newBudgetAPI(t)
	a := api.assign(api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com"), store.SeededDefaultUserBudgetID)
	expectDetail(t, api.call(http.MethodGet, "/budgetAssignments/"+a.ID+"?expand=x", nil), http.StatusUnprocessableEntity, "expand")
}

func TestBudgetAssignments_ListRefusesFiltersThatWouldReadAsNone(t *testing.T) {
	api := newBudgetAPI(t)
	for _, name := range []string{"budgetId", "subjectId", "subjectType"} {
		expectDetail(t, api.call(http.MethodGet, "/budgetAssignments?"+name+"=", nil), http.StatusBadRequest, name)
	}
	expectDetail(t, api.call(http.MethodGet, "/budgetAssignments?budgetId=%zz", nil), http.StatusBadRequest, "query")
}

func TestBudgetAssignments_ListsPendingAlone(t *testing.T) {
	api := newBudgetAPI(t)
	team := api.createBudget("Zq team", "user", "100")
	api.assign(api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com"), team.ID)
	pending := expectCreated[BudgetAssignment](t, api.call(http.MethodPost, "/budgetAssignments", map[string]any{
		"subjectType": "user", "identity": map[string]any{"type": "email", "value": "later.zq@example.com"}, "budgetId": team.ID,
	}), "budgetAssignments")

	page := decodeList[BudgetAssignment](t, api.call(http.MethodGet, "/budgetAssignments?state=pending", nil))
	if len(page.Data) != 1 || page.Data[0].ID != pending.ID || page.Meta.Pagination.Count != 1 {
		t.Errorf("pending list = %+v, want %s alone", page.Data, pending.ID)
	}
}

func TestBudgetAssignments_SearchMatchesIdentitiesOnlyForRBACRead(t *testing.T) {
	api := newBudgetAPI(t)
	team := api.createBudget("Zq team", "user", "100")
	expectStatus(t, api.call(http.MethodPost, "/budgetAssignments", map[string]any{
		"subjectType": "user", "identity": map[string]any{"type": "email", "value": "new.hire@example.com"}, "budgetId": team.ID,
	}), http.StatusCreated)

	for _, c := range []struct {
		scopes []string
		want   int
	}{
		{[]string{rbac.ScopeBudgetRead}, 0},
		{[]string{rbac.ScopeBudgetRead, rbac.ScopeRBACRead}, 1},
	} {
		if page := decodeList[BudgetAssignment](t, api.as(c.scopes...).call(http.MethodGet, "/budgetAssignments?search=new.h", nil)); len(page.Data) != c.want {
			t.Errorf("search as %v found %d assignments, want %d", c.scopes, len(page.Data), c.want)
		}
	}
}

func TestBudgetAssignments_ReadsTakeBudgetReadAlone(t *testing.T) {
	api := newBudgetAPI(t)
	a := api.assign(api.seedUser("zq-jane", "Jane Zq", "jane.zq@example.com"), store.SeededDefaultUserBudgetID)
	for _, path := range []string{"/budgetAssignments", "/budgetAssignments/" + a.ID} {
		expectMessage(t, api.as(rbac.ScopeRBACRead).call(http.MethodGet, path, nil), http.StatusForbidden, msgNoPermission)
		expectStatus(t, api.as(rbac.ScopeBudgetRead).call(http.MethodGet, path, nil), http.StatusOK)
	}
}
