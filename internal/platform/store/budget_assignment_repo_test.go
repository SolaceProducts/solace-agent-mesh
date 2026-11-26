// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

type assignmentFixture struct {
	db      *DB
	budgets *BudgetsRepo
	repo    *BudgetAssignmentsRepo
}

func budgetAssignmentRepoTest(t *testing.T, fn func(t *testing.T, f assignmentFixture)) {
	t.Helper()
	forEachMigratedPlatformDialect(t, func(t *testing.T, db *DB) {
		for _, ref := range []string{refA, refB, refC, refD} {
			seedUserWithID(t, db, ref)
		}
		fn(t, assignmentFixture{db: db, budgets: NewBudgetsRepo(db), repo: NewBudgetAssignmentsRepo(db)})
	})
}

func seedUserWithID(t *testing.T, db *DB, id string) {
	t.Helper()
	u := &User{ID: id, Issuer: "https://idp.test", Sub: "sub-" + id, ProvisionedVia: "jit", CreatedBy: "test", UpdatedBy: "test"}
	if err := NewUsersRepo(db).UpsertUser(t.Context(), u, nil); err != nil {
		t.Fatalf("seed user %s: %v", id, err)
	}
	if u.ID != id {
		t.Fatalf("seed user: got id %s, want %s", u.ID, id)
	}
}

func insertOrphanAssignment(t *testing.T, db *DB, ref, budgetID string) string {
	t.Helper()
	id := idgen.New()
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id, created_by, updated_by, created_time, updated_time)
		 VALUES (?, 'user', ?, ?, 'x', 'x', 1, 1)`, id, ref, budgetID); err != nil {
		t.Fatalf("insert orphan assignment: %v", err)
	}
	return id
}

const (
	refA = "0b5e3c1a-0000-4000-8000-00000000000a"
	refB = "0b5e3c1a-0000-4000-8000-00000000000b"
	refC = "0b5e3c1a-0000-4000-8000-00000000000c"
	refD = "0b5e3c1a-0000-4000-8000-00000000000d"
)

const systemUserIssuer = "sam:system"

func seedAssignmentUser(t *testing.T, db *DB, sub, provisionedVia string, displayName, email *string) string {
	t.Helper()
	u := &User{
		Issuer: "https://idp.test", Sub: sub, ProvisionedVia: provisionedVia,
		DisplayName: displayName, Email: email, CreatedBy: "test", UpdatedBy: "test",
	}
	if provisionedVia == provisionedViaSystem {
		u.Issuer = systemUserIssuer
	}
	if err := NewUsersRepo(db).UpsertUser(t.Context(), u, nil); err != nil {
		t.Fatalf("seed user %q: %v", sub, err)
	}
	return u.ID
}

func (f assignmentFixture) assign(t *testing.T, ref, budgetID string) *BudgetAssignment {
	t.Helper()
	a := &BudgetAssignment{
		SubjectType: BudgetSubjectUser, SubjectRef: ref, BudgetID: budgetID,
		CreatedBy: "assigner", UpdatedBy: "assigner",
	}
	if _, err := f.repo.Create(t.Context(), a); err != nil {
		t.Fatalf("assign %q: %v", ref, err)
	}
	return a
}

func (f assignmentFixture) mustGet(t *testing.T, id string) *BudgetAssignment {
	t.Helper()
	a, err := f.repo.GetByID(t.Context(), id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	if a == nil {
		t.Fatalf("get %s: not found", id)
	}
	return a
}

func (f assignmentFixture) meshBudget(t *testing.T) *Budget {
	t.Helper()
	b, err := f.budgets.GetByID(t.Context(), SeededMeshBudgetID)
	if err != nil || b == nil {
		t.Fatalf("read the seeded mesh budget: %v, %v", b, err)
	}
	return b
}

func assignmentRefs(as []*BudgetAssignment) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.SubjectRef)
	}
	return out
}

func TestBudgetAssignmentsRepo_CreateAndGet(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		budget := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})

		a := &BudgetAssignment{
			SubjectType: BudgetSubjectUser, SubjectRef: refA,
			BudgetID: strings.ToUpper(budget.ID), CreatedBy: "alice", UpdatedBy: "alice",
		}
		if _, err := f.repo.Create(ctx, a); err != nil {
			t.Fatalf("create: %v", err)
		}
		if a.ID == "" || a.CreatedTime == 0 || a.UpdatedTime != a.CreatedTime {
			t.Errorf("create did not fill id and times: %+v", a)
		}
		if a.BudgetID != budget.ID {
			t.Errorf("budget id stored as %q, want the canonical %q", a.BudgetID, budget.ID)
		}

		got := f.mustGet(t, strings.ToUpper(a.ID))
		want := *a
		want.State, want.BudgetName = BudgetAssignmentConcrete, "Engineering"
		want.SubjectSub, want.SubjectIssuer = "sub-"+refA, "https://idp.test"
		if *got != want {
			t.Errorf("got %+v\nwant %+v", *got, want)
		}

		for _, id := range []string{idgen.New(), "not-a-uuid"} {
			if miss, err := f.repo.GetByID(ctx, id); err != nil || miss != nil {
				t.Errorf("GetByID(%q) = %+v, %v; want nil, nil", id, miss, err)
			}
		}
	})
}

func TestBudgetAssignmentsRepo_SubjectFields(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		budget := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})

		named := seedAssignmentUser(t, f.db, "alice", "jit", new("Alice Smith"), new("alice@acme.test"))
		emailOnly := seedAssignmentUser(t, f.db, "bob", "jit", nil, new("bob@acme.test"))
		blankName := seedAssignmentUser(t, f.db, "carol", "jit", new(""), new("carol@acme.test"))
		blankEmail := seedAssignmentUser(t, f.db, "erin", "jit", new("Erin"), new(""))
		anonymous := seedAssignmentUser(t, f.db, "dave", "jit", nil, nil)
		bot := seedAssignmentUser(t, f.db, "system:ci-bot", provisionedViaSystem, new("CI bot"), nil)

		builtin, _, err := NewUsersRepo(f.db).GetByIssuerSub(t.Context(), systemUserIssuer, "system:default")
		if err != nil || builtin == nil {
			t.Fatalf("seeded system:default: %v, %v", builtin, err)
		}

		type subject struct {
			name, email *string
			system      bool
		}
		read := func(t *testing.T, id string) subject {
			t.Helper()
			a := f.mustGet(t, id)
			return subject{a.SubjectDisplayName, a.SubjectEmail, a.SubjectIsSystemUser}
		}
		same := func(a, b subject) bool {
			eq := func(x, y *string) bool { return (x == nil) == (y == nil) && (x == nil || *x == *y) }
			return eq(a.name, b.name) && eq(a.email, b.email) && a.system == b.system
		}
		show := func(s subject) string {
			return "{name " + derefString(s.name) + ", email " + derefString(s.email) + ", system " + map[bool]string{true: "true", false: "false"}[s.system] + "}"
		}

		cases := []struct {
			name, ref string
			want      subject
		}{
			{"name and email", named, subject{new("Alice Smith"), new("alice@acme.test"), false}},
			{"email only", emailOnly, subject{nil, new("bob@acme.test"), false}},
			{"blank name", blankName, subject{nil, new("carol@acme.test"), false}},
			{"blank email", blankEmail, subject{new("Erin"), nil, false}},
			{"neither", anonymous, subject{}},
			{"custom system user", bot, subject{new("CI bot"), nil, true}},
			{"seeded built-in", builtin.ID, subject{new("Default"), nil, true}},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				if got := read(t, f.assign(t, c.ref, budget.ID).ID); !same(got, c.want) {
					t.Errorf("subject = %s, want %s", show(got), show(c.want))
				}
			})
		}
		for name, ref := range map[string]string{
			"a sub stored as the ref": "system:ci-bot",
			"no users row":            "3f2b8c1e-5d4a-4e6f-9a7b-1c2d3e4f5a6b",
		} {
			t.Run(name, func(t *testing.T) {
				if got := read(t, insertOrphanAssignment(t, f.db, ref, budget.ID)); !same(got, subject{}) {
					t.Errorf("subject = %s, want none", show(got))
				}
			})
		}
	})
}

func TestBudgetAssignmentsRepo_CreateRefusals(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		budget := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		other := createBudget(t, f.budgets, &Budget{Name: "Premium", Amount: new(int64(200))})
		mesh := f.meshBudget(t)
		const ref = refD
		first := f.assign(t, ref, budget.ID)

		cases := []struct {
			name string
			a    BudgetAssignment
			want error
		}{
			{"second assignment for a subject", BudgetAssignment{SubjectRef: ref, BudgetID: other.ID}, ErrBudgetAssignmentExists},
			{"unknown budget", BudgetAssignment{SubjectRef: refA, BudgetID: idgen.New()}, ErrBudgetAssignmentNoBudget},
			{"budget at another tier", BudgetAssignment{SubjectRef: refB, BudgetID: mesh.ID}, ErrBudgetAssignmentNoBudget},
			{"budget id not a UUID", BudgetAssignment{SubjectRef: refC, BudgetID: "nope"}, ErrBudgetAssignmentNoBudget},
			{"no users row", BudgetAssignment{SubjectRef: idgen.New(), BudgetID: budget.ID}, ErrBudgetAssignmentNoSubject},
			{"a sub, not an id", BudgetAssignment{SubjectRef: "system:default", BudgetID: budget.ID}, ErrBudgetAssignmentNoSubject},
			{"an id not in canonical form", BudgetAssignment{SubjectRef: strings.ToUpper(refA), BudgetID: budget.ID}, ErrBudgetAssignmentNoSubject},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				a := c.a
				a.SubjectType, a.CreatedBy, a.UpdatedBy = BudgetSubjectUser, "x", "x"
				if _, err := f.repo.Create(ctx, &a); !errors.Is(err, c.want) {
					t.Errorf("Create = %v, want %v", err, c.want)
				}
			})
		}

		t.Run("primary key collision stays unclassified", func(t *testing.T) {
			dup := BudgetAssignment{ID: first.ID, SubjectType: BudgetSubjectUser, SubjectRef: refC,
				BudgetID: budget.ID, CreatedBy: "x", UpdatedBy: "x"}
			_, err := f.repo.Create(ctx, &dup)
			if err == nil || errors.Is(err, ErrBudgetAssignmentExists) || errors.Is(err, ErrBudgetAssignmentNoBudget) {
				t.Errorf("Create = %v, want an unclassified error", err)
			}
		})
	})
}

func TestBudgetAssignmentsRepo_ListOrdersByShownName(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})

		bob := seedAssignmentUser(t, f.db, "bob", "jit", new("bob"), new("a-first@acme.test"))
		aliceLater := seedAssignmentUser(t, f.db, "alice-2", "jit", new("Alice"), nil)
		aliceEarlier := seedAssignmentUser(t, f.db, "alice-1", "jit", new("ALICE"), nil)
		carol := seedAssignmentUser(t, f.db, "carol", "jit", new(""), new("carol@acme.test"))
		for _, spec := range []struct{ ref, id string }{
			{bob, "00000000-0000-7000-8000-000000000001"},
			{carol, "00000000-0000-7000-8000-000000000002"},
			{aliceLater, "00000000-0000-7000-8000-000000000005"},
			{aliceEarlier, "00000000-0000-7000-8000-000000000004"},
			{refA, "00000000-0000-7000-8000-000000000003"},
		} {
			a := &BudgetAssignment{ID: spec.id, SubjectType: BudgetSubjectUser, SubjectRef: spec.ref,
				BudgetID: eng.ID, CreatedBy: "x", UpdatedBy: "x"}
			if _, err := f.repo.Create(ctx, a); err != nil {
				t.Fatalf("create %s: %v", spec.ref, err)
			}
		}

		as, err := f.repo.List(ctx, BudgetAssignmentFilter{BudgetID: eng.ID})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{refA, aliceEarlier, aliceLater, bob, carol}
		if got := assignmentRefs(as); !slices.Equal(got, want) {
			t.Errorf("list = %v, want %v", got, want)
		}
	})
}

func TestBudgetAssignmentsRepo_ListAndCount(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		ops := createBudget(t, f.budgets, &Budget{Name: "Operations", Amount: new(int64(100))})

		for _, spec := range []struct {
			ref, budget, id string
			created         int64
		}{
			{refA, eng.ID, "00000000-0000-7000-8000-000000000004", 1001},
			{refB, ops.ID, "00000000-0000-7000-8000-000000000003", 1000},
			{refC, eng.ID, "00000000-0000-7000-8000-000000000001", 1000},
			{refD, eng.ID, "00000000-0000-7000-8000-000000000002", 1001},
		} {
			a := &BudgetAssignment{ID: spec.id, SubjectType: BudgetSubjectUser, SubjectRef: spec.ref,
				BudgetID: spec.budget, CreatedBy: "x", UpdatedBy: "x", CreatedTime: spec.created}
			if _, err := f.repo.Create(ctx, a); err != nil {
				t.Fatalf("create %s: %v", spec.ref, err)
			}
		}
		wantOrder := []string{refA, refB, refC, refD}

		list := func(filter BudgetAssignmentFilter) []string {
			t.Helper()
			as, err := f.repo.List(ctx, filter)
			if err != nil {
				t.Fatalf("list %+v: %v", filter, err)
			}
			return assignmentRefs(as)
		}
		count := func(filter BudgetAssignmentFilter) int64 {
			t.Helper()
			n, err := f.repo.Count(ctx, filter)
			if err != nil {
				t.Fatalf("count %+v: %v", filter, err)
			}
			return n
		}

		if got := list(BudgetAssignmentFilter{}); !slices.Equal(got, wantOrder) {
			t.Errorf("list all = %v, want %v", got, wantOrder)
		}
		var paged []string
		for offset := 0; offset < len(wantOrder); offset += 3 {
			paged = append(paged, list(BudgetAssignmentFilter{Limit: 3, Offset: offset})...)
		}
		if !slices.Equal(paged, wantOrder) {
			t.Errorf("pages = %v, want %v", paged, wantOrder)
		}

		engRefs := []string{refA, refC, refD}
		if got := list(BudgetAssignmentFilter{BudgetID: strings.ToUpper(eng.ID)}); !slices.Equal(got, engRefs) {
			t.Errorf("by budget = %v, want %v", got, engRefs)
		}
		if got := list(BudgetAssignmentFilter{SubjectRef: refB}); !slices.Equal(got, []string{refB}) {
			t.Errorf("by subject = %v, want [%s]", got, refB)
		}
		if got := list(BudgetAssignmentFilter{BudgetID: eng.ID, SubjectRef: refB}); len(got) != 0 {
			t.Errorf("by budget and another budget's subject = %v, want none", got)
		}
		if got := list(BudgetAssignmentFilter{BudgetID: "not-a-uuid"}); len(got) != 0 {
			t.Errorf("by a non-UUID budget = %v, want none", got)
		}

		for _, c := range []struct {
			filter BudgetAssignmentFilter
			want   int64
		}{
			{BudgetAssignmentFilter{Limit: 1, Offset: 1}, 4},
			{BudgetAssignmentFilter{BudgetID: eng.ID}, 3},
			{BudgetAssignmentFilter{SubjectRef: refB}, 1},
			{BudgetAssignmentFilter{BudgetID: "not-a-uuid"}, 0},
		} {
			if got := count(c.filter); got != c.want {
				t.Errorf("count %+v = %d, want %d", c.filter, got, c.want)
			}
		}
	})
}

func TestBudgetAssignmentsRepo_UpdateMoves(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		ops := createBudget(t, f.budgets, &Budget{Name: "Operations", Amount: new(int64(100))})
		mesh := f.meshBudget(t)
		a := f.assign(t, refA, eng.ID)

		res, err := f.repo.Update(ctx, strings.ToUpper(a.ID), BudgetAssignmentUpdate{BudgetID: strings.ToUpper(ops.ID), Actor: "bob", NowMs: a.CreatedTime + 5})
		if err != nil || res.PreviousBudgetID != eng.ID || !res.Moved || res.Rerecorded {
			t.Fatalf("reassign = %+v, %v; want moved from %s", res, err, eng.ID)
		}
		got := f.mustGet(t, a.ID)
		if got.BudgetID != ops.ID || got.BudgetName != "Operations" {
			t.Errorf("after reassign: budget %s %q, want %s Operations", got.BudgetID, got.BudgetName, ops.ID)
		}
		if got.UpdatedBy != "bob" || got.UpdatedTime != a.CreatedTime+5 {
			t.Errorf("after reassign: updated %s at %d, want bob at %d", got.UpdatedBy, got.UpdatedTime, a.CreatedTime+5)
		}
		if got.CreatedBy != a.CreatedBy || got.CreatedTime != a.CreatedTime || got.SubjectRef != a.SubjectRef {
			t.Errorf("reassign changed immutable fields: %+v", got)
		}

		res, err = f.repo.Update(ctx, a.ID, BudgetAssignmentUpdate{BudgetID: ops.ID, Actor: "idle", NowMs: a.CreatedTime + 9})
		if err != nil || res.PreviousBudgetID != ops.ID || res.Moved {
			t.Errorf("no-op reassign = %+v, %v; want unmoved from %s", res, err, ops.ID)
		}
		if after := f.mustGet(t, a.ID); after.UpdatedBy != "bob" || after.UpdatedTime != a.CreatedTime+5 {
			t.Errorf("a no-op reassign wrote: %+v", after)
		}

		refusals := []struct {
			name, id, budgetID string
			want               error
		}{
			{"budget at another tier", a.ID, mesh.ID, ErrBudgetAssignmentNoBudget},
			{"unknown budget", a.ID, idgen.New(), ErrBudgetAssignmentNoBudget},
			{"budget id not a UUID", a.ID, "nope", ErrBudgetAssignmentNoBudget},
			{"unknown assignment", idgen.New(), eng.ID, ErrBudgetAssignmentNotFound},
			{"assignment id not a UUID", "nope", eng.ID, ErrBudgetAssignmentNotFound},
		}
		for _, c := range refusals {
			t.Run(c.name, func(t *testing.T) {
				if _, err := f.repo.Update(ctx, c.id, BudgetAssignmentUpdate{BudgetID: c.budgetID, Actor: "eve", NowMs: 1}); !errors.Is(err, c.want) {
					t.Errorf("Update = %v, want %v", err, c.want)
				}
			})
		}
		if after := f.mustGet(t, a.ID); after.BudgetID != ops.ID || after.UpdatedBy != "bob" {
			t.Errorf("a refused reassign wrote: %+v", after)
		}
	})
}

func TestBudgetAssignmentsRepo_UpdateRerecordsTheIdentity(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		ops := createBudget(t, f.budgets, &Budget{Name: "Operations", Amount: new(int64(100))})
		a := f.assign(t, refA, eng.ID)
		madeBy := BudgetIdentity{Type: BudgetIdentitySub, Value: "sub-" + refA, Issuer: "https://idp.test"}
		declared := BudgetIdentity{Type: BudgetIdentityEmail, Value: "a@new.test"}

		res, err := f.repo.Update(ctx, a.ID, BudgetAssignmentUpdate{Identity: &declared, Actor: "apply", NowMs: a.CreatedTime + 5})
		if err != nil || !res.Rerecorded || res.Moved || res.PreviousIdentity != madeBy || res.PreviousBudgetID != eng.ID {
			t.Fatalf("re-record = %+v, %v; want re-recorded from %+v and left on Engineering", res, err, madeBy)
		}
		got := f.mustGet(t, a.ID)
		if (BudgetIdentity{Type: got.IdentityType, Value: got.IdentityValue, Issuer: got.IdentityIssuer}) != declared {
			t.Errorf("recorded identity = %s %s %s, want %+v", got.IdentityType, got.IdentityValue, got.IdentityIssuer, declared)
		}
		if got.BudgetID != eng.ID || got.SubjectRef != refA || got.UpdatedBy != "apply" || got.UpdatedTime != a.CreatedTime+5 {
			t.Errorf("after re-record: %+v", got)
		}

		res, err = f.repo.Update(ctx, a.ID, BudgetAssignmentUpdate{Identity: &declared, Actor: "idle", NowMs: a.CreatedTime + 9})
		if err != nil || res.Rerecorded || res.Moved {
			t.Errorf("re-recording the same identity = %+v, %v; want nothing written", res, err)
		}
		if after := f.mustGet(t, a.ID); after.UpdatedBy != "apply" {
			t.Errorf("a no-op re-record wrote: %+v", after)
		}

		both := BudgetIdentity{Type: BudgetIdentitySub, Value: "sub-" + refA, Issuer: "https://idp.test"}
		res, err = f.repo.Update(ctx, a.ID, BudgetAssignmentUpdate{BudgetID: ops.ID, Identity: &both, Actor: "apply", NowMs: a.CreatedTime + 12})
		if err != nil || !res.Moved || !res.Rerecorded || res.PreviousIdentity != declared {
			t.Errorf("move and re-record = %+v, %v; want both", res, err)
		}
		if after := f.mustGet(t, a.ID); after.BudgetID != ops.ID || after.IdentityType != BudgetIdentitySub {
			t.Errorf("after move and re-record: %+v", after)
		}

		p := f.pend(t, BudgetIdentityEmail, "later@x.test", "", eng.ID)
		if _, err := f.repo.Update(ctx, p.ID, BudgetAssignmentUpdate{Identity: &declared, Actor: "apply", NowMs: 1}); !errors.Is(err, ErrBudgetAssignmentPendingIdentity) {
			t.Errorf("re-recording a pending assignment = %v, want ErrBudgetAssignmentPendingIdentity", err)
		}
		if _, err := f.repo.Update(ctx, p.ID, BudgetAssignmentUpdate{BudgetID: ops.ID, Identity: &declared, Actor: "apply", NowMs: 1}); !errors.Is(err, ErrBudgetAssignmentPendingIdentity) {
			t.Errorf("moving and re-recording a pending assignment = %v, want ErrBudgetAssignmentPendingIdentity", err)
		}
		if after, err := f.repo.GetByID(ctx, p.ID); err != nil || after == nil || after.BudgetID != eng.ID || after.IdentityValue != "later@x.test" {
			t.Errorf("a refused write to a pending assignment wrote: %+v, %v", after, err)
		}
	})
}

func TestBudgetAssignmentsRepo_Delete(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		budget := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		a := f.assign(t, refA, budget.ID)
		kept := f.assign(t, refB, budget.ID)

		if from, err := f.repo.Delete(ctx, strings.ToUpper(a.ID)); err != nil || from != budget.ID {
			t.Fatalf("delete = %s, %v; want %s, nil", from, err, budget.ID)
		}
		if got, err := f.repo.GetByID(ctx, a.ID); err != nil || got != nil {
			t.Errorf("after delete: %+v, %v; want nil, nil", got, err)
		}
		f.mustGet(t, kept.ID)

		for _, id := range []string{a.ID, idgen.New(), "nope"} {
			if _, err := f.repo.Delete(ctx, id); !errors.Is(err, ErrBudgetAssignmentNotFound) {
				t.Errorf("Delete(%q) = %v, want ErrBudgetAssignmentNotFound", id, err)
			}
		}
	})
}

func TestBudgetAssignmentsRepo_SearchAndSubjectType(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		ops := createBudget(t, f.budgets, &Budget{Name: "Operations", Amount: new(int64(100))})

		alice := seedAssignmentUser(t, f.db, "alice", "jit", new("Alice Smith"), new("alice@acme.test"))
		bob := seedAssignmentUser(t, f.db, "bob", "jit", new("Bob"), new("bob@corp.test"))
		percent := seedAssignmentUser(t, f.db, "pct", "jit", new("100% Bot"), nil)
		slash := seedAssignmentUser(t, f.db, "slash", "jit", new(`back\slash`), nil)
		f.assign(t, alice, eng.ID)
		f.assign(t, bob, eng.ID)
		f.assign(t, percent, eng.ID)
		f.assign(t, slash, ops.ID)
		f.assign(t, refA, eng.ID)

		for _, c := range []struct {
			name   string
			filter BudgetAssignmentFilter
			want   []string
		}{
			{"name, folded", BudgetAssignmentFilter{Search: "SMITH"}, []string{alice}},
			{"email", BudgetAssignmentFilter{Search: "@corp"}, []string{bob}},
			{"blank is no filter", BudgetAssignmentFilter{BudgetID: eng.ID, Search: "  "}, []string{refA, percent, alice, bob}},
			{"percent is literal", BudgetAssignmentFilter{Search: "100%"}, []string{percent}},
			{"underscore is literal", BudgetAssignmentFilter{Search: "_"}, nil},
			{"backslash is literal", BudgetAssignmentFilter{Search: `k\s`}, []string{slash}},
			{"within a budget", BudgetAssignmentFilter{BudgetID: ops.ID, Search: "a"}, []string{slash}},
			{"user tier", BudgetAssignmentFilter{SubjectType: BudgetSubjectUser, BudgetID: ops.ID}, []string{slash}},
			{"mesh tier holds none", BudgetAssignmentFilter{SubjectType: BudgetSubjectMesh}, nil},
		} {
			t.Run(c.name, func(t *testing.T) {
				as, err := f.repo.List(ctx, c.filter)
				if err != nil {
					t.Fatal(err)
				}
				if got := assignmentRefs(as); !slices.Equal(got, c.want) && (len(got) != 0 || len(c.want) != 0) {
					t.Errorf("list = %v, want %v", got, c.want)
				}
				n, err := f.repo.Count(ctx, c.filter)
				if err != nil {
					t.Fatal(err)
				}
				if n != int64(len(c.want)) {
					t.Errorf("count = %d, want %d", n, len(c.want))
				}
			})
		}
	})
}

func TestBudgetAssignmentsRepo_CreateWaitsForAUserDelete(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		if f.db.Dialect() != "postgres" {
			t.Skip("SQLite serialises writes on one connection")
		}
		ctx := t.Context()
		budget := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		victim := seedAssignmentUser(t, f.db, "system:doomed", provisionedViaSystem, nil, nil)

		tx, err := f.db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, victim); err != nil {
			t.Fatal(err)
		}

		done := make(chan error, 1)
		go func() {
			_, err := f.repo.Create(ctx, &BudgetAssignment{
				SubjectType: BudgetSubjectUser, SubjectRef: victim, BudgetID: budget.ID, CreatedBy: "x", UpdatedBy: "x",
			})
			done <- err
		}()
		dbtest.WaitForLockWait(t, f.db, "SELECT id, email, sub, issuer, provisioned_via FROM users WHERE id IN")
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}

		if err := <-done; !errors.Is(err, ErrBudgetAssignmentNoSubject) {
			t.Errorf("Create racing the user's delete = %v, want ErrBudgetAssignmentNoSubject", err)
		}
		if n, _ := f.repo.Count(ctx, BudgetAssignmentFilter{SubjectRef: victim}); n != 0 {
			t.Errorf("the race left %d orphaned assignments", n)
		}
	})
}

func TestBudgetAssignmentsRepo_HiddenSubjectsAreNotCountedOrListed(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		hiding := NewBudgetsRepo(f.db, refA)
		budget := createBudget(t, hiding, &Budget{Name: "Engineering", Amount: new(int64(100))})
		for _, ref := range []string{refA, refB} {
			if _, err := f.repo.Create(ctx, &BudgetAssignment{SubjectType: BudgetSubjectUser, SubjectRef: ref, BudgetID: budget.ID, CreatedBy: "x", UpdatedBy: "x"}); err != nil {
				t.Fatal(err)
			}
		}

		byID, err := hiding.GetByID(ctx, budget.ID)
		if err != nil {
			t.Fatal(err)
		}
		byName, err := hiding.GetByName(ctx, budget.Name)
		if err != nil {
			t.Fatal(err)
		}
		listed, err := hiding.List(ctx, BudgetFilter{SubjectType: BudgetSubjectUser, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(listed, func(b *Budget) bool { return b.ID == budget.ID })
		if byID.AssignedCount != 1 || byName.AssignedCount != 1 || i < 0 || listed[i].AssignedCount != 1 {
			t.Errorf("assigned counts = %d by id, %d by name, %v listed; want 1, leaving out the hidden subject", byID.AssignedCount, byName.AssignedCount, listed)
		}
		if byID.HiddenAssignedCount != 1 || byName.HiddenAssignedCount != 1 || listed[i].HiddenAssignedCount != 1 {
			t.Errorf("hidden counts = %d by id, %d by name, %d listed; want the hidden subject counted apart", byID.HiddenAssignedCount, byName.HiddenAssignedCount, listed[i].HiddenAssignedCount)
		}

		filter := BudgetAssignmentFilter{BudgetID: budget.ID, HiddenSubjectRefs: []string{refA}}
		rows, err := f.repo.List(ctx, filter)
		if err != nil {
			t.Fatal(err)
		}
		n, err := f.repo.Count(ctx, filter)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].SubjectRef != refB || n != 1 {
			t.Errorf("listed %d rows, counted %d; want only refB's", len(rows), n)
		}

		if _, err := f.repo.Delete(ctx, rows[0].ID); err != nil {
			t.Fatal(err)
		}
		if deleted, err := hiding.Delete(ctx, budget.ID); err != nil || len(deleted.Assignments) != 1 {
			t.Errorf("delete with only the hidden assignment left = %+v, %v; want it deleted with that assignment", deleted, err)
		}
	})
}

func TestBudgetAssignmentsRepo_ApplyBudgetChangesWaitsForAUserDelete(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		if f.db.Dialect() != "postgres" {
			t.Skip("SQLite serialises writes on one connection")
		}
		ctx := t.Context()
		budget := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		victim := seedAssignmentUser(t, f.db, "system:doomed", provisionedViaSystem, nil, nil)
		survivor := seedAssignmentUser(t, f.db, "system:survivor", provisionedViaSystem, nil, nil)

		tx, err := f.db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, victim); err != nil {
			t.Fatal(err)
		}

		done := make(chan error, 1)
		go func() {
			_, err := f.repo.ApplyBudgetChanges(ctx, BudgetAssignmentChanges{
				BudgetID: budget.ID, SubjectType: BudgetSubjectUser, Add: []string{victim, survivor}, Actor: "x", NowMs: 1,
			})
			done <- err
		}()
		dbtest.WaitForLockWait(t, f.db, "SELECT id, email, sub, issuer, provisioned_via FROM users WHERE id IN")
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}

		var missing *BudgetAssignmentMissingSubjectsError
		if err := <-done; !errors.As(err, &missing) || !slices.Equal(missing.Refs, []string{victim}) {
			t.Errorf("save racing the user's delete = %v, want a missing-subjects error naming only the deleted user", err)
		}
		if n, _ := f.repo.Count(ctx, BudgetAssignmentFilter{BudgetID: budget.ID}); n != 0 {
			t.Errorf("the refused save left %d assignments", n)
		}
	})
}

func TestBudgetAssignmentsRepo_UpdateReportsTheBudgetItLocked(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		if f.db.Dialect() != "postgres" {
			t.Skip("SQLite serialises writes on one connection")
		}
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		ops := createBudget(t, f.budgets, &Budget{Name: "Operations", Amount: new(int64(100))})
		sales := createBudget(t, f.budgets, &Budget{Name: "Sales", Amount: new(int64(100))})
		a := f.assign(t, refA, eng.ID)

		tx, err := f.db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, `UPDATE budget_assignments SET budget_id = ? WHERE id = ?`, ops.ID, a.ID); err != nil {
			t.Fatal(err)
		}

		type result struct {
			previous string
			err      error
		}
		done := make(chan result, 1)
		go func() {
			res, err := f.repo.Update(ctx, a.ID, BudgetAssignmentUpdate{BudgetID: sales.ID, Actor: "racer", NowMs: 2})
			done <- result{res.PreviousBudgetID, err}
		}()
		dbtest.WaitForLockWait(t, f.db, "SELECT budget_id FROM budget_assignments")
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}

		if got := <-done; got.err != nil || got.previous != ops.ID {
			t.Errorf("move racing another = %s, %v; want %s (Operations), nil", got.previous, got.err, ops.ID)
		}
	})
}

func TestBudgetAssignmentsRepo_ApplyBudgetChanges(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		ops := createBudget(t, f.budgets, &Budget{Name: "Operations", Amount: new(int64(100))})
		alice := seedAssignmentUser(t, f.db, "alice", "jit", nil, nil)
		moving := f.assign(t, refA, ops.ID)
		leaving := f.assign(t, refB, eng.ID)
		staying := f.assign(t, refC, eng.ID)
		elsewhere := f.assign(t, refD, ops.ID)
		nowhere := "3f2b8c1e-5d4a-4e6f-9a7b-1c2d3e4f5a6b"

		got, err := f.repo.ApplyBudgetChanges(ctx, BudgetAssignmentChanges{
			BudgetID: strings.ToUpper(eng.ID), SubjectType: BudgetSubjectUser,
			Add:    []string{alice, refA, refC},
			Remove: []string{refB, refD, nowhere},
			Actor:  "saver", NowMs: 4242,
		})
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		added := got[0].AssignmentID
		want := []BudgetAssignmentChange{
			{Outcome: BudgetAssignmentAdded, SubjectRef: alice, AssignmentID: added},
			{Outcome: BudgetAssignmentMoved, SubjectRef: refA, AssignmentID: moving.ID, FromBudgetID: ops.ID, FromBudgetName: "Operations"},
			{Outcome: BudgetAssignmentUnchanged, SubjectRef: refC, AssignmentID: staying.ID, Reason: BudgetAssignmentAlreadyAssigned},
			{Outcome: BudgetAssignmentRemoved, SubjectRef: refB, AssignmentID: leaving.ID},
			{Outcome: BudgetAssignmentUnchanged, SubjectRef: refD, AssignmentID: elsewhere.ID, Reason: BudgetAssignmentNotAssigned},
			{Outcome: BudgetAssignmentUnchanged, SubjectRef: nowhere, Reason: BudgetAssignmentNotAssigned},
		}
		if !reflect.DeepEqual(got, want) || added == "" {
			t.Errorf("changes =\n%+v\nwant\n%+v", got, want)
		}

		state := map[string]string{}
		as, err := f.repo.List(ctx, BudgetAssignmentFilter{})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range as {
			state[a.SubjectRef] = a.BudgetID
		}
		wantState := map[string]string{alice: eng.ID, refA: eng.ID, refC: eng.ID, refD: ops.ID}
		if !maps.Equal(state, wantState) {
			t.Errorf("after the save: %v, want %v", state, wantState)
		}
		if a := f.mustGet(t, added); a.CreatedBy != "saver" || a.CreatedTime != 4242 || a.UpdatedTime != 4242 {
			t.Errorf("added row = %+v, want saver at 4242", a)
		}
		if a := f.mustGet(t, moving.ID); a.UpdatedBy != "saver" || a.UpdatedTime != 4242 || a.CreatedBy != moving.CreatedBy {
			t.Errorf("moved row = %+v, want updated by saver at 4242", a)
		}
		if a := f.mustGet(t, staying.ID); a.UpdatedBy != staying.UpdatedBy || a.UpdatedTime != staying.UpdatedTime {
			t.Errorf("an unchanged row was written: %+v", a)
		}

		t.Run("nothing to do", func(t *testing.T) {
			got, err := f.repo.ApplyBudgetChanges(ctx, BudgetAssignmentChanges{BudgetID: eng.ID, SubjectType: BudgetSubjectUser})
			if err != nil || got == nil || len(got) != 0 {
				t.Errorf("empty save = %v, %v; want an empty, non-nil result", got, err)
			}
		})
	})
}

func TestBudgetAssignmentsRepo_ApplyBudgetChangesRefusals(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		kept := f.assign(t, refB, eng.ID)
		ghost1, ghost2 := idgen.New(), idgen.New()

		_, err := f.repo.ApplyBudgetChanges(ctx, BudgetAssignmentChanges{
			BudgetID: eng.ID, SubjectType: BudgetSubjectUser,
			Add: []string{refA, ghost1, ghost2}, Remove: []string{refB}, Actor: "x", NowMs: 1,
		})
		missing, ok := errors.AsType[*BudgetAssignmentMissingSubjectsError](err)
		if !ok || !slices.Equal(missing.Refs, []string{ghost1, ghost2}) || !errors.Is(err, ErrBudgetAssignmentNoSubject) {
			t.Fatalf("save with missing users = %v, want both named in order", err)
		}
		if n, _ := f.repo.Count(ctx, BudgetAssignmentFilter{SubjectRef: refA}); n != 0 {
			t.Error("a refused save added an assignment")
		}
		f.mustGet(t, kept.ID)

		for name, budgetID := range map[string]string{"unknown budget": idgen.New(), "budget id not a UUID": "nope"} {
			t.Run(name, func(t *testing.T) {
				_, err := f.repo.ApplyBudgetChanges(ctx, BudgetAssignmentChanges{
					BudgetID: budgetID, SubjectType: BudgetSubjectUser, Add: []string{refA}, Actor: "x", NowMs: 1,
				})
				if !errors.Is(err, ErrBudgetAssignmentNoBudget) {
					t.Errorf("save = %v, want ErrBudgetAssignmentNoBudget", err)
				}
			})
		}
	})
}

func TestBudgetAssignmentsRepo_ApplyBudgetChangesLosesARace(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		if f.db.Dialect() != "postgres" {
			t.Skip("SQLite serialises writes on one connection")
		}
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		ops := createBudget(t, f.budgets, &Budget{Name: "Operations", Amount: new(int64(100))})

		tx, err := f.db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id, created_by, updated_by, created_time, updated_time)
			 VALUES (?, 'user', ?, ?, 'x', 'x', 1, 1)`, idgen.New(), refA, ops.ID); err != nil {
			t.Fatal(err)
		}

		done := make(chan error, 1)
		go func() {
			_, err := f.repo.ApplyBudgetChanges(ctx, BudgetAssignmentChanges{
				BudgetID: eng.ID, SubjectType: BudgetSubjectUser, Add: []string{refB, refA}, Actor: "x", NowMs: 1,
			})
			done <- err
		}()
		dbtest.WaitForLockWait(t, f.db, "INSERT INTO budget_assignments")
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if err := <-done; !errors.Is(err, ErrBudgetAssignmentConflict) {
			t.Errorf("save racing a create = %v, want ErrBudgetAssignmentConflict", err)
		}
		if n, _ := f.repo.Count(ctx, BudgetAssignmentFilter{SubjectRef: refB}); n != 0 {
			t.Error("the losing save kept part of its work")
		}
	})
}

func TestBudgetAssignmentsRepo_ApplyBudgetChangesReportsTheLockedBudget(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		if f.db.Dialect() != "postgres" {
			t.Skip("SQLite serialises writes on one connection")
		}
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		ops := createBudget(t, f.budgets, &Budget{Name: "Operations", Amount: new(int64(100))})
		sales := createBudget(t, f.budgets, &Budget{Name: "Sales", Amount: new(int64(100))})
		a := f.assign(t, refA, ops.ID)

		tx, err := f.db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, `UPDATE budget_assignments SET budget_id = ? WHERE id = ?`, sales.ID, a.ID); err != nil {
			t.Fatal(err)
		}

		type result struct {
			changes []BudgetAssignmentChange
			err     error
		}
		done := make(chan result, 1)
		go func() {
			changes, err := f.repo.ApplyBudgetChanges(ctx, BudgetAssignmentChanges{
				BudgetID: eng.ID, SubjectType: BudgetSubjectUser, Add: []string{refA}, Actor: "x", NowMs: 1,
			})
			done <- result{changes, err}
		}()
		dbtest.WaitForLockWait(t, f.db, "SELECT id, subject_ref, budget_id FROM budget_assignments")
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		got := <-done
		if got.err != nil || len(got.changes) != 1 || got.changes[0].FromBudgetID != sales.ID || got.changes[0].FromBudgetName != "Sales" {
			t.Errorf("save racing a move = %+v, %v; want moved from Sales", got.changes, got.err)
		}
	})
}

func TestClassifyBudgetAssignmentErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		err        error
		write      error
		bulkChange error
	}{
		{"postgres subject constraint", &pgconn.PgError{Code: "23505", ConstraintName: "uq_budget_assignments_subject"}, ErrBudgetAssignmentExists, ErrBudgetAssignmentConflict},
		{"sqlite subject constraint", errors.New("UNIQUE constraint failed: budget_assignments.subject_type, budget_assignments.subject_ref (2067)"), ErrBudgetAssignmentExists, ErrBudgetAssignmentConflict},
		{"postgres primary key", &pgconn.PgError{Code: "23505", ConstraintName: "budget_assignments_pkey"}, nil, nil},
		{"sqlite primary key", errors.New("UNIQUE constraint failed: budget_assignments.id (1555)"), nil, nil},
		{"postgres deadlock", &pgconn.PgError{Code: "40P01"}, nil, ErrBudgetAssignmentConflict},
		{"postgres foreign key", &pgconn.PgError{Code: "23503"}, ErrBudgetAssignmentNoBudget, ErrBudgetAssignmentNoBudget},
		{"anything else", errors.New("connection reset"), nil, nil},
	}
	sentinels := []error{ErrBudgetAssignmentExists, ErrBudgetAssignmentConflict, ErrBudgetAssignmentNoBudget}
	check := func(t *testing.T, path string, got, want error) {
		t.Helper()
		for _, sentinel := range sentinels {
			if is := errors.Is(got, sentinel); is != (sentinel == want) {
				t.Errorf("%s: errors.Is(%v, %v) = %v", path, got, sentinel, is)
			}
		}
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			check(t, "single write", classifyBudgetAssignmentWriteError(c.err), c.write)
			check(t, "bulk save", classifyBudgetChangesError(c.err), c.bulkChange)
		})
	}
}

func TestBudgetAssignmentsRepo_RecordsTheIdentityAUserGoesBy(t *testing.T) {
	t.Parallel()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		ctx := t.Context()
		eng := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		alice := seedAssignmentUser(t, f.db, "alice", "jit", nil, new("Alice@Acme.test"))
		shared1 := seedAssignmentUser(t, f.db, "s1", "jit", nil, new("team@acme.test"))
		shared2 := seedAssignmentUser(t, f.db, "s2", "jit", nil, new("team@acme.test"))
		bot := seedAssignmentUser(t, f.db, "system:ci-bot", provisionedViaSystem, nil, nil)

		type identity struct{ typ, value, issuer string }
		recorded := func(t *testing.T, id string) identity {
			t.Helper()
			a := f.mustGet(t, id)
			return identity{a.IdentityType, a.IdentityValue, a.IdentityIssuer}
		}
		for name, c := range map[string]struct {
			ref  string
			want identity
		}{
			"an email of its own": {alice, identity{BudgetIdentityEmail, "alice@acme.test", ""}},
			"a shared email":      {shared1, identity{BudgetIdentitySub, "s1", "https://idp.test"}},
			"no email":            {refA, identity{BudgetIdentitySub, "sub-" + refA, "https://idp.test"}},
			"a system user":       {bot, identity{BudgetIdentitySystem, "ci-bot", ""}},
		} {
			t.Run("create: "+name, func(t *testing.T) {
				a := f.assign(t, c.ref, eng.ID)
				if got := recorded(t, a.ID); got != c.want {
					t.Errorf("recorded %+v, want %+v", got, c.want)
				}
			})
		}

		ops := createBudget(t, f.budgets, &Budget{Name: "Operations", Amount: new(int64(100))})
		got, err := f.repo.ApplyBudgetChanges(ctx, BudgetAssignmentChanges{
			BudgetID: ops.ID, SubjectType: BudgetSubjectUser, Add: []string{shared2}, Actor: "saver", NowMs: 1,
		})
		if err != nil {
			t.Fatalf("save: %v", err)
		}
		if id := recorded(t, got[0].AssignmentID); id != (identity{BudgetIdentitySub, "s2", "https://idp.test"}) {
			t.Errorf("save recorded %+v, want s2's sub", id)
		}
	})
}
