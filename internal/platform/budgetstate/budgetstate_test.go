// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package budgetstate

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const testNamespace = "budget-state-test"

var (
	discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))
	testNow       = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	testPeriodEnd = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
)

type fixture struct {
	t     *testing.T
	db    *store.DB
	store Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := store.TestDB(t)
	return &fixture{t: t, db: db, store: NewStore(db)}
}

func (f *fixture) limitMesh(amount int64) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.t.Context(), `UPDATE budgets SET amount = ? WHERE subject_type = 'mesh'`, amount); err != nil {
		f.t.Fatalf("limit the mesh: %v", err)
	}
}

func (f *fixture) topUpMesh(amount int64, expires time.Time) {
	f.t.Helper()
	created := testNow.Add(-time.Hour).UnixMilli()
	if _, err := f.db.ExecContext(f.t.Context(),
		`INSERT INTO budget_top_ups (id, subject_type, subject_ref, amount, expires_time, created_by, updated_by, created_time, updated_time)
		 VALUES (?, 'mesh', 'mesh', ?, ?, 'admin', 'admin', ?, ?)`,
		idgen.New(), amount, expires.UnixMilli(), created, created); err != nil {
		f.t.Fatalf("top up the mesh: %v", err)
	}
}

func (f *fixture) spend(at time.Time, cost int64) {
	f.t.Helper()
	entry := store.SpendLedgerEntry{
		ID:            idgen.New(),
		CompletedTime: at.UnixMilli(),
		UserID:        new(idgen.New()),
		Surface:       "agent",
		ModelName:     new("m"),
		OutputTokens:  1,
		Cost:          &spend.Cost{Output: cost},
	}
	if _, err := store.NewSpendLedgerRepo(f.db).Insert(f.t.Context(), []store.SpendLedgerEntry{entry}); err != nil {
		f.t.Fatalf("file spend: %v", err)
	}
}

func (f *fixture) limitUserDefault(amount int64) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.t.Context(), `UPDATE budgets SET amount = ? WHERE id = ?`, amount, store.SeededDefaultUserBudgetID); err != nil {
		f.t.Fatalf("limit the user default: %v", err)
	}
}

func (f *fixture) assign(userID string, amount *int64) {
	f.t.Helper()
	budgetID, created := idgen.New(), testNow.Add(-time.Hour).UnixMilli()
	if _, err := f.db.ExecContext(f.t.Context(),
		`INSERT INTO budgets (id, name, name_lower, subject_type, amount, is_default, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, 'user', ?, 0, 'admin', 'admin', ?, ?)`,
		budgetID, "budget "+budgetID, "budget "+budgetID, amount, created, created); err != nil {
		f.t.Fatalf("create a budget: %v", err)
	}
	if _, err := f.db.ExecContext(f.t.Context(),
		`INSERT INTO budget_assignments (id, subject_type, subject_ref, budget_id, created_by, updated_by, created_time, updated_time)
		 VALUES (?, 'user', ?, ?, 'admin', 'admin', ?, ?)`,
		idgen.New(), userID, budgetID, created, created); err != nil {
		f.t.Fatalf("assign a budget: %v", err)
	}
}

func (f *fixture) systemUser(sub string) string {
	f.t.Helper()
	id := idgen.New()
	if _, err := f.db.ExecContext(f.t.Context(),
		`INSERT INTO users (id, issuer, sub, source, provisioned_via, active, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, '{}', 'system', 1, 'admin', 'admin', 1, 1)`, id, rbac.SystemUserIssuer, sub); err != nil {
		f.t.Fatalf("create a system user: %v", err)
	}
	return id
}

func (f *fixture) assignPending(identityType, value string, amount *int64) {
	f.t.Helper()
	budgetID, created := idgen.New(), testNow.Add(-time.Hour).UnixMilli()
	if _, err := f.db.ExecContext(f.t.Context(),
		`INSERT INTO budgets (id, name, name_lower, subject_type, amount, is_default, created_by, updated_by, created_time, updated_time)
		 VALUES (?, ?, ?, 'user', ?, 0, 'admin', 'admin', ?, ?)`,
		budgetID, "budget "+budgetID, "budget "+budgetID, amount, created, created); err != nil {
		f.t.Fatalf("create a budget: %v", err)
	}
	if _, err := f.db.ExecContext(f.t.Context(),
		`INSERT INTO budget_pending_assignments (id, subject_type, identity_type, identity_value, issuer, budget_id,
		 created_by, updated_by, created_time, updated_time)
		 VALUES (?, 'user', ?, ?, ?, ?, 'admin', 'admin', ?, ?)`,
		idgen.New(), identityType, value, map[bool]string{true: "https://idp.example.com"}[identityType == store.BudgetIdentitySub],
		budgetID, created, created); err != nil {
		f.t.Fatalf("assign a pending budget: %v", err)
	}
}

func (f *fixture) topUpUser(userID string, amount int64, expires time.Time) {
	f.t.Helper()
	created := testNow.Add(-time.Hour).UnixMilli()
	if _, err := f.db.ExecContext(f.t.Context(),
		`INSERT INTO budget_top_ups (id, subject_type, subject_ref, amount, expires_time, created_by, updated_by, created_time, updated_time)
		 VALUES (?, 'user', ?, ?, ?, 'admin', 'admin', ?, ?)`,
		idgen.New(), userID, amount, expires.UnixMilli(), created, created); err != nil {
		f.t.Fatalf("top up a user: %v", err)
	}
}

func (f *fixture) spendAs(id, sub string, at time.Time, cost int64) {
	f.t.Helper()
	entry := store.SpendLedgerEntry{
		ID:            idgen.New(),
		CompletedTime: at.UnixMilli(),
		Surface:       "agent",
		ModelName:     new("m"),
		OutputTokens:  1,
		Cost:          &spend.Cost{Output: cost},
	}
	if id != "" {
		entry.UserID = new(id)
	} else {
		entry.UserSub = new(sub)
	}
	if _, err := store.NewSpendLedgerRepo(f.db).Insert(f.t.Context(), []store.SpendLedgerEntry{entry}); err != nil {
		f.t.Fatalf("file spend: %v", err)
	}
}

func (f *fixture) compute() budget.EnforcementState {
	f.t.Helper()
	state, err := Compute(f.t.Context(), f.store, testNow)
	if err != nil {
		f.t.Fatalf("compute: %v", err)
	}
	return state
}

func TestCompute_AFreshInstallHasNoBudgetsInUse(t *testing.T) {
	f := newFixture(t)
	f.spend(testNow, 1_000_000)

	state := f.compute()
	want := budget.EnforcementState{PeriodEnd: testPeriodEnd, ComputedAt: testNow}
	if !reflect.DeepEqual(state, want) {
		t.Errorf("state = %+v, want %+v", state, want)
	}
}

func TestCompute_TheMeshIsBlockedOnceThePeriodsSpendReachesItsLimit(t *testing.T) {
	f := newFixture(t)
	f.limitMesh(100)
	f.spend(time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC), 1000)
	f.spend(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), 40)
	f.spend(testNow, 59)

	if state := f.compute(); !state.BudgetsInUse || state.MeshBlocked {
		t.Fatalf("state = %+v at 99 of 100 this period, want in use and not blocked; last month's spend must not count", state)
	}
	f.spend(testNow, 1)
	if state := f.compute(); !state.MeshBlocked || !state.PeriodEnd.Equal(testPeriodEnd) {
		t.Errorf("state = %+v at exactly the limit, want blocked until %s", state, testPeriodEnd)
	}
}

func TestCompute_AnActiveTopUpRaisesTheLimit(t *testing.T) {
	f := newFixture(t)
	f.limitMesh(100)
	f.spend(testNow, 150)
	f.topUpMesh(500, testNow.Add(-time.Minute))

	if !f.compute().MeshBlocked {
		t.Fatal("an expired top-up lifted the limit")
	}
	f.topUpMesh(100, testPeriodEnd)
	if f.compute().MeshBlocked {
		t.Error("an active top-up did not lift the limit")
	}
}

func TestCompute_UnpricedSpendDoesNotCount(t *testing.T) {
	f := newFixture(t)
	f.limitMesh(10)
	entry := store.SpendLedgerEntry{
		ID: idgen.New(), CompletedTime: testNow.UnixMilli(), UserSub: new("u"),
		Surface: "agent", ModelName: new("inline"), OutputTokens: 1_000_000,
	}
	if _, err := store.NewSpendLedgerRepo(f.db).Insert(t.Context(), []store.SpendLedgerEntry{entry}); err != nil {
		t.Fatalf("file spend: %v", err)
	}
	if f.compute().MeshBlocked {
		t.Error("an unpriced call counted against the limit")
	}
}

type limitedBetweenReads struct {
	Store
	limit func()
}

func (s limitedBetweenReads) AnyLimited(ctx context.Context) (bool, error) {
	inUse, err := s.Store.AnyLimited(ctx)
	s.limit()
	return inUse, err
}

func TestCompute_AFirstLimitSetMidComputeStillYieldsAStateReceiversAccept(t *testing.T) {
	f := newFixture(t)
	f.spend(testNow, 500)
	racing := limitedBetweenReads{Store: f.store, limit: func() { f.limitMesh(100) }}

	state, err := Compute(t.Context(), racing, testNow)
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if !state.MeshBlocked || !state.BudgetsInUse {
		t.Errorf("state = %+v, want blocked with a budget in use", state)
	}
	if err := state.Validate(); err != nil {
		t.Errorf("Validate() = %v, want a state every receiver accepts", err)
	}
}

func TestCompute_AUserIsBlockedOnceTheirSpendReachesTheDefaultLimit(t *testing.T) {
	f := newFixture(t)
	f.limitUserDefault(100)
	alice, bob := idgen.New(), idgen.New()
	f.spendAs(alice, "", time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC), 1000)
	f.spendAs(alice, "", testNow, 99)
	f.spendAs(bob, "", testNow, 99)

	if state := f.compute(); len(state.BlockedUsers) != 0 || state.MeshBlocked {
		t.Fatalf("state = %+v at 99 of 100 each, want nobody blocked; last month's spend must not count", state)
	}
	f.spendAs(alice, "", testNow, 1)
	state := f.compute()
	if want := []string{budget.UserKey(alice, "")}; !slices.Equal(state.BlockedUsers, want) {
		t.Errorf("blocked users = %v at alice's limit, want %v", state.BlockedUsers, want)
	}
	if state.MeshBlocked || state.DefaultUsersBlocked {
		t.Errorf("state = %+v, want only alice blocked", state)
	}
}

func TestCompute_AnAssignedBudgetReplacesTheDefault(t *testing.T) {
	f := newFixture(t)
	f.limitUserDefault(100)
	generous, strict, unlimited := idgen.New(), idgen.New(), idgen.New()
	f.assign(generous, new(int64(1000)))
	f.assign(strict, new(int64(10)))
	f.assign(unlimited, nil)
	for _, user := range []string{generous, strict, unlimited} {
		f.spendAs(user, "", testNow, 500)
	}

	if want := []string{budget.UserKey(strict, "")}; !slices.Equal(f.compute().BlockedUsers, want) {
		t.Errorf("blocked users = %v, want only the user on the strict budget %v", f.compute().BlockedUsers, want)
	}
}

func TestCompute_AUsersActiveTopUpRaisesTheirLimit(t *testing.T) {
	f := newFixture(t)
	f.limitUserDefault(100)
	onDefault, assigned := idgen.New(), idgen.New()
	f.assign(assigned, new(int64(100)))
	f.spendAs(onDefault, "", testNow, 150)
	f.spendAs(assigned, "", testNow, 150)
	f.topUpUser(onDefault, 500, testNow.Add(-time.Minute))

	if got := f.compute().BlockedUsers; len(got) != 2 {
		t.Fatalf("blocked users = %v, want both: the only top-up has expired", got)
	}
	f.topUpUser(onDefault, 100, testPeriodEnd)
	f.topUpUser(assigned, 100, testPeriodEnd)
	if got := f.compute().BlockedUsers; len(got) != 0 {
		t.Errorf("blocked users = %v, want none once each has an active top-up", got)
	}
}

func TestCompute_AUserKnownOnlyByNameIsOnTheDefault(t *testing.T) {
	f := newFixture(t)
	f.limitUserDefault(100)
	f.spendAs("", "system:default", testNow, 100)

	if want := []string{budget.UserKey("", "system:default")}; !slices.Equal(f.compute().BlockedUsers, want) {
		t.Errorf("blocked users = %v, want %v", f.compute().BlockedUsers, want)
	}
}

func TestCompute_ASystemUserBookedByNameIsHeldToItsOwnLimit(t *testing.T) {
	f := newFixture(t)
	f.limitUserDefault(100)
	assigned, toppedUp := f.systemUser("system:nightly"), f.systemUser("system:hourly")
	f.assign(assigned, new(int64(1000)))
	f.topUpUser(toppedUp, 400, testPeriodEnd)
	f.systemUser("system:weekly")
	for _, sub := range []string{"system:nightly", "system:hourly", "system:weekly"} {
		f.spendAs("", sub, testNow, 450)
	}

	if want := []string{budget.UserKey("", "system:weekly")}; !slices.Equal(f.compute().BlockedUsers, want) {
		t.Errorf("blocked users = %v, want %v: the nightly user's assignment and the hourly user's top-up apply to the spend booked by their names",
			f.compute().BlockedUsers, want)
	}
}

func TestCompute_APendingAssignmentHoldsTheNameItWaitsFor(t *testing.T) {
	f := newFixture(t)
	f.limitUserDefault(100)
	f.assignPending(store.BudgetIdentityEmail, "pat@example.com", new(int64(1000)))
	f.assignPending(store.BudgetIdentitySub, "kim-sub", new(int64(10)))
	f.spendAs("", "Pat@Example.com", testNow, 500)
	f.spendAs("", "kim-sub", testNow, 50)
	f.spendAs("", "lee@example.com", testNow, 500)

	want := []string{budget.UserKey("", "kim-sub"), budget.UserKey("", "lee@example.com")}
	slices.Sort(want)
	if got := f.compute().BlockedUsers; !slices.Equal(got, want) {
		t.Errorf("blocked users = %v, want %v: pat's pending 1000 applies whatever the email's case, kim's pending 10 replaces the default", got, want)
	}
}

func TestCompute_AZeroDefaultAdmitsNamesAPendingOrSystemAssignmentCovers(t *testing.T) {
	f := newFixture(t)
	f.limitUserDefault(0)
	f.assignPending(store.BudgetIdentityEmail, "pat@example.com", new(int64(100)))
	f.assign(f.systemUser("system:nightly"), new(int64(100)))
	f.systemUser("system:unassigned")

	state := f.compute()
	for _, user := range []string{budget.UserKey("", "pat@example.com"), budget.UserKey("", "system:nightly")} {
		if state.UserBlocked(user) {
			t.Errorf("%s is refused before spending anything, want admitted by its assignment", user)
		}
	}
	if !state.UserBlocked(budget.UserKey("", "system:unassigned")) {
		t.Error("a system user without an assignment is admitted, want held to the zero default")
	}
}

func TestCompute_AZeroAssignedBudgetBlocksAUserWhoHasSpentNothing(t *testing.T) {
	f := newFixture(t)
	denied := idgen.New()
	f.assign(denied, new(int64(0)))

	if want := []string{budget.UserKey(denied, "")}; !slices.Equal(f.compute().BlockedUsers, want) {
		t.Errorf("blocked users = %v, want %v", f.compute().BlockedUsers, want)
	}
}

func TestCompute_AZeroDefaultListsWhoIsStillAdmitted(t *testing.T) {
	f := newFixture(t)
	f.limitUserDefault(0)
	allowed, spent, toppedUp, onDefault := idgen.New(), idgen.New(), idgen.New(), idgen.New()
	f.assign(allowed, new(int64(100)))
	f.assign(spent, new(int64(100)))
	f.spendAs(spent, "", testNow, 100)
	f.topUpUser(toppedUp, 50, testPeriodEnd)
	f.spendAs(onDefault, "", testNow, 1)

	state := f.compute()
	if !state.DefaultUsersBlocked || len(state.BlockedUsers) != 0 {
		t.Fatalf("state = %+v, want the default blocking and no blocked list", state)
	}
	want := []string{budget.UserKey(allowed, ""), budget.UserKey(toppedUp, "")}
	slices.Sort(want)
	if !slices.Equal(state.AdmittedUsers, want) {
		t.Errorf("admitted users = %v, want %v", state.AdmittedUsers, want)
	}
	for _, user := range []string{budget.UserKey(spent, ""), budget.UserKey(onDefault, ""), budget.UserKey("", "never-seen")} {
		if !state.UserBlocked(user) {
			t.Errorf("%s is admitted, want refused", user)
		}
	}
}

func TestCompute_TheMeshCountsEveryUsersSpend(t *testing.T) {
	f := newFixture(t)
	f.limitMesh(100)
	f.spendAs(idgen.New(), "", testNow, 50)
	f.spendAs("", "system:default", testNow, 50)

	if !f.compute().MeshBlocked {
		t.Error("the mesh is not blocked once two users' spend together reaches its limit")
	}
}

func (f *fixture) warnMeshAt(pct int) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.t.Context(), `UPDATE budgets SET warn_percentage = ? WHERE subject_type = 'mesh'`, pct); err != nil {
		f.t.Fatalf("set the mesh warning: %v", err)
	}
}

func (f *fixture) warnUserDefaultAt(pct int) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.t.Context(), `UPDATE budgets SET warn_percentage = ? WHERE id = ?`, pct, store.SeededDefaultUserBudgetID); err != nil {
		f.t.Fatalf("set the default's warning: %v", err)
	}
}

func (f *fixture) assess() assessment {
	f.t.Helper()
	a, err := assess(f.t.Context(), f.store, testNow)
	if err != nil {
		f.t.Fatalf("assess: %v", err)
	}
	return a
}

func (a assessment) standingOf(t *testing.T, ref string) standing {
	t.Helper()
	for _, s := range a.standings {
		if s.subjectRef == ref {
			return s
		}
	}
	t.Fatalf("no standing for %s in %+v", ref, a.standings)
	return standing{}
}

func TestCompute_AUserPastTheirWarningIsListedWarnedNotBlocked(t *testing.T) {
	f := newFixture(t)
	f.limitUserDefault(100)
	f.warnUserDefaultAt(80)
	warned, below, spent := idgen.New(), idgen.New(), idgen.New()
	f.spendAs(warned, "", testNow, 80)
	f.spendAs(below, "", testNow, 79)
	f.spendAs(spent, "", testNow, 100)

	state := f.compute()
	if want := []string{budget.UserKey(warned, "")}; !slices.Equal(state.WarnedUsers, want) {
		t.Errorf("warned users = %v, want only the user at the warning %v; a blocked user is not listed warned", state.WarnedUsers, want)
	}
	for user, want := range map[string]budget.Level{warned: budget.LevelWarn, below: budget.LevelNone, spent: budget.LevelLimit} {
		if got := state.UserLevel(budget.UserKey(user, "")); got != want {
			t.Errorf("level of a user = %q, want %q", got, want)
		}
	}
}

func TestCompute_ATopUpRaisesTheWarningWithTheLimit(t *testing.T) {
	f := newFixture(t)
	alice := idgen.New()
	f.assign(alice, new(int64(100)))
	if _, err := f.db.ExecContext(t.Context(), `UPDATE budgets SET warn_percentage = 50 WHERE subject_type = 'user' AND is_default = 0`); err != nil {
		t.Fatalf("set the warning: %v", err)
	}
	f.spendAs(alice, "", testNow, 60)
	if got := f.compute().UserLevel(budget.UserKey(alice, "")); got != budget.LevelWarn {
		t.Fatalf("level = %q at 60 of 100 warning at 50%%, want warn", got)
	}
	f.topUpUser(alice, 100, testPeriodEnd)
	if got := f.compute().UserLevel(budget.UserKey(alice, "")); got != budget.LevelNone {
		t.Errorf("level = %q at 60 of 200 warning at 50%%, want none", got)
	}
}

func TestCompute_ADefaultThatWarnsWithNothingSpentListsWhoIsNotWarned(t *testing.T) {
	f := newFixture(t)
	f.limitUserDefault(50)
	f.warnUserDefaultAt(1)
	generous, onDefault := idgen.New(), idgen.New()
	f.assign(generous, new(int64(1_000_000)))
	f.spendAs(onDefault, "", testNow, 1)

	state := f.compute()
	if !state.DefaultUsersWarned || len(state.WarnedUsers) != 0 || state.DefaultUsersBlocked {
		t.Fatalf("state = %+v, want the default warning everyone and no warned list", state)
	}
	if want := []string{budget.UserKey(generous, "")}; !slices.Equal(state.UnwarnedUsers, want) {
		t.Errorf("unwarned users = %v, want %v", state.UnwarnedUsers, want)
	}
	if got := state.UserLevel(budget.UserKey("", "never-seen")); got != budget.LevelWarn {
		t.Errorf("a user the platform has never seen is at %q, want warn", got)
	}
}

func TestCompute_TheMeshWarningIsNotBroadcast(t *testing.T) {
	f := newFixture(t)
	f.limitMesh(100)
	f.warnMeshAt(50)
	f.spend(testNow, 60)

	a := f.assess()
	if a.state.MeshBlocked {
		t.Fatalf("state = %+v, want the mesh not blocked", a.state)
	}
	if got := a.standingOf(t, store.BudgetMeshSubjectRef).level; got != budget.LevelWarn {
		t.Errorf("mesh level = %q, want warn", got)
	}
}

func TestAssess_TheCapKeyChangesWithWhatGovernsTheLevelButNotWithSpend(t *testing.T) {
	f := newFixture(t)
	f.limitUserDefault(100)
	alice := idgen.New()
	f.spendAs(alice, "", testNow, 10)
	key := func() string {
		t.Helper()
		return f.assess().standingOf(t, budget.UserKey(alice, "")).capKey
	}
	onDefault := key()

	f.spendAs(alice, "", testNow, 10)
	if got := key(); got != onDefault {
		t.Errorf("spending changed the cap key from %q to %q", onDefault, got)
	}
	f.warnUserDefaultAt(80)
	warned := key()
	if warned == onDefault {
		t.Error("setting a warning percentage left the cap key unchanged")
	}
	f.topUpUser(alice, 50, testPeriodEnd)
	toppedUp := key()
	if toppedUp == warned {
		t.Error("a top-up left the cap key unchanged")
	}
	f.assign(alice, new(int64(150)))
	if got := key(); got == toppedUp {
		t.Error("assigning a budget with the same effective cap left the cap key unchanged")
	}
}

func TestCompute_APendingAssignmentsWarningWarnsTheNameItWaitsFor(t *testing.T) {
	f := newFixture(t)
	f.limitUserDefault(1000)
	f.assignPending(store.BudgetIdentityEmail, "pat@example.com", new(int64(100)))
	if _, err := f.db.ExecContext(t.Context(),
		`UPDATE budgets SET warn_percentage = 50 WHERE id = (SELECT budget_id FROM budget_pending_assignments)`); err != nil {
		t.Fatalf("set the pending budget's warning: %v", err)
	}
	f.spendAs("", "pat@example.com", testNow, 60)

	pat := budget.UserKey("", "pat@example.com")
	a := f.assess()
	if got := a.state.UserLevel(pat); got != budget.LevelWarn {
		t.Errorf("level = %q, want warn: the pending budget's 50%% warning applies, not the default's none", got)
	}
	if st := a.standingOf(t, pat); st.capKey == "" || strings.HasPrefix(st.capKey, defaultCapRef+":") {
		t.Errorf("cap key = %q, want it named after the pending budget", st.capKey)
	}
}

func TestCompute_AStateTooLargeToSendOmitsTheWarnedUsersToo(t *testing.T) {
	f := newFixture(t)
	f.limitUserDefault(100)
	f.warnUserDefaultAt(50)
	for range 5 {
		f.spendAs(idgen.New(), "", testNow, 60)
	}
	a, err := assessWithin(t.Context(), f.store, testNow, 10)
	if err != nil {
		t.Fatalf("assess: %v", err)
	}
	if !a.state.UsersOmitted || len(a.state.WarnedUsers) != 0 || len(a.state.WarnCapKeys) != 0 {
		t.Errorf("state = %+v, want the user lists, warned ones included, omitted", a.state)
	}
	if len(a.standings) != 6 {
		t.Errorf("standings = %d, want the mesh and all 5 users still judged for alerts", len(a.standings))
	}
}

func TestCompute_AWarnedUsersCapKeyIsOpaqueAndChangesWithATopUp(t *testing.T) {
	f := newFixture(t)
	f.limitUserDefault(10_000)
	f.warnUserDefaultAt(80)
	alice := idgen.New()
	key := budget.UserKey(alice, "")
	f.spendAs(alice, "", testNow, 8_500)

	before := f.compute()
	capKey := before.WarnCapKey(key)
	if capKey == "" || strings.Contains(capKey, "10000") || strings.Contains(capKey, "default") {
		t.Fatalf("cap key = %q, want an opaque key that names neither the amount nor the budget", capKey)
	}
	f.topUpUser(alice, 100, testPeriodEnd)
	after := f.compute()
	if after.UserLevel(key) != budget.LevelWarn || after.WarnCapKey(key) == capKey {
		t.Errorf("after a top-up the level is %q with cap key %q (was %q), want still warned under a new key", after.UserLevel(key), after.WarnCapKey(key), capKey)
	}
	if before.SameDecision(after) {
		t.Error("a top-up that changes only the warned user's cap reads as the same decision, so it would not be broadcast")
	}
}

func TestCompute_ADefaultThatWarnsEveryoneCarriesItsCapKey(t *testing.T) {
	f := newFixture(t)
	f.limitUserDefault(50)
	f.warnUserDefaultAt(1)
	if got := f.compute().WarnCapKey(budget.UserKey("", "never-seen")); got == "" {
		t.Error("a user warned by the default has no cap key")
	}
}

func TestCompute_AWarnedUsersCapKeyHoldsAsTheySpend(t *testing.T) {
	f := newFixture(t)
	f.limitUserDefault(100)
	f.warnUserDefaultAt(50)
	alice := idgen.New()
	alicesKey := budget.UserKey(alice, "")
	f.spendAs(alice, "", testNow, 60)
	before := f.compute().WarnCapKey(alicesKey)

	f.spendAs(alice, "", testNow, 10)
	after := f.compute().WarnCapKey(alicesKey)

	if before == "" || before != after {
		t.Errorf("cap key %q became %q with only more spend, want it unchanged", before, after)
	}
}
