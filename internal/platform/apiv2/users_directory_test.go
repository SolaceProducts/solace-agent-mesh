// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

var usersBudgetReader = []string{rbac.ScopeRBACRead, rbac.ScopeBudgetRead}

type usersBudgetFixture struct {
	api                    *entrypointAPI
	alice, bob, carol      string
	engineering, adminRole string
	budgets                *store.BudgetsRepo
	subjects               *entplatform.BudgetAssignmentService
	composer               *rbac.Composer
}

func newUsersBudgetFixture(t *testing.T) usersBudgetFixture {
	t.Helper()
	core, deps := newTestCoreWithConfig(t, nil)
	api := &entrypointAPI{apiClient: newAPIClient(t, core)}
	db := core.DB()
	f := usersBudgetFixture{
		api:      api,
		composer: deps.RBACComposer,
		alice:    api.seedUser(userSeed{sub: "alice", displayName: "Alice", email: "alice@example.com"}),
		bob:      api.seedUser(userSeed{sub: "bob", displayName: "Bob", email: "bob@example.com"}),
		carol:    api.seedUser(userSeed{sub: "carol", displayName: "Carol", email: "carol@example.com"}),
		budgets:  store.NewBudgetsRepo(db),
	}
	assignments := store.NewBudgetAssignmentsRepo(db)
	f.subjects = entplatform.NewBudgetAssignmentService(assignments, f.budgets, store.NewUsersRepo(db), nil, nil, slog.Default())

	limit := budgetAmount(t, "100.00")
	eng := &store.Budget{Name: "Engineering", SubjectType: store.BudgetSubjectUser, Amount: &limit, CreatedBy: "x", UpdatedBy: "x"}
	if err := f.budgets.Create(t.Context(), eng); err != nil {
		t.Fatal(err)
	}
	f.engineering = eng.ID
	for user, budgetID := range map[string]string{f.alice: f.engineering, f.bob: store.SeededDefaultUserBudgetID} {
		if _, err := assignments.Create(t.Context(), &store.BudgetAssignment{SubjectType: store.BudgetSubjectUser, SubjectRef: user, BudgetID: budgetID, CreatedBy: "x", UpdatedBy: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	f.adminRole = api.createRole("budget-filter-role").ID
	f.grantAdminRole(f.alice)
	return f
}

func (f usersBudgetFixture) grantAdminRole(userID string) {
	f.api.t.Helper()
	expectStatus(f.api.t, f.api.replaceUserRoleGrants(userID, append(f.api.userRoleGrants(userID), f.adminRole)...), http.StatusOK)
}

func (f usersBudgetFixture) listedIDs(query string) []string {
	f.api.t.Helper()
	rec := f.api.call(http.MethodGet, "/users?"+query, nil, usersBudgetReader...)
	expectStatus(f.api.t, rec, http.StatusOK)
	page := decodeList[User](f.api.t, rec)
	ids := make([]string, len(page.Data))
	for i, u := range page.Data {
		ids[i] = u.ID
	}
	return ids
}

func (f usersBudgetFixture) expandedBudgets(query string) map[string]string {
	f.api.t.Helper()
	rec := f.api.call(http.MethodGet, "/users?expand=budget&"+query, nil, usersBudgetReader...)
	expectStatus(f.api.t, rec, http.StatusOK)
	out := map[string]string{}
	for _, u := range decodeList[User](f.api.t, rec).Data {
		out[u.ID] = usersBudgetID(u)
	}
	return out
}

func (f usersBudgetFixture) expandedUser(id string) User {
	f.api.t.Helper()
	rec := f.api.call(http.MethodGet, "/users/"+id+"?expand=budget", nil, usersBudgetReader...)
	expectStatus(f.api.t, rec, http.StatusOK)
	return decodeData[User](f.api.t, rec)
}

func usersBudgetID(u User) string {
	if u.Budget == nil {
		return ""
	}
	return u.Budget.ID
}

func (f usersBudgetFixture) subjectBudgets(t *testing.T) map[string]entplatform.BudgetSubjectBudget {
	t.Helper()
	resp, err := f.subjects.ListSubjects(t.Context(), entplatform.ListBudgetSubjectsParams{SubjectType: store.BudgetSubjectUser, PageNumber: 1, PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Meta.Pagination.Count != len(resp.Data) {
		t.Fatalf("budget subjects count %d over %d rows; the fixture must fit one page", resp.Meta.Pagination.Count, len(resp.Data))
	}
	out := map[string]entplatform.BudgetSubjectBudget{}
	for _, s := range resp.Data {
		if s.Budget == nil {
			t.Fatalf("subject %s has no budget; the user tier always has a default", s.ID)
		}
		out[s.ID] = *s.Budget
	}
	return out
}

func TestUsers_BudgetFilterListsWhomTheBudgetApplies(t *testing.T) {
	f := newUsersBudgetFixture(t)
	if got := f.listedIDs("pageSize=100&budgetId=" + f.engineering); !slices.Equal(got, []string{f.alice}) {
		t.Errorf("Engineering = %v, want only alice", got)
	}
	onDefault := f.listedIDs("pageSize=100&budgetId=" + store.SeededDefaultUserBudgetID)
	if !slices.Contains(onDefault, f.bob) || !slices.Contains(onDefault, f.carol) || slices.Contains(onDefault, f.alice) {
		t.Errorf("default = %v, want bob and carol, not alice", onDefault)
	}
	if got := f.listedIDs("budgetId=" + store.SeededMeshBudgetID); len(got) != 0 {
		t.Errorf("the mesh budget = %v, want an empty page", got)
	}
}

func TestUsers_BudgetAndRoleFiltersCompose(t *testing.T) {
	f := newUsersBudgetFixture(t)
	f.grantAdminRole(f.carol)
	if got := f.listedIDs("roleId=" + f.adminRole + "&budgetId=" + f.engineering); !slices.Equal(got, []string{f.alice}) {
		t.Errorf("role and Engineering = %v, want only alice", got)
	}
	if got := f.listedIDs("roleId=" + f.adminRole + "&budgetId=" + store.SeededDefaultUserBudgetID); !slices.Equal(got, []string{f.carol}) {
		t.Errorf("role and the default = %v, want only carol", got)
	}
}

func TestUsers_BudgetFilterAgreesWithBudgetSubjects(t *testing.T) {
	f := newUsersBudgetFixture(t)
	ctx := t.Context()
	system := &store.User{Issuer: rbac.SystemUserIssuer, Sub: "budget-consistency-bot", ProvisionedVia: rbac.ProvisionedViaSystem, CreatedBy: "x", UpdatedBy: "x"}
	if err := store.NewUsersRepo(f.api.core.DB()).UpsertUser(ctx, system, nil); err != nil {
		t.Fatal(err)
	}
	ops := &store.Budget{Name: "Operations", SubjectType: store.BudgetSubjectUser, CreatedBy: "x", UpdatedBy: "x"}
	if err := f.budgets.Create(ctx, ops); err != nil {
		t.Fatal(err)
	}
	rbacRepo := store.NewRBACRepo(f.api.core.DB())
	for id := range f.subjectBudgets(t) {
		if id == f.alice {
			continue
		}
		if err := rbacRepo.AddUserRole(ctx, id, f.adminRole, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.composer.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}

	agree := func(t *testing.T) map[string][]string {
		t.Helper()
		onBudget := map[string][]string{}
		for id, b := range f.subjectBudgets(t) {
			onBudget[b.ID] = append(onBudget[b.ID], id)
		}
		for _, budgetID := range []string{f.engineering, store.SeededDefaultUserBudgetID, ops.ID} {
			want := slices.Sorted(slices.Values(onBudget[budgetID]))
			for _, query := range []string{"budgetId=" + budgetID, "roleId=" + f.adminRole + "&budgetId=" + budgetID} {
				if got := slices.Sorted(slices.Values(f.listedIDs(query + "&pageSize=100"))); !slices.Equal(got, want) {
					t.Errorf("GET /users?%s = %v, want the budget subjects on it %v", query, got, want)
				}
			}
		}
		onExpanded := map[string]string{}
		for _, query := range []string{"pageSize=100", "roleId=" + f.adminRole + "&pageSize=100"} {
			for id, budgetID := range f.expandedBudgets(query) {
				onExpanded[id] = budgetID
				if single := usersBudgetID(f.expandedUser(id)); single != budgetID {
					t.Errorf("GET /users/%s?expand=budget = %q, but the list says %q", id, single, budgetID)
				}
			}
		}
		for budgetID, ids := range onBudget {
			for _, id := range ids {
				if onExpanded[id] != budgetID {
					t.Errorf("user %s expands to budget %q, want %q as the budget subjects say", id, onExpanded[id], budgetID)
				}
			}
		}
		return onBudget
	}

	before := agree(t)
	if !slices.Contains(before[store.SeededDefaultUserBudgetID], f.carol) || !slices.Contains(before[store.SeededDefaultUserBudgetID], system.ID) {
		t.Fatalf("before: the default's subjects %v, want the unassigned carol and system user on it", before[store.SeededDefaultUserBudgetID])
	}
	if _, _, err := f.budgets.SetDefault(ctx, f.engineering, "x", time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	after := agree(t)
	for _, unassigned := range []string{f.carol, system.ID} {
		if !slices.Contains(after[f.engineering], unassigned) {
			t.Errorf("after: %s is not on the new default %v", unassigned, after[f.engineering])
		}
	}
	if !slices.Equal(after[store.SeededDefaultUserBudgetID], []string{f.bob}) {
		t.Errorf("after: the old default's subjects = %v, want only bob, who is pinned to it", after[store.SeededDefaultUserBudgetID])
	}
}

func TestUsers_ExpandedBudgetResolvesEachPage(t *testing.T) {
	f := newUsersBudgetFixture(t)
	want := f.subjectBudgets(t)
	for page := 1; page <= len(want); page++ {
		got := f.expandedBudgets(fmt.Sprintf("pageSize=1&pageNumber=%d", page))
		if len(got) != 1 {
			t.Fatalf("page %d holds %d users, want 1", page, len(got))
		}
		for id, budgetID := range got {
			if w, ok := want[id]; !ok || budgetID != w.ID {
				t.Errorf("page %d: %s is on %q, want %+v as the budget subjects have it", page, id, budgetID, w)
			}
		}
	}
	for _, path := range []string{"/users", "/users/" + f.alice} {
		expectDetail(t, f.api.call(http.MethodGet, path+"?expand=budget,roles", nil, usersBudgetReader...), http.StatusUnprocessableEntity, "expand")
	}
}

func TestUsers_UnlimitedDefaultBudgetHasNoEffectiveAmount(t *testing.T) {
	f := newUsersBudgetFixture(t)
	listed := map[string]User{}
	rec := f.api.call(http.MethodGet, "/users?expand=budget&pageSize=100", nil, usersBudgetReader...)
	expectStatus(t, rec, http.StatusOK)
	for _, u := range decodeList[User](t, rec).Data {
		listed[u.ID] = u
	}
	for name, carol := range map[string]User{"list": listed[f.carol], "single": f.expandedUser(f.carol)} {
		if carol.Budget == nil || carol.Budget.ID != store.SeededDefaultUserBudgetID || carol.Budget.Amount != nil {
			t.Errorf("%s: carol's budget = %+v, want the unlimited default", name, carol.Budget)
		}
		if carol.BudgetTopUp != nil || carol.EffectiveBudgetAmount != nil {
			t.Errorf("%s: carol = top-up %+v, effective %q; want neither", name, carol.BudgetTopUp, orEmpty(carol.EffectiveBudgetAmount))
		}
	}
}

func TestUsers_FailedBudgetLookupIs500AndLogsTheCause(t *testing.T) {
	logs := captureLogs(t)
	api := newRBACAPI(t)
	userID := api.seedUser(userSeed{sub: "u-lookup", email: "lookup@example.com"})
	if _, err := api.core.DB().ExecContext(t.Context(), "DROP TABLE budget_assignments"); err != nil {
		t.Fatal(err)
	}

	rec := api.call(http.MethodGet, "/users/"+userID+"?expand=budget", nil, usersBudgetReader...)
	expectStatus(t, rec, http.StatusInternalServerError)
	if strings.Contains(rec.Body.String(), "budget_assignments") {
		t.Errorf("500 body leaks the cause: %s", rec.Body.String())
	}
	if !strings.Contains(logs.String(), "budget_assignments") {
		t.Error("the failed lookup's cause is not logged")
	}
}

func TestUsers_EmailSortPlacesSystemUsersByTheirEmptyEmail(t *testing.T) {
	api := newRBACAPI(t)
	for _, s := range []struct{ sub, email string }{{"s1", "carol@example.com"}, {"s2", "alice@example.com"}, {"s3", "bob@example.com"}} {
		api.seedUser(userSeed{sub: s.sub, displayName: "User " + s.sub, email: s.email})
	}
	list := func(query string) []User {
		t.Helper()
		rec := api.call(http.MethodGet, "/users?pageSize=100&"+query, nil)
		expectStatus(t, rec, http.StatusOK)
		return decodeList[User](t, rec).Data
	}
	isSystem := func(u User) bool { return u.ProvisionedVia == rbac.ProvisionedViaSystem }
	emails := func(users []User) []string {
		var out []string
		for _, u := range users {
			if !isSystem(u) {
				out = append(out, orEmpty(u.Email))
			}
		}
		return out
	}

	asc := list("sort=email:asc")
	if got := emails(asc); !slices.Equal(got, []string{"alice@example.com", "bob@example.com", "carol@example.com"}) {
		t.Errorf("email asc = %v", got)
	}
	if first := slices.IndexFunc(asc, func(u User) bool { return !isSystem(u) }); first == 0 || slices.ContainsFunc(asc[first:], isSystem) {
		t.Errorf("email asc does not lead with the system users: %v", userDisplayNames(asc))
	}
	desc := list("sort=email:desc")
	if got := emails(desc); !slices.Equal(got, []string{"carol@example.com", "bob@example.com", "alice@example.com"}) {
		t.Errorf("email desc = %v", got)
	}
	if last := slices.IndexFunc(desc, isSystem); last < 0 || slices.ContainsFunc(desc[last:], func(u User) bool { return !isSystem(u) }) {
		t.Errorf("email desc does not end with the system users: %v", userDisplayNames(desc))
	}
	if byName := list("sort=displayName:asc"); len(byName) == 0 || !isSystem(byName[0]) {
		t.Errorf("displayName asc = %v, want a system user first", userDisplayNames(byName))
	}
}
