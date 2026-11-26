// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/codegen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/manifest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	meshID        = "00000000-0000-0000-0000-00000000b001"
	defaultUserID = "00000000-0000-0000-0000-00000000b002"
)

func TestBudgetSchemaDocumentsExactlyTheSpecKeysTheReconcilerAccepts(t *testing.T) {
	documented := make([]string, 0, len(codegen.BudgetSchema.Fields))
	for _, f := range codegen.BudgetSchema.Fields {
		documented = append(documented, f.JSONName)
	}
	slices.Sort(documented)
	accepted := slices.Sorted(slices.Values(budgetSpecAllowedKeys))
	if !slices.Equal(documented, accepted) {
		t.Errorf("BudgetSchema fields = %v, reconciler accepts %v", documented, accepted)
	}
}

func budgetFile(name string, spec map[string]any) resolve.ResolvedResource {
	return resolve.ResolvedResource{Kind: "budget", Name: name, SourceFile: "budgets/" + name + ".yaml", Spec: spec}
}

func userBudget(name string, amount any) resolve.ResolvedResource {
	return budgetFile(name, map[string]any{"subjectType": store.BudgetSubjectUser, "amount": amount})
}

func seededBudgets() []wire.BudgetResponse {
	return []wire.BudgetResponse{
		{ID: meshID, Name: "Agent Mesh", SubjectType: store.BudgetSubjectMesh, IsDefault: true},
		{ID: defaultUserID, Name: "Default User", SubjectType: store.BudgetSubjectUser, IsDefault: true},
	}
}

func withBudget(rows []wire.BudgetResponse, b wire.BudgetResponse) []wire.BudgetResponse {
	return append(rows, b)
}

func applyAllBudgets(ctx context.Context, c *Client, plan *budgetPlan, opts ApplyOptions) *budgetOutcome {
	out := applyBudgets(ctx, c, plan, opts)
	applyBudgetDeletes(ctx, c, plan, nil, opts, out)
	return out
}

func mustPlanBudgets(t *testing.T, desired []resolve.ResolvedResource, actual []wire.BudgetResponse) *budgetPlan {
	t.Helper()
	plan, err := planBudgets(desired, actual)
	if err != nil {
		t.Fatalf("planBudgets: %v", err)
	}
	return plan
}

func opNames(ops []budgetOp) []string {
	names := make([]string, len(ops))
	for i, op := range ops {
		names[i] = op.Name
	}
	return names
}

func wantOps(t *testing.T, label string, ops []budgetOp, want ...string) {
	t.Helper()
	if got := strings.Join(opNames(ops), ","); got != strings.Join(want, ",") {
		t.Errorf("%s = [%s], want [%s]", label, got, strings.Join(want, ","))
	}
}

func TestPlanBudgets_AmountsConvergeOnTheStoredForm(t *testing.T) {
	stored := withBudget(seededBudgets(), wire.BudgetResponse{ID: "e1", Name: "Engineering", SubjectType: store.BudgetSubjectUser, Amount: new("19.90")})
	for _, amount := range []any{"19.9", 19.9, "19.90", "+19.9"} {
		t.Run(fmt.Sprint(amount), func(t *testing.T) {
			plan := mustPlanBudgets(t, []resolve.ResolvedResource{userBudget("Engineering", amount)}, stored)
			wantOps(t, "unchanged", plan.Unchanged, "Engineering")
			wantOps(t, "updates", plan.Updates)
		})
	}

	plan := mustPlanBudgets(t, []resolve.ResolvedResource{userBudget("Engineering", 25)}, stored)
	wantOps(t, "updates", plan.Updates, "Engineering")
	if got := *plan.Updates[0].Desired.Amount; got != "25.00" {
		t.Errorf("desired amount = %q, want the canonical 25.00", got)
	}
}

func TestPlanBudgets_UnparseableAmountIsSentAsWritten(t *testing.T) {
	plan := mustPlanBudgets(t, []resolve.ResolvedResource{userBudget("Engineering", "19.999")}, seededBudgets())
	wantOps(t, "creates", plan.Creates, "Engineering")
	if got := *plan.Creates[0].Desired.Amount; got != "19.999" {
		t.Errorf("amount = %q, want it passed through for the platform to refuse", got)
	}
}

func TestReadBudgetSpec_RefusesAFloatYAMLHasRounded(t *testing.T) {
	_, err := readBudgetSpec(userBudget("Engineering", 12345678901234567.89))
	if err == nil || !strings.Contains(err.Error(), "quote the amount") {
		t.Fatalf("err = %v, want a refusal asking for a quoted amount", err)
	}
	for _, exact := range []float64{123456789.99, 1e16} {
		if _, err := readBudgetSpec(userBudget("Engineering", exact)); err != nil {
			t.Errorf("%v holds every digit it was written with but was refused: %v", exact, err)
		}
	}
}

func TestReadBudgetSpec_StructuralErrors(t *testing.T) {
	cases := map[string]resolve.ResolvedResource{
		"subjectType missing": budgetFile("X", map[string]any{"amount": nil}),
		"amount missing":      budgetFile("X", map[string]any{"subjectType": "user"}),
		"amount a list":       budgetFile("X", map[string]any{"subjectType": "user", "amount": []any{1}}),
		"warn a string":       budgetFile("X", map[string]any{"subjectType": "user", "amount": "1", "warningPercentage": "80"}),
		"isDefault a string":  budgetFile("X", map[string]any{"subjectType": "user", "amount": "1", "isDefault": "yes"}),
		"mesh not default":    budgetFile("X", map[string]any{"subjectType": "mesh", "amount": nil, "isDefault": false}),
	}
	for name, r := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := readBudgetSpec(r); err == nil {
				t.Error("readBudgetSpec succeeded, want an error")
			}
		})
	}
}

func TestPlanBudgets_UserBudgetsMatchByFoldedName(t *testing.T) {
	stored := withBudget(seededBudgets(), wire.BudgetResponse{ID: "e1", Name: "engineering", SubjectType: store.BudgetSubjectUser})

	recased := mustPlanBudgets(t, []resolve.ResolvedResource{userBudget("Engineering", nil)}, stored)
	wantOps(t, "updates", recased.Updates, "Engineering")
	if recased.Updates[0].ID != "e1" {
		t.Errorf("recase targets %q, want e1", recased.Updates[0].ID)
	}

	renamed := mustPlanBudgets(t, []resolve.ResolvedResource{userBudget("Eng Team", nil)}, stored)
	wantOps(t, "creates", renamed.Creates, "Eng Team")
	wantOps(t, "deletes", renamed.Deletes, "engineering")
}

func TestPlanBudgets_MeshMatchesByTier(t *testing.T) {
	mesh := budgetFile("Whole Mesh", map[string]any{"subjectType": "mesh", "amount": "1000"})
	plan := mustPlanBudgets(t, []resolve.ResolvedResource{mesh}, seededBudgets())
	wantOps(t, "updates", plan.Updates, "Whole Mesh")
	if plan.Updates[0].ID != meshID {
		t.Errorf("mesh rename targets %q, want the seeded mesh row", plan.Updates[0].ID)
	}
	wantOps(t, "creates", plan.Creates)
}

func TestPlanBudgets_UndeclaredMeshIsNeverPruned(t *testing.T) {
	plan := mustPlanBudgets(t, []resolve.ResolvedResource{userBudget("Default User", nil)}, seededBudgets())
	wantOps(t, "deletes", plan.Deletes)
	wantOps(t, "kept", plan.Kept)
}

func TestPlanBudgets_PlanErrors(t *testing.T) {
	stored := withBudget(seededBudgets(), wire.BudgetResponse{ID: "e1", Name: "Engineering", SubjectType: store.BudgetSubjectUser})
	isDefault := func(r resolve.ResolvedResource, v bool) resolve.ResolvedResource {
		r.Spec["isDefault"] = v
		return r
	}
	cases := map[string]struct {
		desired []resolve.ResolvedResource
		want    string
	}{
		"names equal ignoring case": {
			[]resolve.ResolvedResource{userBudget("Ops", nil), userBudget("OPS", nil)}, "unique regardless of case",
		},
		"two mesh budgets": {
			[]resolve.ResolvedResource{
				budgetFile("M1", map[string]any{"subjectType": "mesh", "amount": nil}),
				budgetFile("M2", map[string]any{"subjectType": "mesh", "amount": nil}),
			}, "both mesh budgets",
		},
		"two user defaults": {
			[]resolve.ResolvedResource{isDefault(userBudget("A1", nil), true), isDefault(userBudget("A2", nil), true)}, "one default",
		},
		"demoting the default with no replacement": {
			[]resolve.ResolvedResource{isDefault(userBudget("Default User", nil), false)}, "always has a default",
		},
		"subjectType mismatch": {
			[]resolve.ResolvedResource{budgetFile("Engineering", map[string]any{"subjectType": "team", "amount": nil})}, "tier is fixed",
		},
		"user budget named like the mesh budget": {
			[]resolve.ResolvedResource{budgetFile("Agent Mesh", map[string]any{"subjectType": "user", "amount": nil})}, "mesh budget still has this name",
		},
		"mesh renamed while a user budget takes its old name": {
			[]resolve.ResolvedResource{
				budgetFile("Global", map[string]any{"subjectType": "mesh", "amount": nil}),
				budgetFile("Agent Mesh", map[string]any{"subjectType": "user", "amount": nil}),
			}, "rename the mesh budget in an earlier apply",
		},
		"mesh renamed onto a user budget": {
			[]resolve.ResolvedResource{budgetFile("engineering", map[string]any{"subjectType": "mesh", "amount": nil})}, "collides",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := planBudgets(tc.desired, stored)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestPlanBudgets_PruneKeepsTheTierDefault(t *testing.T) {
	plan := mustPlanBudgets(t, []resolve.ResolvedResource{userBudget("Engineering", nil)}, seededBudgets())
	wantOps(t, "deletes", plan.Deletes)
	wantOps(t, "kept", plan.Kept, "Default User")
	if len(plan.Kept[0].Warnings) != 1 || !strings.Contains(plan.Kept[0].Warnings[0], "isDefault: true") {
		t.Errorf("kept warnings = %v, want one naming the way to replace the default", plan.Kept[0].Warnings)
	}
}

func TestPlanBudgets_PruneDeletesAnAssignedBudgetSayingWhoReverts(t *testing.T) {
	stored := withBudget(withBudget(seededBudgets(),
		wire.BudgetResponse{ID: "budget-eng", Name: "Engineering", SubjectType: store.BudgetSubjectUser, AssignedCount: 12}),
		wire.BudgetResponse{ID: "budget-ops", Name: "Operations", SubjectType: store.BudgetSubjectUser, AssignedCount: 1})
	stored = withBudget(stored, wire.BudgetResponse{ID: "budget-idle", Name: "Idle", SubjectType: store.BudgetSubjectUser})
	plan := mustPlanBudgets(t, nil, stored)
	wantOps(t, "deletes", plan.Deletes, "Engineering", "Idle", "Operations")
	wantOps(t, "kept", plan.Kept, "Default User")
	for i, want := range map[int][]string{
		0: {"deleting it reverts its 12 assigned users to the default user budget"},
		1: nil,
		2: {"deleting it reverts its 1 assigned user to the default user budget"},
	} {
		if got := plan.Deletes[i].Warnings; !slices.Equal(got, want) {
			t.Errorf("%s warnings = %v, want %v", plan.Deletes[i].Name, got, want)
		}
	}
	diagnostics := (&PlanResult{Budgets: plan}).Diagnostics()
	if !slices.Contains(diagnostics, Warning(`budget "Engineering": deleting it reverts its 12 assigned users to the default user budget`)) {
		t.Errorf("diagnostics = %v, want Engineering's revert warning", diagnostics)
	}
}

func TestPlanBudgets_PromotionFreesTheOldDefaultForPrune(t *testing.T) {
	desired := budgetFile("Engineering", map[string]any{"subjectType": "user", "amount": nil, "isDefault": true})
	plan := mustPlanBudgets(t, []resolve.ResolvedResource{desired}, seededBudgets())
	wantOps(t, "creates", plan.Creates, "Engineering")
	if !plan.Creates[0].Promote {
		t.Error("create does not promote, want it to become the user default")
	}
	wantOps(t, "deletes", plan.Deletes, "Default User")
	wantOps(t, "kept", plan.Kept)
}

func TestPlanBudgets_ALimitedUserDefaultWarnsThatItLimitsSystemUsers(t *testing.T) {
	limitedDefault := budgetFile("Default User", map[string]any{"subjectType": "user", "amount": "20"})
	newDefault := budgetFile("Engineering", map[string]any{"subjectType": "user", "amount": "50", "isDefault": true})
	alreadyLimited := []wire.BudgetResponse{
		{ID: meshID, Name: "Agent Mesh", SubjectType: store.BudgetSubjectMesh, IsDefault: true},
		{ID: defaultUserID, Name: "Default User", SubjectType: store.BudgetSubjectUser, IsDefault: true, Amount: new("20.00")},
	}
	for name, tc := range map[string]struct {
		desired       []resolve.ResolvedResource
		stored        []wire.BudgetResponse
		warned        string
		wantUnchanged []string
	}{
		"an amount on the current default":           {desired: []resolve.ResolvedResource{limitedDefault}, stored: seededBudgets(), warned: "Default User"},
		"a limited default the plan leaves as it is": {desired: []resolve.ResolvedResource{limitedDefault}, stored: alreadyLimited, warned: "Default User", wantUnchanged: []string{"Default User"}},
		"a limited budget promoted to default":       {desired: []resolve.ResolvedResource{newDefault}, stored: seededBudgets(), warned: "Engineering"},
		"a limited default another budget replaces":  {desired: []resolve.ResolvedResource{limitedDefault, newDefault}, stored: alreadyLimited, warned: "Engineering"},
		"a limited budget that is not the default":   {desired: []resolve.ResolvedResource{userBudget("Sales", "50")}, stored: seededBudgets()},
		"an unlimited default":                       {desired: []resolve.ResolvedResource{budgetFile("Default User", map[string]any{"subjectType": "user", "amount": nil})}, stored: seededBudgets()},
	} {
		budgets := mustPlanBudgets(t, tc.desired, tc.stored)
		if tc.wantUnchanged != nil {
			wantOps(t, "unchanged", budgets.Unchanged, tc.wantUnchanged...)
		}
		plan := &PlanResult{Budgets: budgets}
		var warned []string
		for _, d := range plan.Diagnostics() {
			if strings.Contains(d.Message, "also limits every service account without an assigned budget") {
				warned = append(warned, d.Message)
			}
		}
		switch {
		case tc.warned == "" && len(warned) != 0:
			t.Errorf("%s: diagnostics = %v, want no system-user warning", name, warned)
		case tc.warned != "" && (len(warned) != 1 || !strings.Contains(warned[0], fmt.Sprintf("budget %q", tc.warned))):
			t.Errorf("%s: diagnostics = %v, want one system-user warning on %q", name, warned, tc.warned)
		}
	}
}

func TestPlanBudgets_OmittedFieldsMeanNoneButOmittedDefaultIsUnmanaged(t *testing.T) {
	stored := withBudget(seededBudgets(), wire.BudgetResponse{
		ID: "e1", Name: "Engineering", SubjectType: store.BudgetSubjectUser, Amount: new("10.00"), WarningPercentage: new(80), IsDefault: false,
	})
	plan := mustPlanBudgets(t, []resolve.ResolvedResource{userBudget("Engineering", "10")}, stored)
	wantOps(t, "updates", plan.Updates, "Engineering")
	if op := plan.Updates[0]; op.Desired.WarningPercentage != nil || op.Promote {
		t.Errorf("op = %+v, want warningPercentage cleared and no promotion", op)
	}

	unmanaged := mustPlanBudgets(t, []resolve.ResolvedResource{userBudget("Default User", nil)}, seededBudgets())
	wantOps(t, "unchanged", unmanaged.Unchanged, "Default User")
}

type fakeBudgetPlatform struct {
	t           *testing.T
	rejectPatch bool
	mu          sync.Mutex
	rows        []wire.BudgetResponse
	nextID      int
	calls       []string
}

func newFakeBudgetPlatform(t *testing.T, rows []wire.BudgetResponse) (*fakeBudgetPlatform, *Client) {
	f := &fakeBudgetPlatform{t: t, rows: rows}
	srv := httptest.NewServer(servedOnV2(f.ServeHTTP))
	t.Cleanup(srv.Close)
	return f, newClientAgainstServer(srv)
}

func (f *fakeBudgetPlatform) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	f.calls = append(f.calls, strings.TrimSpace(r.Method+" "+r.URL.Path+" "+string(body)))
	id, promotion := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, budgetsResource.apiPath()+"/"), "/makeDefault")
	var row *wire.BudgetResponse
	if r.URL.Path != budgetsResource.apiPath() {
		if row = f.find(id); row == nil {
			http.NotFound(w, r)
			return
		}
	}

	switch {
	case r.Method == "GET" && r.URL.Path == budgetsResource.apiPath():
		f.list(w, r)
	case r.Method == "POST":
		var raw map[string]json.RawMessage
		_ = json.Unmarshal(body, &raw)
		var req wire.CreateBudgetRequest
		_ = json.Unmarshal(body, &req)
		if _, sent := raw["isDefault"]; sent {
			writeV2Error(w, http.StatusBadRequest, "isDefault is not a field of a create", nil)
			return
		}
		f.nextID++
		row := wire.BudgetResponse{ID: fmt.Sprintf("new-%d", f.nextID), Name: req.Name, SubjectType: req.SubjectType, Amount: patchedValue(req.Amount), WarningPercentage: req.WarningPercentage}
		f.rows = append(f.rows, row)
		writeBudgetJSON(w, http.StatusCreated, budgetData(row))
	case r.Method == "PATCH" && f.rejectPatch:
		writeV2Error(w, http.StatusUnprocessableEntity, "validation failed", nil)
	case r.Method == "PATCH":
		var req wire.UpdateBudgetRequest
		_ = json.Unmarshal(body, &req)
		row.Name, row.Amount, row.WarningPercentage = req.Name.Value, patchedValue(req.Amount), patchedValue(req.WarningPercentage)
		writeBudgetJSON(w, http.StatusOK, budgetData(*row))
	case r.Method == "PUT" && promotion:
		for i := range f.rows {
			if f.rows[i].SubjectType == row.SubjectType {
				f.rows[i].IsDefault = false
			}
		}
		row.IsDefault = true
		writeBudgetJSON(w, http.StatusOK, budgetData(*row))
	case r.Method == "DELETE":
		if row.IsDefault {
			writeV2Error(w, http.StatusConflict, "default", nil)
			return
		}
		for i := range f.rows {
			if f.rows[i].ID == id {
				f.rows = append(f.rows[:i], f.rows[i+1:]...)
				break
			}
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}
}

func (f *fakeBudgetPlatform) list(w http.ResponseWriter, r *http.Request) {
	size, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	page, _ := strconv.Atoi(r.URL.Query().Get("pageNumber"))
	start := min((page-1)*size, len(f.rows))
	end := min(start+size, len(f.rows))
	resp := entplatform.PaginatedResponse[wire.BudgetResponse]{Data: append([]wire.BudgetResponse{}, f.rows[start:end]...)}
	if end < len(f.rows) {
		next := page + 1
		resp.Meta.Pagination.NextPage = &next
	}
	writeBudgetJSON(w, http.StatusOK, resp)
}

func (f *fakeBudgetPlatform) find(id string) *wire.BudgetResponse {
	for i := range f.rows {
		if f.rows[i].ID == id {
			return &f.rows[i]
		}
	}
	f.t.Errorf("no budget %q", id)
	return nil
}

func budgetData(b wire.BudgetResponse) entplatform.DataResponse[wire.BudgetResponse] {
	return entplatform.DataResponse[wire.BudgetResponse]{Data: b}
}

func patchedValue[T any](p wire.Patch[T]) *T {
	if p.Null {
		return nil
	}
	return &p.Value
}

func writeBudgetJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func TestListBudgets_WalksEveryPage(t *testing.T) {
	rows := seededBudgets()
	for i := range listPageSize + 5 {
		rows = append(rows, wire.BudgetResponse{ID: fmt.Sprintf("b%d", i), Name: fmt.Sprintf("Budget %03d", i), SubjectType: store.BudgetSubjectUser})
	}
	_, c := newFakeBudgetPlatform(t, rows)
	got, err := c.ListBudgets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(rows) {
		t.Errorf("listed %d budgets, want %d", len(got), len(rows))
	}
}

func TestListBudgets_404SaysThePlatformDoesNotServeBudgets(t *testing.T) {
	srv := httptest.NewServer(servedOnV2(http.NotFound))
	t.Cleanup(srv.Close)
	_, err := newClientAgainstServer(srv).ListBudgets(context.Background())
	if !errors.Is(err, ErrBudgetsUnavailable) {
		t.Errorf("err = %v, want %v", err, ErrBudgetsUnavailable)
	}
}

func TestApplyBudgets_PromotesBeforeDeletingTheOldDefault(t *testing.T) {
	f, c := newFakeBudgetPlatform(t, seededBudgets())
	desired := []resolve.ResolvedResource{
		budgetFile("Engineering", map[string]any{"subjectType": "user", "amount": "500", "warningPercentage": 80, "isDefault": true}),
	}
	actual, err := c.ListBudgets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := applyAllBudgets(context.Background(), c, mustPlanBudgets(t, desired, actual), ApplyOptions{Prune: true})
	if len(out.Failures) != 0 {
		t.Fatalf("failures = %+v", out.Failures)
	}

	want := []string{
		`POST /api/v2/platform/budgets {"name":"Engineering","subjectType":"user","amount":"500.00","warningPercentage":80}`,
		`PUT /api/v2/platform/budgets/new-1/makeDefault`,
		`DELETE /api/v2/platform/budgets/` + defaultUserID,
	}
	writes := f.calls[1:]
	if strings.Join(writes, "\n") != strings.Join(want, "\n") {
		t.Errorf("writes:\n%s\nwant:\n%s", strings.Join(writes, "\n"), strings.Join(want, "\n"))
	}
}

func TestApplyBudgets_PatchSendsTheWholeBudget(t *testing.T) {
	stored := withBudget(seededBudgets(), wire.BudgetResponse{ID: "e1", Name: "Engineering", SubjectType: store.BudgetSubjectUser, Amount: new("10.00"), WarningPercentage: new(80)})
	f, c := newFakeBudgetPlatform(t, stored)
	plan := mustPlanBudgets(t, []resolve.ResolvedResource{userBudget("Engineering", nil)}, stored)
	out := applyAllBudgets(context.Background(), c, plan, ApplyOptions{})
	if len(out.Updated) != 1 {
		t.Fatalf("outcome = %+v, want one update", out)
	}
	if want := `PATCH /api/v2/platform/budgets/e1 {"name":"Engineering","amount":null,"warningPercentage":null}`; f.calls[0] != want {
		t.Errorf("sent %s\nwant %s", f.calls[0], want)
	}
	wantOps(t, "skipped without --prune", resultOps(out.Skipped))
}

func TestApplyBudgets_DeletesOnlyUnderPrune(t *testing.T) {
	stored := withBudget(seededBudgets(), wire.BudgetResponse{ID: "o1", Name: "Old", SubjectType: store.BudgetSubjectUser})
	plan := mustPlanBudgets(t, []resolve.ResolvedResource{userBudget("Default User", nil)}, stored)

	f, c := newFakeBudgetPlatform(t, stored)
	out := applyAllBudgets(context.Background(), c, plan, ApplyOptions{})
	wantOps(t, "skipped", resultOps(out.Skipped), "Old")
	if len(f.calls) != 0 {
		t.Errorf("calls without --prune = %v, want none", f.calls)
	}

	out = applyAllBudgets(context.Background(), c, plan, ApplyOptions{Prune: true})
	wantOps(t, "deleted", resultOps(out.Deleted), "Old")
}

func TestApplyBudgets_KeepsABudgetAMoveOffDidNotLeave(t *testing.T) {
	stored := withBudget(withBudget(withBudget(seededBudgets(),
		wire.BudgetResponse{ID: "f1", Name: "Failed", SubjectType: store.BudgetSubjectUser, AssignedCount: 1}),
		wire.BudgetResponse{ID: "s1", Name: "Skipped", SubjectType: store.BudgetSubjectUser, AssignedCount: 1}),
		wire.BudgetResponse{ID: "o1", Name: "Old", SubjectType: store.BudgetSubjectUser})
	plan := mustPlanBudgets(t, []resolve.ResolvedResource{userBudget("Default User", nil)}, stored)
	assignments := &budgetAssignmentOutcome{
		Failures: []budgetAssignmentOpResult{{Op: budgetAssignmentOp{ID: "a-1", Budget: "Platform", FromBudget: "Failed"}, Verb: verbMove, Err: errors.New("move refused")}},
		Skipped:  []budgetAssignmentOpResult{{Op: budgetAssignmentOp{ID: "a-2", Budget: "Platform", FromBudget: "Skipped"}, Verb: verbMove, Reason: skipReasonBudgetNotCreated}},
	}

	f, c := newFakeBudgetPlatform(t, stored)
	out := applyBudgets(context.Background(), c, plan, ApplyOptions{Prune: true})
	applyBudgetDeletes(context.Background(), c, plan, assignments, ApplyOptions{Prune: true}, out)

	wantOps(t, "deleted", resultOps(out.Deleted), "Old")
	wantOps(t, "skipped", resultOps(out.Skipped), "Failed", "Skipped")
	for _, r := range out.Skipped {
		if r.Reason != skipReasonAssigneesNotMoved {
			t.Errorf("%s skipped for %q, want %q", r.Op.Name, r.Reason, skipReasonAssigneesNotMoved)
		}
	}
	for _, call := range f.calls {
		if strings.Contains(call, "/f1") || strings.Contains(call, "/s1") {
			t.Errorf("deleted a budget its users had not left: %s", call)
		}
	}
	var rendered bytes.Buffer
	renderBudgetApply(&rendered, out, FormatOptions{NoColor: true})
	if !strings.Contains(rendered.String(), "skipped (assignees_not_moved)") {
		t.Errorf("apply output:\n%s\nwant the kept budgets labelled with why", rendered.String())
	}
}

func resultOps(results []budgetOpResult) []budgetOp {
	ops := make([]budgetOp, len(results))
	for i, r := range results {
		ops[i] = r.Op
	}
	return ops
}

func TestRenderBudgetPlan_VerboseShowsOnlyDeclaredDefault(t *testing.T) {
	stored := withBudget(seededBudgets(), wire.BudgetResponse{ID: "e1", Name: "Engineering", SubjectType: store.BudgetSubjectUser, Amount: new("10.00")})

	var unmanaged bytes.Buffer
	renderBudgetPlan(&unmanaged, mustPlanBudgets(t, []resolve.ResolvedResource{userBudget("Engineering", "20")}, stored), FormatOptions{Verbose: true})
	if !strings.Contains(unmanaged.String(), "amount: 10.00  →  20.00") {
		t.Errorf("verbose plan:\n%s\nwant the amount change", unmanaged.String())
	}
	if strings.Contains(unmanaged.String(), "isDefault: false") {
		t.Errorf("verbose plan shows isDefault the file does not declare:\n%s", unmanaged.String())
	}

	var promoted bytes.Buffer
	desired := budgetFile("Engineering", map[string]any{"subjectType": "user", "amount": "10", "isDefault": true})
	renderBudgetPlan(&promoted, mustPlanBudgets(t, []resolve.ResolvedResource{desired}, stored), FormatOptions{Verbose: true})
	for _, want := range []string{"set default", "isDefault: false  →  true", "delete (needs --prune)"} {
		if !strings.Contains(promoted.String(), want) {
			t.Errorf("verbose plan:\n%s\nwant %q", promoted.String(), want)
		}
	}

	var pruning bytes.Buffer
	renderBudgetPlan(&pruning, mustPlanBudgets(t, []resolve.ResolvedResource{desired}, stored), FormatOptions{Prune: true})
	if !strings.Contains(pruning.String(), "delete") || strings.Contains(pruning.String(), "needs --prune") {
		t.Errorf("plan under --prune:\n%s\nwant the delete without the --prune note", pruning.String())
	}
}

func TestApplyBudgets_FailuresNameTheStepThatFailed(t *testing.T) {
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			writeBudgetJSON(w, http.StatusCreated, budgetData(wire.BudgetResponse{ID: "new-1", Name: "Engineering"}))
			return
		}
		writeV2Error(w, http.StatusConflict, "refused", nil)
	}))
	t.Cleanup(srv.Close)
	stored := withBudget(seededBudgets(), wire.BudgetResponse{ID: "o1", Name: "Old", SubjectType: store.BudgetSubjectUser})
	desired := budgetFile("Engineering", map[string]any{"subjectType": "user", "amount": nil, "isDefault": true})

	out := applyAllBudgets(context.Background(), newClientAgainstServer(srv), mustPlanBudgets(t, []resolve.ResolvedResource{desired}, stored), ApplyOptions{Prune: true})

	verbs := map[string]string{}
	for _, f := range out.Failures {
		verbs[f.Op.Name] = f.Verb
	}
	if verbs["Engineering"] != verbSetDefault || verbs["Old"] != verbDelete {
		t.Errorf("failure verbs = %v, want Engineering: %q and Old: %q", verbs, verbSetDefault, verbDelete)
	}
	var rendered bytes.Buffer
	renderBudgetApply(&rendered, out, FormatOptions{})
	for _, want := range []string{"set default failed", "delete failed"} {
		if !strings.Contains(rendered.String(), want) {
			t.Errorf("apply output:\n%s\nwant %q", rendered.String(), want)
		}
	}
}

func TestCreateBudget_RefusesAResponseWithoutABudget(t *testing.T) {
	var promoted bool
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			promoted = true
		}
		writeBudgetJSON(w, http.StatusCreated, budgetData(wire.BudgetResponse{Name: "Engineering"}))
	}))
	t.Cleanup(srv.Close)
	desired := budgetFile("Engineering", map[string]any{"subjectType": "user", "amount": nil, "isDefault": true})

	out := applyAllBudgets(context.Background(), newClientAgainstServer(srv), mustPlanBudgets(t, []resolve.ResolvedResource{desired}, seededBudgets()), ApplyOptions{})

	if len(out.Failures) != 1 || out.Failures[0].Verb != verbCreate || !strings.Contains(out.Failures[0].Err.Error(), "returned no budget") {
		t.Errorf("failures = %+v, want the create refused for returning no budget", out.Failures)
	}
	if promoted {
		t.Error("apply promoted a budget the create did not return")
	}
}

func TestBudgetOps_FeedPendingResultsAndDiagnostics(t *testing.T) {
	changed := &PlanResult{Budgets: &budgetPlan{Kind: "budget", Updates: []budgetOp{{Name: "Engineering", Source: "budgets/eng.yaml"}}}}
	if !changed.Pending(false, false) {
		t.Error("a budget-only update is not pending; --detailed-exit-code would exit 0")
	}
	if ops := changed.Ops(); len(ops) != 1 || ops[0] != (PlanOp{Kind: "budget", Name: "Engineering", Action: ActionUpdate, Source: "budgets/eng.yaml"}) {
		t.Errorf("Ops() = %+v", ops)
	}

	kept := &PlanResult{Budgets: &budgetPlan{Kind: "budget", Kept: []budgetOp{{Name: "Default User", Warnings: []string{"not deleted"}}}}}
	if kept.Pending(true, false) {
		t.Error("a kept budget counts as pending, but apply --prune will not delete it")
	}
	if d := kept.Diagnostics(); len(d) != 1 || !strings.Contains(d[0].Message, `budget "Default User": not deleted`) {
		t.Errorf("Diagnostics() = %+v, want the kept budget's warning", d)
	}

	applied := &ApplyResult{Budgets: &budgetOutcome{Kind: "budget",
		Created:  []budgetOpResult{{Op: budgetOp{Name: "Engineering"}, Verb: verbCreate}},
		Failures: []budgetOpResult{{Op: budgetOp{Name: "Old"}, Verb: verbDelete, Err: errors.New("refused")}},
	}}
	want := []ApplyOpResult{
		{Kind: "budget", Name: "Engineering", Status: StatusCreated},
		{Kind: "budget", Name: "Old", Status: StatusFailed, Action: verbDelete, Error: "refused"},
	}
	if got := applied.Results(); !slices.Equal(got, want) {
		t.Errorf("Results() = %+v, want %+v", got, want)
	}
}

func TestReadBudgetSpec_RefusesUnknownKeys(t *testing.T) {
	_, err := readBudgetSpec(budgetFile("Engineering", map[string]any{"subjectType": "user", "amount": "10", "warningPercent": 80}))
	if err == nil || !strings.Contains(err.Error(), `unknown spec key "warningPercent"`) {
		t.Errorf("err = %v, want the misspelt key refused before it clears the real threshold", err)
	}
}

func TestPlanBudgets_TrimsTheAuthoredName(t *testing.T) {
	stored := withBudget(seededBudgets(), wire.BudgetResponse{ID: "e1", Name: "Engineering", SubjectType: store.BudgetSubjectUser})
	plan := mustPlanBudgets(t, []resolve.ResolvedResource{userBudget("Engineering ", nil)}, stored)
	wantOps(t, "unchanged", plan.Unchanged, "Engineering")
}

func TestApplyBudgets_PromotesAnExistingBudgetBeforeDeletingTheOldDefault(t *testing.T) {
	stored := withBudget(seededBudgets(), wire.BudgetResponse{ID: "e1", Name: "Engineering", SubjectType: store.BudgetSubjectUser})
	f, c := newFakeBudgetPlatform(t, stored)
	desired := []resolve.ResolvedResource{budgetFile("Engineering", map[string]any{"subjectType": "user", "amount": nil, "isDefault": true})}

	out := applyAllBudgets(context.Background(), c, mustPlanBudgets(t, desired, stored), ApplyOptions{Prune: true})
	if len(out.Failures) != 0 {
		t.Fatalf("failures = %+v", out.Failures)
	}
	want := []string{`PUT /api/v2/platform/budgets/e1/makeDefault`, `DELETE /api/v2/platform/budgets/` + defaultUserID}
	if strings.Join(f.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(f.calls, "\n"), strings.Join(want, "\n"))
	}

	actual, err := c.ListBudgets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	replan := mustPlanBudgets(t, desired, actual)
	if len(replan.Creates)+len(replan.Updates)+len(replan.Deletes)+len(replan.Kept) != 0 {
		t.Errorf("re-plan = %+v, want quiet", replan)
	}
}

func TestPlanAllBudgets_SkipsAManifestWithoutBudgets(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.NotFound(w, nil)
	}))
	t.Cleanup(srv.Close)
	m := &manifest.Manifest{Resources: map[string][]manifest.ResourceEntry{"models": nil}}
	out := &PlanResult{}

	if err := planAllBudgets(context.Background(), newClientAgainstServer(srv), m, resolve.New(t.TempDir(), m, resolve.Options{}), out); err != nil {
		t.Fatalf("planAllBudgets: %v", err)
	}
	if out.Budgets != nil || requests != 0 {
		t.Errorf("budgets plan = %+v after %d requests, want nil and none", out.Budgets, requests)
	}
}

func TestListBudgets_RefusesAServerThatNeverStopsPaging(t *testing.T) {
	srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, _ *http.Request) {
		next := 2
		page := entplatform.PaginatedResponse[wire.BudgetResponse]{Data: []wire.BudgetResponse{{ID: "x", Name: "Loop"}}}
		page.Meta.Pagination.NextPage = &next
		writeBudgetJSON(w, http.StatusOK, page)
	}))
	t.Cleanup(srv.Close)
	if _, err := newClientAgainstServer(srv).ListBudgets(context.Background()); err == nil || !strings.Contains(err.Error(), "still paging") {
		t.Errorf("err = %v, want the page cap to stop the walk", err)
	}
}

func TestRenderPlan_PrintsAKeptBudgetsWarningOnce(t *testing.T) {
	plan := &PlanResult{Budgets: mustPlanBudgets(t, []resolve.ResolvedResource{userBudget("Engineering", nil)}, seededBudgets())}
	var out bytes.Buffer
	RenderPlan(&out, plan, FormatOptions{NoColor: true})
	if n := strings.Count(out.String(), "not deleted: it is the user tier's default"); n != 1 {
		t.Errorf("warning printed %d times, want once:\n%s", n, out.String())
	}
	if !strings.Contains(out.String(), "Default User         kept (tier default)") {
		t.Errorf("plan output:\n%s\nwant Default User's kept row", out.String())
	}
}

func TestRenderPlan_LabelsEachKeptBudgetWithItsReason(t *testing.T) {
	cases := []struct {
		name    string
		desired []resolve.ResolvedResource
		stored  []wire.BudgetResponse
		want    map[string]keptReason
	}{
		{
			name:    "the default and an assigned budget",
			desired: []resolve.ResolvedResource{userBudget("Engineering", nil)},
			stored:  withBudget(seededBudgets(), wire.BudgetResponse{ID: "budget-ops", Name: "Operations", SubjectType: store.BudgetSubjectUser, AssignedCount: 2}),
			want:    map[string]keptReason{"Default User": keptTierDefault},
		},
		{
			name:    "a replaced default that still has users is not kept",
			desired: []resolve.ResolvedResource{budgetFile("Engineering", map[string]any{"subjectType": "user", "amount": nil, "isDefault": true})},
			stored: []wire.BudgetResponse{
				{ID: meshID, Name: "Agent Mesh", SubjectType: store.BudgetSubjectMesh, IsDefault: true},
				{ID: defaultUserID, Name: "Default User", SubjectType: store.BudgetSubjectUser, IsDefault: true, AssignedCount: 3},
			},
			want: map[string]keptReason{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			RenderPlan(&out, &PlanResult{Budgets: mustPlanBudgets(t, tc.desired, tc.stored)}, FormatOptions{NoColor: true})
			for name, label := range tc.want {
				if !strings.Contains(out.String(), "= "+padName(name)+" "+string(label)) {
					t.Errorf("plan output:\n%s\nwant %s %s", out.String(), name, label)
				}
			}
			if n := strings.Count(out.String(), "not deleted: "); n != len(tc.want) {
				t.Errorf("%d kept warnings printed, want %d:\n%s", n, len(tc.want), out.String())
			}
		})
	}
}

func TestApplyBudgets_RejectedUpdateSkipsItsPromotion(t *testing.T) {
	stored := withBudget(seededBudgets(), wire.BudgetResponse{ID: "e1", Name: "Engineering", SubjectType: store.BudgetSubjectUser, Amount: new("10.00")})
	f, c := newFakeBudgetPlatform(t, stored)
	f.rejectPatch = true
	desired := []resolve.ResolvedResource{budgetFile("Engineering", map[string]any{"subjectType": "user", "amount": "20", "isDefault": true})}

	out := applyAllBudgets(context.Background(), c, mustPlanBudgets(t, desired, stored), ApplyOptions{})

	if len(out.Failures) != 1 || out.Failures[0].Verb != verbUpdate || len(out.Updated) != 0 {
		t.Errorf("outcome = %+v, want one update failure", out)
	}
	for _, call := range f.calls {
		if strings.HasSuffix(call, "/makeDefault") {
			t.Errorf("promoted after the update was refused: %s", call)
		}
	}
}

func TestRenderBudgets_CombinedWriteAndPromotionLabels(t *testing.T) {
	stored := withBudget(seededBudgets(), wire.BudgetResponse{ID: "e1", Name: "Engineering", SubjectType: store.BudgetSubjectUser, Amount: new("10.00")})
	cases := map[string]struct {
		desired   resolve.ResolvedResource
		planLabel string
		applyVerb string
	}{
		"create": {budgetFile("Ops", map[string]any{"subjectType": "user", "amount": nil, "isDefault": true}), "create + set default", "created + set default"},
		"update": {budgetFile("Engineering", map[string]any{"subjectType": "user", "amount": "20", "isDefault": true}), "update + set default", "updated + set default"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			plan := mustPlanBudgets(t, []resolve.ResolvedResource{tc.desired}, stored)
			var rendered bytes.Buffer
			renderBudgetPlan(&rendered, plan, FormatOptions{NoColor: true})
			if !strings.Contains(rendered.String(), tc.planLabel) {
				t.Errorf("plan:\n%s\nwant %q", rendered.String(), tc.planLabel)
			}

			_, c := newFakeBudgetPlatform(t, slices.Clone(stored))
			rendered.Reset()
			renderBudgetApply(&rendered, applyAllBudgets(context.Background(), c, plan, ApplyOptions{}), FormatOptions{NoColor: true})
			if !strings.Contains(rendered.String(), tc.applyVerb) {
				t.Errorf("apply:\n%s\nwant %q", rendered.String(), tc.applyVerb)
			}
		})
	}
}
