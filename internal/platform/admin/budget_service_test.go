// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func newBudgetSvc(t *testing.T) (*BudgetService, *store.BudgetsRepo, *store.DB, *bytes.Buffer) {
	t.Helper()
	db := testOpenDB(t)
	removeSeededBudgets(t, db)
	repo := store.NewBudgetsRepo(db)
	var logs bytes.Buffer
	return NewBudgetService(repo, nil, slog.New(slog.NewJSONHandler(&logs, nil))), repo, db, &logs
}

func removeSeededBudgets(t *testing.T, db *store.DB) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `DELETE FROM budgets WHERE id IN ('`+store.SeededMeshBudgetID+`', '`+store.SeededDefaultUserBudgetID+`')`); err != nil {
		t.Fatalf("remove seeded budgets: %v", err)
	}
}

func budgetAudits(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var recs []map[string]any
	for line := range strings.SplitSeq(logs.String(), "\n") {
		var rec map[string]any
		if line == "" || json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec["event"] == "budget_mutation" {
			recs = append(recs, rec)
		}
	}
	return recs
}

func lastBudgetAudit(t *testing.T, logs *bytes.Buffer, action string) map[string]any {
	t.Helper()
	recs := budgetAudits(t, logs)
	for i := len(recs) - 1; i >= 0; i-- {
		if recs[i]["action"] == action {
			return recs[i]
		}
	}
	t.Fatalf("no budget audit line with action=%s; lines: %v", action, recs)
	return nil
}

func sameBudgetRow(a, b *store.Budget) bool {
	return a.ID == b.ID && a.Name == b.Name && a.NameLower == b.NameLower && a.SubjectType == b.SubjectType &&
		equalPtr(a.Amount, b.Amount) && equalPtr(a.WarnPercentage, b.WarnPercentage) && a.IsDefault == b.IsDefault &&
		a.CreatedBy == b.CreatedBy && a.UpdatedBy == b.UpdatedBy && a.CreatedTime == b.CreatedTime && a.UpdatedTime == b.UpdatedTime
}

func seedAssignableUser(t *testing.T, db *store.DB, sub string) string {
	t.Helper()
	u := &store.User{Issuer: "https://idp.test", Sub: sub, ProvisionedVia: "jit", CreatedBy: "test", UpdatedBy: "test"}
	if err := store.NewUsersRepo(db).UpsertUser(t.Context(), u, nil); err != nil {
		t.Fatalf("seed user %q: %v", sub, err)
	}
	return u.ID
}

func userBudgetReq(name string) CreateBudgetRequest {
	return CreateBudgetRequest{Name: name, SubjectType: store.BudgetSubjectUser, Amount: new("100"), AmountSet: true}
}

func mustCreate(t *testing.T, svc *BudgetService, req CreateBudgetRequest) *BudgetResponse {
	t.Helper()
	resp, err := svc.Create(t.Context(), req, "creator")
	if err != nil {
		t.Fatalf("create %q: %v", req.Name, err)
	}
	return resp
}

func validationDetails(t *testing.T, err error) map[string][]string {
	t.Helper()
	ve, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("error %v (%T) is not a *ValidationError", err, err)
	}
	return ve.Details
}

func conflictMessage(t *testing.T, err error) string {
	t.Helper()
	ce, ok := errors.AsType[*ConflictError](err)
	if !ok {
		t.Fatalf("error %v (%T) is not a *ConflictError", err, err)
	}
	return ce.Message
}

func TestBudgetService_CreateValidation(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newBudgetSvc(t)

	with := func(mutate func(*CreateBudgetRequest)) CreateBudgetRequest {
		req := userBudgetReq("Engineering")
		mutate(&req)
		return req
	}
	for _, c := range []struct {
		name string
		req  CreateBudgetRequest
		want map[string]string
	}{
		{"name missing", with(func(r *CreateBudgetRequest) { r.Name = "   " }), map[string]string{"name": "required"}},
		{"name 2 chars", with(func(r *CreateBudgetRequest) { r.Name = "ab" }), map[string]string{"name": "at least 3"}},
		{"name 2 multibyte chars", with(func(r *CreateBudgetRequest) { r.Name = "Éé" }), map[string]string{"name": "at least 3"}},
		{"name 256 chars", with(func(r *CreateBudgetRequest) { r.Name = strings.Repeat("a", 256) }), map[string]string{"name": "at most 255"}},
		{"name with newline", with(func(r *CreateBudgetRequest) { r.Name = "Eng\nTeam" }), map[string]string{"name": "control characters"}},
		{"tier missing", with(func(r *CreateBudgetRequest) { r.SubjectType = "" }), map[string]string{"subjectType": "required"}},
		{"agent tier", with(func(r *CreateBudgetRequest) { r.SubjectType = "agent" }), map[string]string{"subjectType": "not supported yet"}},
		{"unknown tier", with(func(r *CreateBudgetRequest) { r.SubjectType = "team" }), map[string]string{"subjectType": "one of: mesh, user"}},
		{"amount key absent", with(func(r *CreateBudgetRequest) { r.Amount, r.AmountSet = nil, false }), map[string]string{"amount": "required"}},
		{"negative amount", with(func(r *CreateBudgetRequest) { r.Amount = new("-1") }), map[string]string{"amount": "negative"}},
		{"three decimals", with(func(r *CreateBudgetRequest) { r.Amount = new("1.234") }), map[string]string{"amount": "2 decimal places"}},
		{"not a number", with(func(r *CreateBudgetRequest) { r.Amount = new("12 dollars") }), map[string]string{"amount": "decimal number"}},
		{"too large", with(func(r *CreateBudgetRequest) { r.Amount = new("99999999999999999999") }), map[string]string{"amount": "larger"}},
		{"warn 0", with(func(r *CreateBudgetRequest) { r.WarningPercentage = new(0) }), map[string]string{"warningPercentage": "between 1 and 100"}},
		{"warn 101", with(func(r *CreateBudgetRequest) { r.WarningPercentage = new(101) }), map[string]string{"warningPercentage": "between 1 and 100"}},
		{"warn on unlimited", with(func(r *CreateBudgetRequest) { r.Amount, r.WarningPercentage = nil, new(80) }),
			map[string]string{"amount": "unlimited", "warningPercentage": "requires an amount"}},
		{"every error at once", CreateBudgetRequest{Name: "x", SubjectType: "team", WarningPercentage: new(0)},
			map[string]string{"name": "at least 3", "subjectType": "one of", "amount": "required", "warningPercentage": "between"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.Create(t.Context(), c.req, "creator")
			details := validationDetails(t, err)
			if len(details) != len(c.want) {
				t.Errorf("details = %v, want exactly the fields %v", details, c.want)
			}
			for field, substr := range c.want {
				if !slices.ContainsFunc(details[field], func(m string) bool { return strings.Contains(m, substr) }) {
					t.Errorf("details[%s] = %v, want a message containing %q", field, details[field], substr)
				}
			}
		})
	}
}

func TestBudgetService_CreateAccepts(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newBudgetSvc(t)
	ctx := t.Context()

	resp := mustCreate(t, svc, CreateBudgetRequest{
		Name: "  Ééé team  ", SubjectType: store.BudgetSubjectUser, Amount: new("19.99"), AmountSet: true, WarningPercentage: new(80),
	})
	if resp.Name != "Ééé team" || resp.Amount == nil || *resp.Amount != "19.99" || *resp.WarningPercentage != 80 ||
		resp.IsDefault || resp.CreatedBy != "creator" || resp.UpdatedBy != "creator" {
		t.Errorf("response = %+v", resp)
	}
	stored, err := repo.GetByID(ctx, resp.ID)
	if err != nil || stored == nil {
		t.Fatalf("get: %v", err)
	}
	if want, _ := budget.ParseAmount("19.99"); stored.Amount == nil || *stored.Amount != want {
		t.Errorf("stored amount = %v, want %d at scale %d", stored.Amount, want, budget.ScaleExponent)
	}

	unlimited := mustCreate(t, svc, CreateBudgetRequest{Name: "Unlimited", SubjectType: store.BudgetSubjectUser, AmountSet: true})
	if unlimited.Amount != nil || unlimited.WarningPercentage != nil {
		t.Errorf("explicit null amount: %+v, want unlimited with no threshold", unlimited)
	}
	zero := mustCreate(t, svc, CreateBudgetRequest{
		Name: "Blocked", SubjectType: store.BudgetSubjectUser, Amount: new("0"), AmountSet: true, WarningPercentage: new(50),
	})
	if zero.Amount == nil || *zero.Amount != "0.00" || *zero.WarningPercentage != 50 {
		t.Errorf("zero amount with threshold: %+v, want accepted", zero)
	}
}

func TestBudgetService_NameConflictEchoesTheRequestedName(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newBudgetSvc(t)
	ctx := t.Context()
	mustCreate(t, svc, userBudgetReq("Engineering-Monthly"))
	other := mustCreate(t, svc, userBudgetReq("Sales"))

	_, err := svc.Create(ctx, userBudgetReq("engineering-monthly"), "creator")
	if msg := conflictMessage(t, err); !strings.Contains(msg, `"engineering-monthly"`) || strings.Contains(msg, "Engineering-Monthly") {
		t.Errorf("create conflict = %q, want the requested name only", msg)
	}
	_, err = svc.Update(ctx, other.ID, UpdateBudgetRequest{Name: new("ENGINEERING-MONTHLY")}, "editor")
	if msg := conflictMessage(t, err); !strings.Contains(msg, `"ENGINEERING-MONTHLY"`) || strings.Contains(msg, "Engineering-Monthly") {
		t.Errorf("rename conflict = %q, want the requested name only", msg)
	}
}

func TestBudgetService_MeshRules(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newBudgetSvc(t)
	ctx := t.Context()
	mesh := mustCreate(t, svc, CreateBudgetRequest{Name: "Deployment cap", SubjectType: store.BudgetSubjectMesh, Amount: new("5000"), AmountSet: true})

	_, err := svc.Create(ctx, CreateBudgetRequest{Name: "Second mesh", SubjectType: store.BudgetSubjectMesh, AmountSet: true}, "creator")
	if msg := conflictMessage(t, err); !strings.Contains(msg, "A mesh budget already exists") || strings.Contains(msg, "Deployment cap") {
		t.Errorf("second mesh budget = %q, want the mesh-exists 409 without the stored name", msg)
	}

	if !mesh.IsDefault {
		t.Errorf("created mesh budget = %+v, want its tier's default", mesh)
	}
	if resp, promoteErr := svc.SetDefault(ctx, mesh.ID, "promoter"); promoteErr != nil || !resp.IsDefault || resp.UpdatedBy == "promoter" {
		t.Errorf("SetDefault(mesh) = %+v, %v; want the unchanged default", resp, promoteErr)
	}

	err = svc.Delete(ctx, mesh.ID, "deleter")
	if msg := conflictMessage(t, err); !strings.Contains(msg, "Agent Mesh budget can't be deleted") {
		t.Errorf("deleting the mesh budget = %q, want the mesh refusal", msg)
	}
	if b, _ := repo.GetByID(ctx, mesh.ID); b == nil || !b.IsDefault {
		t.Errorf("mesh budget after refused delete: %+v", b)
	}
}

func TestBudgetService_DeleteGuardsTheDefault(t *testing.T) {
	t.Parallel()
	svc, repo, _, logs := newBudgetSvc(t)
	ctx := t.Context()
	def := mustCreate(t, svc, userBudgetReq("Standard"))
	if _, err := svc.SetDefault(ctx, def.ID, "promoter"); err != nil {
		t.Fatal(err)
	}

	err := svc.Delete(ctx, def.ID, "deleter")
	msg := conflictMessage(t, err)
	for _, want := range []string{`"Standard"`, "user tier", "Set another budget as the default"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q does not mention %q", msg, want)
		}
	}
	if b, _ := repo.GetByID(ctx, def.ID); b == nil {
		t.Fatal("the default was deleted")
	}
	rejected := lastBudgetAudit(t, logs, auditActionRejected)
	if rejected["resourceId"] != def.ID || rejected["actor"] != "deleter" || rejected["reason"] != "tier_default" {
		t.Errorf("rejected delete audit = %v", rejected)
	}

	if _, err := svc.SetDefault(ctx, mustCreate(t, svc, userBudgetReq("Successor")).ID, "promoter"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, def.ID, "deleter"); err != nil {
		t.Errorf("delete once demoted: %v", err)
	}
	if deleted := lastBudgetAudit(t, logs, auditActionDeleted); deleted["resourceId"] != def.ID || deleted["amount"] != "100.00" {
		t.Errorf("deleted audit = %v, want the budget's values", deleted)
	}

	for _, id := range []string{def.ID, "not-a-uuid"} {
		if _, ok := errors.AsType[*NotFoundError](svc.Delete(ctx, id, "deleter")); !ok {
			t.Errorf("Delete(%q): want NotFoundError", id)
		}
	}
}

func TestBudgetService_CaseOnlyRenameDoesNotCollideWithItsOwnOldName(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newBudgetSvc(t)
	b := mustCreate(t, svc, userBudgetReq("Engineering"))
	resp, err := svc.Update(t.Context(), b.ID, UpdateBudgetRequest{Name: new("ENGINEERING")}, "editor")
	if err != nil || resp.Name != "ENGINEERING" {
		t.Errorf("case-only rename = %+v, %v; want ENGINEERING", resp, err)
	}
}

func TestBudgetService_StaleEditIsAConflict(t *testing.T) {
	t.Parallel()
	b := &store.Budget{ID: idgen.New(), Name: "Engineering", SubjectType: store.BudgetSubjectUser}
	msg := conflictMessage(t, budgetWriteError(fmt.Errorf("update: %w", store.ErrBudgetStale), b))
	if !strings.Contains(msg, "changed while your edit was being saved") || strings.Contains(msg, "Engineering") {
		t.Errorf("stale-edit conflict = %q, want the reload message without the name", msg)
	}
}

func TestBudgetService_DeleteRevertsAnAssignedBudgetsUsersToTheDefault(t *testing.T) {
	t.Parallel()
	svc, repo, db, logs := newBudgetSvc(t)
	ctx := t.Context()
	assignments := store.NewBudgetAssignmentsRepo(db)
	b := mustCreate(t, svc, userBudgetReq("Engineering"))
	users := []string{seedAssignableUser(t, db, "a"), seedAssignableUser(t, db, "b")}
	for _, ref := range users {
		if _, err := assignments.Create(ctx, &store.BudgetAssignment{
			SubjectType: store.BudgetSubjectUser, SubjectRef: ref, BudgetID: b.ID, CreatedBy: "x", UpdatedBy: "x",
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := svc.Delete(ctx, b.ID, "deleter"); err != nil {
		t.Fatalf("delete an assigned budget: %v", err)
	}
	if got, _ := repo.GetByID(ctx, b.ID); got != nil {
		t.Fatal("the assigned budget survived its delete")
	}
	assigned, err := assignments.UserBudgetIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range users {
		if budgetID, ok := assigned[ref]; ok {
			t.Errorf("user %s is still assigned budget %s, want them on the default", ref, budgetID)
		}
	}
	var budgetLine map[string]any
	removed := map[any]bool{}
	for _, rec := range budgetAudits(t, logs) {
		switch {
		case rec["action"] != auditActionDeleted:
		case rec["resource"] == resourceBudget:
			budgetLine = rec
		case rec["resource"] == resourceBudgetAssignment && rec["reason"] == "budget_deleted" && rec["budgetId"] == b.ID:
			removed[rec["subjectId"]] = true
		}
	}
	if budgetLine["resourceId"] != b.ID || budgetLine["actor"] != "deleter" ||
		budgetLine["assignmentsDeleted"] != float64(2) || budgetLine["pendingAssignmentsDeleted"] != float64(0) {
		t.Errorf("budget delete audit = %v", budgetLine)
	}
	for _, ref := range users {
		if !removed[ref] {
			t.Errorf("no assignment audit line names %s, want each removal named, not just counted", ref)
		}
	}
}

func TestBudgetService_DeleteRacingAnAssignmentIsARetryableConflict(t *testing.T) {
	t.Parallel()
	svc, repo, db, logs := newBudgetSvc(t)
	ctx := t.Context()
	const insertLate = `INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id,
	                                created_by, updated_by, created_time, updated_time)
	 VALUES ('00000000-0000-7000-8000-00000000000a', 'user', 'system:late', OLD.id, 't', 't', 1, 1);`
	trigger := `CREATE TRIGGER assign_on_budget_delete BEFORE DELETE ON budgets BEGIN ` + insertLate + ` END`
	if db.Dialect() == "postgres" {
		if _, err := db.ExecContext(ctx, `CREATE FUNCTION assign_on_budget_delete() RETURNS trigger
		 LANGUAGE plpgsql AS $$ BEGIN `+insertLate+` RETURN OLD; END $$`); err != nil {
			t.Fatalf("create trigger function: %v", err)
		}
		trigger = `CREATE TRIGGER assign_on_budget_delete BEFORE DELETE ON budgets FOR EACH ROW EXECUTE FUNCTION assign_on_budget_delete()`
	}
	if _, err := db.ExecContext(ctx, trigger); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	b := mustCreate(t, svc, userBudgetReq("Engineering"))

	if msg := conflictMessage(t, svc.Delete(ctx, b.ID, "deleter")); !strings.Contains(msg, "Try again") {
		t.Errorf("conflict = %q, want the retry message", msg)
	}
	if got, _ := repo.GetByID(ctx, b.ID); got == nil {
		t.Fatal("the budget was deleted despite the late assignment")
	}
	rejected := lastBudgetAudit(t, logs, auditActionRejected)
	if rejected["resourceId"] != b.ID || rejected["actor"] != "deleter" || rejected["reason"] != "assigned_while_deleting" {
		t.Errorf("rejected delete audit = %v", rejected)
	}
}

func TestBudgetService_PatchSemantics(t *testing.T) {
	t.Parallel()
	svc, repo, _, logs := newBudgetSvc(t)
	ctx := t.Context()
	b := mustCreate(t, svc, CreateBudgetRequest{
		Name: "Engineering", SubjectType: store.BudgetSubjectUser, Amount: new("500"), AmountSet: true, WarningPercentage: new(80),
	})
	stored := func() *store.Budget {
		t.Helper()
		got, err := repo.GetByID(ctx, b.ID)
		if err != nil || got == nil {
			t.Fatalf("get: %v", err)
		}
		return got
	}
	before := stored()

	_, err := svc.Update(ctx, b.ID, UpdateBudgetRequest{AmountSet: true}, "editor")
	details := validationDetails(t, err)
	if len(details["amount"]) == 0 || len(details["warningPercentage"]) == 0 {
		t.Errorf("clearing amount under a threshold: details = %v, want both fields named", details)
	}
	if !sameBudgetRow(stored(), before) {
		t.Error("refused PATCH changed the row")
	}

	auditsBefore := len(budgetAudits(t, logs))
	for name, req := range map[string]UpdateBudgetRequest{
		"empty":             {},
		"identical values":  {Name: new("Engineering"), Amount: new("500.00"), AmountSet: true, WarningPercentage: new(80), WarningPercentageSet: true},
		"identical, as 500": {Amount: new("500"), AmountSet: true},
	} {
		resp, patchErr := svc.Update(ctx, b.ID, req, "editor")
		if patchErr != nil {
			t.Fatalf("%s patch: %v", name, patchErr)
		}
		if resp.UpdatedBy != "creator" || !sameBudgetRow(stored(), before) {
			t.Errorf("%s patch wrote the row: %+v", name, stored())
		}
	}
	if n := len(budgetAudits(t, logs)); n != auditsBefore {
		t.Errorf("no-op patches wrote %d audit lines, want none", n-auditsBefore)
	}

	resp, err := svc.Update(ctx, b.ID, UpdateBudgetRequest{WarningPercentageSet: true}, "editor")
	if err != nil {
		t.Fatalf("clear threshold: %v", err)
	}
	if resp.WarningPercentage != nil || resp.Amount == nil || *resp.Amount != "500.00" || resp.Name != "Engineering" {
		t.Errorf("after clearing only the threshold: %+v", resp)
	}
	if got := stored(); got.WarnPercentage != nil || got.UpdatedBy != "editor" || got.UpdatedTime < before.UpdatedTime {
		t.Errorf("stored after clearing the threshold: %+v", got)
	}
	resp, err = svc.Update(ctx, b.ID, UpdateBudgetRequest{AmountSet: true}, "editor")
	if err != nil || resp.Amount != nil {
		t.Errorf("clear amount once the threshold is gone = %+v, %v; want unlimited", resp, err)
	}

	if _, ok := errors.AsType[*NotFoundError](func() error {
		_, err := svc.Update(ctx, "not-a-uuid", UpdateBudgetRequest{Name: new("x-name")}, "editor")
		return err
	}()); !ok {
		t.Error("Update(non-UUID): want NotFoundError")
	}
}

func TestBudgetService_UpdateRefusesSubjectType(t *testing.T) {
	t.Parallel()
	svc, repo, _, logs := newBudgetSvc(t)
	ctx := t.Context()
	b := mustCreate(t, svc, userBudgetReq("Engineering"))
	before, err := repo.GetByID(ctx, b.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	auditsBefore := len(budgetAudits(t, logs))

	for name, c := range map[string]struct {
		req    UpdateBudgetRequest
		fields []string
	}{
		"other tier":           {UpdateBudgetRequest{SubjectType: json.RawMessage(`"mesh"`)}, []string{"subjectType"}},
		"current tier":         {UpdateBudgetRequest{SubjectType: json.RawMessage(`"user"`)}, []string{"subjectType"}},
		"null":                 {UpdateBudgetRequest{SubjectType: json.RawMessage(`null`)}, []string{"subjectType"}},
		"with a valid rename":  {UpdateBudgetRequest{Name: new("Renamed"), SubjectType: json.RawMessage(`"mesh"`)}, []string{"subjectType"}},
		"with an invalid name": {UpdateBudgetRequest{Name: new("x"), SubjectType: json.RawMessage(`"mesh"`)}, []string{"subjectType", "name"}},
	} {
		_, patchErr := svc.Update(ctx, b.ID, c.req, "editor")
		details := validationDetails(t, patchErr)
		for _, field := range c.fields {
			if len(details[field]) == 0 {
				t.Errorf("%s: details = %v, want %s named", name, details, field)
			}
		}
		if len(details) != len(c.fields) {
			t.Errorf("%s: details = %v, want only %v", name, details, c.fields)
		}
	}
	after, err := repo.GetByID(ctx, b.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !sameBudgetRow(after, before) {
		t.Errorf("refused PATCH changed the row: %+v", after)
	}
	if n := len(budgetAudits(t, logs)); n != auditsBefore {
		t.Errorf("refused patches wrote %d audit lines, want none", n-auditsBefore)
	}
}

func TestBudgetService_AuditCarriesValues(t *testing.T) {
	t.Parallel()
	svc, _, _, logs := newBudgetSvc(t)
	ctx := t.Context()

	first := mustCreate(t, svc, CreateBudgetRequest{
		Name: "Standard", SubjectType: store.BudgetSubjectUser, Amount: new("500"), AmountSet: true, WarningPercentage: new(80),
	})
	created := lastBudgetAudit(t, logs, auditActionCreated)
	if created["resourceId"] != first.ID || created["actor"] != "creator" || created["amount"] != "500.00" ||
		created["warningPercentage"] != "80" || created["subjectType"] != "user" {
		t.Errorf("created audit = %v", created)
	}

	if _, err := svc.Update(ctx, first.ID, UpdateBudgetRequest{
		Amount: new("2000"), AmountSet: true, WarningPercentageSet: true,
	}, "editor"); err != nil {
		t.Fatal(err)
	}
	updated := lastBudgetAudit(t, logs, auditActionUpdated)
	amount, _ := updated["amount"].(map[string]any)
	warn, _ := updated["warningPercentage"].(map[string]any)
	if updated["actor"] != "editor" || amount["before"] != "500.00" || amount["after"] != "2000.00" ||
		warn["before"] != "80" || warn["after"] != "none" {
		t.Errorf("updated audit = %v, want amount 500.00→2000.00 and warningPercentage 80→none", updated)
	}
	if _, logged := updated["nameChanged"]; logged {
		t.Errorf("updated audit logs an unchanged name as a change: %v", updated)
	}

	if _, err := svc.Update(ctx, first.ID, UpdateBudgetRequest{Name: new("Standard Plus")}, "editor"); err != nil {
		t.Fatal(err)
	}
	if renamed := lastBudgetAudit(t, logs, auditActionUpdated); renamed["nameChanged"] != true {
		t.Errorf("renamed audit = %v, want nameChanged", renamed)
	}

	if _, err := svc.SetDefault(ctx, first.ID, "promoter"); err != nil {
		t.Fatal(err)
	}
	if set := lastBudgetAudit(t, logs, auditActionDefaultSet); set["resourceId"] != first.ID || set["previousDefault"] != "none" {
		t.Errorf("first default_set audit = %v", set)
	}
	second := mustCreate(t, svc, userBudgetReq("Premium"))
	if _, err := svc.SetDefault(ctx, second.ID, "promoter"); err != nil {
		t.Fatal(err)
	}
	if set := lastBudgetAudit(t, logs, auditActionDefaultSet); set["resourceId"] != second.ID || set["previousDefault"] != first.ID {
		t.Errorf("second default_set audit = %v, want previousDefault %s", set, first.ID)
	}

	n := len(budgetAudits(t, logs))
	if _, err := svc.SetDefault(ctx, second.ID, "promoter"); err != nil {
		t.Fatal(err)
	}
	if got := len(budgetAudits(t, logs)); got != n {
		t.Errorf("idempotent promote wrote %d audit lines, want none", got-n)
	}
}

func TestBudgetService_SetDefault(t *testing.T) {
	t.Parallel()
	svc, repo, _, logs := newBudgetSvc(t)
	ctx := t.Context()
	a := mustCreate(t, svc, userBudgetReq("Standard"))
	b := mustCreate(t, svc, userBudgetReq("Premium"))

	resp, err := svc.SetDefault(ctx, a.ID, "promoter")
	if err != nil || !resp.IsDefault || resp.UpdatedBy != "promoter" {
		t.Fatalf("promote a = %+v, %v", resp, err)
	}
	if _, err := svc.SetDefault(ctx, b.ID, "promoter"); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.GetByID(ctx, a.ID); got.IsDefault {
		t.Error("promoting b left a as default")
	}

	audits := len(budgetAudits(t, logs))
	if resp, err := svc.SetDefault(ctx, b.ID, "again"); err != nil || !resp.IsDefault || resp.UpdatedBy != "promoter" {
		t.Errorf("re-promote b = %+v, %v; want the stored default, unstamped", resp, err)
	}
	if got := len(budgetAudits(t, logs)); got != audits {
		t.Errorf("re-promote wrote %d audit lines, want none", got-audits)
	}
	if _, err := svc.SetDefault(ctx, "not-a-uuid", "p"); !isNotFound(err) {
		t.Errorf("SetDefault(non-UUID) = %v, want NotFoundError", err)
	}
}

func TestBudgetService_List(t *testing.T) {
	t.Parallel()
	svc, _, _, _ := newBudgetSvc(t)
	ctx := t.Context()

	empty, err := svc.List(ctx, BudgetListQuery{PageNumber: 1, PageSize: 20})
	if err != nil || empty.Data == nil || len(empty.Data) != 0 {
		t.Fatalf("empty list = %+v, %v; want a non-nil empty slice so it serialises as []", empty, err)
	}

	for _, name := range []string{"alpha", "bravo", "charlie"} {
		mustCreate(t, svc, userBudgetReq(name))
	}
	mustCreate(t, svc, CreateBudgetRequest{Name: "mesh cap", SubjectType: store.BudgetSubjectMesh, AmountSet: true})

	page, err := svc.List(ctx, BudgetListQuery{PageNumber: 2, PageSize: 2, SubjectType: store.BudgetSubjectUser})
	if err != nil {
		t.Fatal(err)
	}
	p := page.Meta.Pagination
	if len(page.Data) != 1 || page.Data[0].Name != "charlie" || p.Count != 3 || p.PageNumber != 2 || p.PageSize != 2 {
		t.Errorf("page 2 of user budgets = %+v, meta %+v", page.Data, p)
	}
	if named, err := svc.List(ctx, BudgetListQuery{PageNumber: 1, PageSize: 20, Name: "CAP"}); err != nil || len(named.Data) != 1 || named.Data[0].Name != "mesh cap" {
		t.Errorf("name filter = %+v, %v", named, err)
	}

	for _, tier := range []string{"agent", "team"} {
		_, err := svc.List(ctx, BudgetListQuery{PageNumber: 1, PageSize: 20, SubjectType: tier})
		if details := validationDetails(t, err); len(details["subjectType"]) == 0 {
			t.Errorf("List(subjectType=%q): details = %v, want subjectType named", tier, details)
		}
	}
}

func TestBudgetService_SetDefaultLostRaceIsARetryableConflict(t *testing.T) {
	t.Parallel()
	svc, repo, db, _ := newBudgetSvc(t)
	if db.Dialect() != "postgres" {
		t.Skip("SQLite serialises writers; the race exists only on Postgres")
	}
	ctx := t.Context()
	incumbent := mustCreate(t, svc, userBudgetReq("Incumbent"))
	winner := mustCreate(t, svc, userBudgetReq("Winner"))
	loser := mustCreate(t, svc, userBudgetReq("Loser"))
	if _, err := svc.SetDefault(ctx, incumbent.ID, "p"); err != nil {
		t.Fatal(err)
	}

	tx, beginErr := db.BeginTx(ctx)
	if beginErr != nil {
		t.Fatal(beginErr)
	}
	defer func() { _ = tx.Rollback() }()
	for _, stmt := range []struct{ sql, id string }{
		{`UPDATE budgets SET is_default = 0 WHERE id = ?`, incumbent.ID},
		{`UPDATE budgets SET is_default = 1 WHERE id = ?`, winner.ID},
	} {
		if _, err := tx.ExecContext(ctx, stmt.sql, stmt.id); err != nil {
			t.Fatalf("staging transaction: %v", err)
		}
	}

	done := make(chan error, 1)
	go func() {
		_, err := svc.SetDefault(ctx, loser.ID, "loser")
		done <- err
	}()
	dbtest.WaitForLockWait(t, db, "UPDATE budgets SET is_default = 0")
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	msg := conflictMessage(t, <-done)
	if !strings.Contains(msg, "changed while this request was running") || !strings.Contains(msg, "Retry") {
		t.Errorf("lost race = %q, want the retryable concurrent-change message", msg)
	}
	if got, _ := repo.GetByID(ctx, winner.ID); !got.IsDefault {
		t.Error("the winner is not the default")
	}
}

func TestBudgetService_LogsOmitNames(t *testing.T) {
	t.Parallel()
	svc, _, db, logs := newBudgetSvc(t)
	ctx := t.Context()
	const canary, renamed = "canary-Ω-name", "canary-Ω-renamed"

	b := mustCreate(t, svc, userBudgetReq(canary))
	if _, err := svc.Update(ctx, b.ID, UpdateBudgetRequest{Name: new(renamed)}, "editor"); err != nil {
		t.Fatal(err)
	}
	var errs []error
	_, dupErr := svc.Create(ctx, userBudgetReq(renamed), "creator")
	errs = append(errs, dupErr)
	if _, err := svc.SetDefault(ctx, b.ID, "promoter"); err != nil {
		t.Fatal(err)
	}
	errs = append(errs, svc.Delete(ctx, b.ID, "deleter"))
	assigned := mustCreate(t, svc, userBudgetReq(canary+"-assigned"))
	if _, err := store.NewBudgetAssignmentsRepo(db).Create(ctx, &store.BudgetAssignment{
		SubjectType: store.BudgetSubjectUser, SubjectRef: seedAssignableUser(t, db, "a"), BudgetID: assigned.ID, CreatedBy: "x", UpdatedBy: "x",
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, assigned.ID, "deleter"); err != nil {
		t.Fatalf("delete the assigned budget: %v", err)
	}
	mustCreate(t, svc, CreateBudgetRequest{Name: canary + "-mesh", SubjectType: store.BudgetSubjectMesh, AmountSet: true})
	_, meshErr := svc.Create(ctx, CreateBudgetRequest{Name: "Second Mesh", SubjectType: store.BudgetSubjectMesh, AmountSet: true}, "creator")
	if msg := conflictMessage(t, meshErr); strings.Contains(msg, "canary-Ω") {
		t.Errorf("mesh conflict %q names the stored mesh budget to a caller who may not read it", msg)
	}

	for _, err := range errs {
		conflict, ok := errors.AsType[*ConflictError](err)
		if !ok {
			t.Fatalf("want a ConflictError, got %v", err)
		}
		if !strings.Contains(conflict.Message, "canary-Ω") {
			t.Errorf("response message %q should name the budget", conflict.Message)
		}
		if strings.Contains(conflict.Error(), "canary-Ω") {
			t.Errorf("logged error text %q names the budget", conflict.Error())
		}
	}
	if strings.Contains(logs.String(), "canary-Ω") {
		t.Errorf("budget name reached the logs:\n%s", logs.String())
	}
}

func TestBudgetService_Seeded(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	svc := NewBudgetService(store.NewBudgetsRepo(testOpenDB(t)), nil, slog.New(slog.NewJSONHandler(&logs, nil)))
	ctx := t.Context()

	users, err := svc.List(ctx, BudgetListQuery{PageNumber: 1, PageSize: 20, SubjectType: store.BudgetSubjectUser})
	if err != nil || len(users.Data) != 1 {
		t.Fatalf("user budgets = %+v, %v; want the seeded default alone", users, err)
	}
	if def := users.Data[0]; def.ID != store.SeededDefaultUserBudgetID || def.Name != "Default User" || !def.IsDefault || def.Amount != nil {
		t.Errorf("seeded user budget = %+v, want the unlimited Default User default", def)
	}
	meshes, err := svc.List(ctx, BudgetListQuery{PageNumber: 1, PageSize: 1, SubjectType: store.BudgetSubjectMesh})
	if err != nil || len(meshes.Data) != 1 || meshes.Data[0].Name != "Agent Mesh" || meshes.Data[0].Amount != nil || !meshes.Data[0].IsDefault {
		t.Fatalf("mesh budgets = %+v, %v; want the unlimited Agent Mesh default", meshes, err)
	}

	_, err = svc.Create(ctx, CreateBudgetRequest{Name: "Second Mesh", SubjectType: store.BudgetSubjectMesh, AmountSet: true}, "creator")
	if msg := conflictMessage(t, err); !strings.Contains(msg, "A mesh budget already exists") {
		t.Errorf("creating a mesh budget beside the seed = %q, want the mesh-exists 409", msg)
	}
	if err := svc.Delete(ctx, store.SeededDefaultUserBudgetID, "deleter"); !strings.Contains(conflictMessage(t, err), "default for the user tier") {
		t.Errorf("deleting the seeded default = %v, want the tier-default 409", err)
	}
	if err := svc.Delete(ctx, store.SeededMeshBudgetID, "deleter"); !strings.Contains(conflictMessage(t, err), "Agent Mesh budget can't be deleted") {
		t.Errorf("deleting the seeded mesh budget = %v, want the mesh 409", err)
	}

	standard := mustCreate(t, svc, userBudgetReq("Standard"))
	if _, err := svc.SetDefault(ctx, standard.ID, "promoter"); err != nil {
		t.Fatalf("promote over the seeded default: %v", err)
	}
	if err := svc.Delete(ctx, store.SeededDefaultUserBudgetID, "deleter"); err != nil {
		t.Errorf("delete the seeded budget once demoted: %v", err)
	}
	if _, err := svc.Update(ctx, store.SeededMeshBudgetID, UpdateBudgetRequest{Amount: new("5000"), AmountSet: true}, "editor"); err != nil {
		t.Errorf("cap the seeded mesh budget: %v", err)
	}
}

func TestBudgetService_UpdateRefusesThresholdOnUnlimited(t *testing.T) {
	t.Parallel()
	svc, repo, _, _ := newBudgetSvc(t)
	b := mustCreate(t, svc, CreateBudgetRequest{Name: "Exemption", SubjectType: store.BudgetSubjectUser, AmountSet: true})

	_, err := svc.Update(t.Context(), b.ID, UpdateBudgetRequest{WarningPercentage: new(80), WarningPercentageSet: true}, "editor")
	ve, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("threshold on an unlimited budget = %v, want a ValidationError", err)
	}
	for _, field := range []string{"amount", "warningPercentage"} {
		if len(ve.Details[field]) == 0 {
			t.Errorf("details %v do not name %s", ve.Details, field)
		}
	}
	if stored, _ := repo.GetByID(t.Context(), b.ID); stored == nil || stored.Amount != nil || stored.WarnPercentage != nil {
		t.Errorf("stored after the refused PATCH = %+v, want unchanged", stored)
	}
}
