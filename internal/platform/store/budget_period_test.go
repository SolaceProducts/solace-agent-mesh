// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"maps"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const (
	octoberStart  = int64(1_759_276_800_000)
	novemberStart = int64(1_761_955_200_000)
	decemberStart = int64(1_764_547_200_000)
	midOctober    = octoberStart + 9*24*60*60*1000
)

type budgetPeriodRow struct {
	SubjectKind         string `db:"subject_kind"`
	SubjectRef          string `db:"subject_ref"`
	PeriodStart         int64  `db:"period_start"`
	PeriodEnd           int64  `db:"period_end"`
	PricedCalls         int64  `db:"priced_calls"`
	UnpricedCalls       int64  `db:"unpriced_calls"`
	UncachedInputTokens int64  `db:"uncached_input_tokens"`
	OutputTokens        int64  `db:"output_tokens"`
	CacheReadTokens     int64  `db:"cache_read_tokens"`
	CacheWriteTokens    int64  `db:"cache_write_tokens"`
	UncachedInputCost   int64  `db:"uncached_input_cost"`
	OutputCost          int64  `db:"output_cost"`
	CacheReadCost       int64  `db:"cache_read_cost"`
	CacheWriteCost      int64  `db:"cache_write_cost"`
	TotalCost           int64  `db:"total_cost"`
}

func readBudgetPeriodRows(t *testing.T, db *DB) map[BudgetPeriodKey]budgetPeriodRow {
	t.Helper()
	rows, err := dbutil.QueryRows[budgetPeriodRow](t.Context(), db, `SELECT * FROM token_spend_budget_period`)
	if err != nil {
		t.Fatalf("read budget period rows: %v", err)
	}
	byKey := make(map[BudgetPeriodKey]budgetPeriodRow, len(rows))
	for _, r := range rows {
		subject := BudgetPeriodSubject{Kind: BudgetPeriodSubjectKind(r.SubjectKind), Ref: r.SubjectRef}
		byKey[BudgetPeriodKey{Subject: subject, PeriodStart: r.PeriodStart}] = r
	}
	return byKey
}

func userIDKey(id string, periodStart int64) BudgetPeriodKey {
	return BudgetPeriodKey{Subject: BudgetPeriodSubject{Kind: BudgetPeriodUserID, Ref: id}, PeriodStart: periodStart}
}

func userSubKey(sub string, periodStart int64) BudgetPeriodKey {
	return BudgetPeriodKey{Subject: BudgetPeriodSubject{Kind: BudgetPeriodUserSub, Ref: sub}, PeriodStart: periodStart}
}

func meshKey(periodStart int64) BudgetPeriodKey {
	return BudgetPeriodKey{Subject: BudgetPeriodMeshSubject, PeriodStart: periodStart}
}

func pricedCallBy(userID string, completed int64, cost spend.Cost) SpendLedgerEntry {
	e := pricedLedgerEntry()
	e.UserID = new(userID)
	e.CompletedTime = completed
	e.Cost = &cost
	return e
}

func unpricedCallBy(sub string, completed int64) SpendLedgerEntry {
	e := unpricedLedgerEntry()
	e.UserSub = new(sub)
	e.CompletedTime = completed
	return e
}

func insertSpend(t *testing.T, repo *SpendLedgerRepo, entries ...SpendLedgerEntry) int64 {
	t.Helper()
	filed, err := repo.Insert(t.Context(), entries)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	return filed
}

func checkMesh(t *testing.T, repo *SpendLedgerRepo, periodStart int64) BudgetPeriodMeshCheck {
	t.Helper()
	check, err := repo.CheckMeshPeriod(t.Context(), periodStart)
	if err != nil {
		t.Fatalf("check mesh period: %v", err)
	}
	return check
}

func periodDrift(t *testing.T, repo *SpendLedgerRepo, periodStart int64) []BudgetPeriodDrift {
	t.Helper()
	drift, err := repo.PeriodDrift(t.Context(), periodStart, time.Minute)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	return drift
}

func TestBudgetPeriod_ABatchAddsToEachUsersRowAndTheMeshRow(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	alice := idgen.New()

	insertSpend(t, repo,
		pricedCallBy(alice, octoberStart, spend.Cost{UncachedInput: 10, Output: 20, CacheRead: 1, CacheWrite: 2}),
		pricedCallBy(alice, novemberStart-1, spend.Cost{UncachedInput: 100, Output: 200, CacheRead: 10, CacheWrite: 20}),
		unpricedCallBy("bob", midOctober),
	)

	rows := readBudgetPeriodRows(t, db)
	if len(rows) != 3 {
		t.Fatalf("budget period rows = %d, want alice, bob and the mesh: %+v", len(rows), rows)
	}
	want := budgetPeriodRow{
		SubjectKind: "id", SubjectRef: alice, PeriodStart: octoberStart, PeriodEnd: novemberStart,
		PricedCalls: 2, UnpricedCalls: 0,
		UncachedInputTokens: 800, OutputTokens: 100, CacheReadTokens: 800, CacheWriteTokens: 400,
		UncachedInputCost: 110, OutputCost: 220, CacheReadCost: 11, CacheWriteCost: 22, TotalCost: 363,
	}
	if got := rows[userIDKey(alice, octoberStart)]; got != want {
		t.Errorf("alice's period = %+v, want %+v", got, want)
	}
	bob := rows[userSubKey("bob", octoberStart)]
	if bob.UnpricedCalls != 1 || bob.PricedCalls != 0 || bob.TotalCost != 0 || bob.UncachedInputTokens != 10 || bob.OutputTokens != 5 {
		t.Errorf("an unpriced call must count as unpriced, add its tokens and no cost: %+v", bob)
	}
	mesh := rows[meshKey(octoberStart)]
	wantMesh := want
	wantMesh.SubjectKind, wantMesh.SubjectRef = "mesh", ""
	wantMesh.UnpricedCalls = 1
	wantMesh.UncachedInputTokens += bob.UncachedInputTokens
	wantMesh.OutputTokens += bob.OutputTokens
	wantMesh.CacheReadTokens += bob.CacheReadTokens
	wantMesh.CacheWriteTokens += bob.CacheWriteTokens
	if mesh != wantMesh {
		t.Errorf("mesh period = %+v, want %+v", mesh, wantMesh)
	}
}

func TestBudgetPeriod_AccumulatesAcrossBatches(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	alice := idgen.New()

	insertSpend(t, repo, pricedCallBy(alice, octoberStart, spend.Cost{Output: 5}))
	insertSpend(t, repo, pricedCallBy(alice, midOctober, spend.Cost{Output: 6}), unpricedCallBy("bob", octoberStart))
	insertSpend(t, repo, unpricedCallBy("bob", midOctober))

	rows := readBudgetPeriodRows(t, db)
	if got := rows[userIDKey(alice, octoberStart)]; got.PricedCalls != 2 || got.TotalCost != 11 {
		t.Errorf("alice = %+v, want 2 priced calls costing 11", got)
	}
	if got := rows[userSubKey("bob", octoberStart)]; got.UnpricedCalls != 2 {
		t.Errorf("bob = %+v, want 2 unpriced calls", got)
	}
	if got := rows[meshKey(octoberStart)]; got.PricedCalls != 2 || got.UnpricedCalls != 2 || got.TotalCost != 11 {
		t.Errorf("mesh = %+v, want every call of the three batches", got)
	}
}

func TestBudgetPeriod_ALateCallCountsTowardThePeriodItCompletedIn(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	alice := idgen.New()

	insertSpend(t, repo, pricedCallBy(alice, novemberStart, spend.Cost{Output: 7}))
	insertSpend(t, repo, pricedCallBy(alice, novemberStart-1, spend.Cost{Output: 3}), pricedCallBy(alice, novemberStart+5, spend.Cost{Output: 1}))

	rows := readBudgetPeriodRows(t, db)
	for key, want := range map[BudgetPeriodKey]int64{
		userIDKey(alice, octoberStart):  3,
		meshKey(octoberStart):           3,
		userIDKey(alice, novemberStart): 8,
		meshKey(novemberStart):          8,
	} {
		if got := rows[key]; got.TotalCost != want {
			t.Errorf("%+v total cost = %d, want %d", key, got.TotalCost, want)
		}
	}
	if got := rows[meshKey(novemberStart)]; got.PeriodEnd != decemberStart {
		t.Errorf("November's period end = %d, want %d", got.PeriodEnd, decemberStart)
	}
}

func TestBudgetPeriod_CountsARedeliveredCallOnce(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	call := pricedCallBy(idgen.New(), midOctober, spend.Cost{Output: 9})

	if filed := insertSpend(t, repo, call, call); filed != 1 {
		t.Errorf("filed = %d for one call repeated in a batch, want 1", filed)
	}
	if filed := insertSpend(t, repo, call, pricedCallBy(*call.UserID, midOctober, spend.Cost{Output: 1})); filed != 1 {
		t.Errorf("filed = %d for a redelivery and one new call, want 1", filed)
	}

	rows := readBudgetPeriodRows(t, db)
	for _, key := range []BudgetPeriodKey{userIDKey(*call.UserID, octoberStart), meshKey(octoberStart)} {
		if got := rows[key]; got.PricedCalls != 2 || got.TotalCost != 10 {
			t.Errorf("%+v = %+v, want the redelivered call counted once: 2 calls costing 10", key, got)
		}
	}
}

func TestBudgetPeriod_KeepsAUserIDAndASubWithTheSameTextApart(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	id := idgen.New()

	insertSpend(t, repo, pricedCallBy(id, midOctober, spend.Cost{Output: 3}), unpricedCallBy(id, midOctober))

	rows := readBudgetPeriodRows(t, db)
	if len(rows) != 3 {
		t.Fatalf("budget period rows = %d, want one per namespace and the mesh: %+v", len(rows), rows)
	}
	if rows[userSubKey(id, octoberStart)].TotalCost != 0 {
		t.Error("the sub's row took the id's cost")
	}
}

func TestBudgetPeriod_SaturatesInsteadOfFailingTheWrite(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	alice, bob := idgen.New(), idgen.New()
	half := int64(math.MaxInt64/2 + 1)

	insertSpend(t, repo, pricedCallBy(alice, midOctober, spend.Cost{Output: half}), pricedCallBy(alice, midOctober, spend.Cost{Output: half}))
	insertSpend(t, repo, pricedCallBy(alice, midOctober, spend.Cost{Output: half}), pricedCallBy(bob, midOctober, spend.Cost{Output: half}))

	rows := readBudgetPeriodRows(t, db)
	for _, key := range []BudgetPeriodKey{userIDKey(alice, octoberStart), meshKey(octoberStart)} {
		got := rows[key]
		if got.OutputCost != math.MaxInt64 || got.TotalCost != math.MaxInt64 {
			t.Errorf("%+v output cost %d, total %d; want both saturated at %d", key, got.OutputCost, got.TotalCost, int64(math.MaxInt64))
		}
	}
	if got := rows[meshKey(octoberStart)].PricedCalls; got != 4 {
		t.Errorf("mesh priced calls = %d, want 4", got)
	}
	if drift := periodDrift(t, repo, octoberStart); len(drift) != 0 {
		t.Errorf("a saturated period reads as drift, so the check and the write saturate differently: %+v", drift)
	}
	if check := checkMesh(t, repo, octoberStart); !check.Consistent() {
		t.Errorf("a saturated mesh row reads as unequal to its users: %+v", check)
	}
	cost, err := repo.PeriodCost(t.Context(), BudgetPeriodMeshSubject, octoberStart)
	if err != nil || cost != math.MaxInt64 {
		t.Errorf("mesh period cost = %d, %v; want it saturated", cost, err)
	}
}

func TestBudgetPeriod_ALedgerFailureLeavesTheRowsUntouched(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	user := idgen.New()
	insertSpend(t, repo, pricedCallBy(user, midOctober, spend.Cost{Output: 4}))

	bad := pricedCallBy(user, midOctober, spend.Cost{Output: -1})
	if _, err := repo.Insert(t.Context(), []SpendLedgerEntry{pricedCallBy(user, midOctober, spend.Cost{Output: 50}), bad}); err == nil {
		t.Fatal("the ledger accepted a negative cost")
	}

	rows := readBudgetPeriodRows(t, db)
	for _, key := range []BudgetPeriodKey{userIDKey(user, octoberStart), meshKey(octoberStart)} {
		if got := rows[key]; got.PricedCalls != 1 || got.TotalCost != 4 {
			t.Errorf("%+v = %+v after a refused batch, want only the first call", key, got)
		}
	}
}

func TestBudgetPeriod_PurgeKeepsAPeriodUntilItHasEndedByTheCutoff(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	user := idgen.New()
	insertSpend(t, repo,
		pricedCallBy(user, octoberStart-1, spend.Cost{Output: 1}),
		pricedCallBy(user, octoberStart, spend.Cost{Output: 2}),
		pricedCallBy(user, novemberStart-1, spend.Cost{Output: 3}),
	)

	if _, err := repo.DeleteBudgetPeriodsEndedBy(t.Context(), time.UnixMilli(midOctober)); err != nil {
		t.Fatalf("purge: %v", err)
	}
	rows := readBudgetPeriodRows(t, db)
	if len(rows) != 2 {
		t.Errorf("rows = %+v, want September's gone and October's user and mesh rows kept", rows)
	}
	for _, key := range []BudgetPeriodKey{userIDKey(user, octoberStart), meshKey(octoberStart)} {
		if got := rows[key]; got.TotalCost != 5 {
			t.Errorf("%+v in the period the cutoff falls in = %+v, want it kept whole", key, got)
		}
	}

	if _, err := repo.DeleteBudgetPeriodsEndedBy(t.Context(), time.UnixMilli(novemberStart)); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if rows := readBudgetPeriodRows(t, db); len(rows) != 0 {
		t.Errorf("rows = %+v once the period ended at the cutoff, want none", rows)
	}
}

func TestBudgetPeriodCost_ReadsTheSubjectsRowOfThePeriod(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	alice := idgen.New()
	insertSpend(t, repo,
		pricedCallBy(idgen.New(), octoberStart-1, spend.Cost{Output: 1}),
		pricedCallBy(alice, octoberStart, spend.Cost{Output: 10}),
		pricedCallBy(idgen.New(), novemberStart-1, spend.Cost{Output: 100}),
		unpricedCallBy("bob", midOctober),
		pricedCallBy(idgen.New(), novemberStart, spend.Cost{Output: 1000}),
	)

	for _, tc := range []struct {
		name    string
		subject BudgetPeriodSubject
		period  int64
		want    int64
	}{
		{"the mesh", BudgetPeriodMeshSubject, octoberStart, 110},
		{"the next period", BudgetPeriodMeshSubject, novemberStart, 1000},
		{"a user", BudgetPeriodSubject{Kind: BudgetPeriodUserID, Ref: alice}, octoberStart, 10},
		{"an unpriced user", BudgetPeriodSubject{Kind: BudgetPeriodUserSub, Ref: "bob"}, octoberStart, 0},
		{"a period with no rows", BudgetPeriodMeshSubject, decemberStart, 0},
	} {
		got, err := repo.PeriodCost(t.Context(), tc.subject, tc.period)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: cost = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func byID(id string) BudgetPeriodSubject {
	return BudgetPeriodSubject{Kind: BudgetPeriodUserID, Ref: id}
}

func bySub(sub string) BudgetPeriodSubject {
	return BudgetPeriodSubject{Kind: BudgetPeriodUserSub, Ref: sub}
}

func TestPeriodCostsByUser_ReadsEveryUsersRowOfThePeriodBySubject(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	alice, bob := idgen.New(), idgen.New()
	carol := pricedCallBy(idgen.New(), midOctober, spend.Cost{Output: 5})
	carol.UserID, carol.UserSub = nil, new("carol")
	insertSpend(t, repo,
		pricedCallBy(alice, octoberStart-1, spend.Cost{Output: 1}),
		pricedCallBy(alice, octoberStart, spend.Cost{Output: 10}),
		pricedCallBy(alice, novemberStart-1, spend.Cost{Output: 100}),
		pricedCallBy(bob, midOctober, spend.Cost{Output: 20}),
		unpricedCallBy("dave", midOctober),
		carol,
		pricedCallBy(alice, novemberStart, spend.Cost{Output: 1000}),
	)

	for _, tc := range []struct {
		name   string
		period int64
		want   map[BudgetPeriodSubject]int64
	}{
		{"a period with spend", octoberStart, map[BudgetPeriodSubject]int64{
			byID(alice): 110, byID(bob): 20, bySub("carol"): 5, bySub("dave"): 0,
		}},
		{"the next period", novemberStart, map[BudgetPeriodSubject]int64{byID(alice): 1000}},
		{"a period with no rows", decemberStart, map[BudgetPeriodSubject]int64{}},
	} {
		got, err := repo.PeriodCostsByUser(t.Context(), tc.period)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !maps.Equal(got, tc.want) {
			t.Errorf("%s: costs = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestBudgetPeriodSubject_UserKeyKeepsIDsAndNamesApart(t *testing.T) {
	id := idgen.New()
	for _, tc := range []struct {
		subject BudgetPeriodSubject
		want    string
	}{
		{BudgetPeriodSubject{Kind: BudgetPeriodUserID, Ref: id}, budget.UserKey(id, "")},
		{BudgetPeriodSubject{Kind: BudgetPeriodUserSub, Ref: id}, budget.UserKey("", id)},
		{BudgetPeriodMeshSubject, ""},
	} {
		if got := tc.subject.UserKey(); got != tc.want {
			t.Errorf("%+v: user key = %q, want %q", tc.subject, got, tc.want)
		}
	}
}

func TestBudgetPeriod_RefusesATimeThatIsNotAPeriodStart(t *testing.T) {
	repo := NewSpendLedgerRepo(TestDB(t))
	if _, err := repo.PeriodCost(t.Context(), BudgetPeriodMeshSubject, midOctober); err == nil {
		t.Error("PeriodCost accepted a time inside a period")
	}
	if _, err := repo.PeriodCostsByUser(t.Context(), midOctober); err == nil {
		t.Error("PeriodCostsByUser accepted a time inside a period")
	}
	if _, err := repo.CheckMeshPeriod(t.Context(), midOctober); err == nil {
		t.Error("CheckMeshPeriod accepted a time inside a period")
	}
	if _, err := repo.PeriodDrift(t.Context(), midOctober, time.Minute); err == nil {
		t.Error("PeriodDrift accepted a time inside a period")
	}
}

func TestCheckMeshPeriod_TheGovernorKeepsTheMeshEqualToItsUsers(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	if check := checkMesh(t, repo, octoberStart); !check.Consistent() || check.Mesh != (BudgetPeriodTotals{}) {
		t.Errorf("an empty period = %+v, want consistent zeroes", check)
	}
	insertSpend(t, repo,
		pricedCallBy(idgen.New(), octoberStart, spend.Cost{UncachedInput: 1, Output: 2, CacheRead: 3, CacheWrite: 4}),
		pricedCallBy(idgen.New(), midOctober, spend.Cost{Output: 5}),
		unpricedCallBy("bob", midOctober),
	)
	insertSpend(t, repo, pricedCallBy(idgen.New(), midOctober, spend.Cost{Output: 6}))

	check := checkMesh(t, repo, octoberStart)
	if !check.Consistent() || check.Mesh.TotalCost != 21 || check.Mesh.UnpricedCalls != 1 {
		t.Errorf("check = %+v, want a consistent mesh of 21 with one unpriced call", check)
	}
}

func TestCheckMeshPeriod_CatchesAMeshRowThatNoLongerEqualsItsUsers(t *testing.T) {
	for name, edit := range map[string]string{
		"a mesh row edit": `UPDATE token_spend_budget_period SET total_cost = 1, priced_calls = 9 WHERE subject_kind = 'mesh'`,
		"a user row edit": `UPDATE token_spend_budget_period SET total_cost = 1, priced_calls = 9 WHERE subject_kind = 'id'`,
	} {
		t.Run(name, func(t *testing.T) {
			db := TestDB(t)
			repo := NewSpendLedgerRepo(db)
			insertSpend(t, repo, pricedCallBy(idgen.New(), midOctober, spend.Cost{Output: 5}))
			if _, err := db.ExecContext(t.Context(), edit); err != nil {
				t.Fatalf("edit: %v", err)
			}

			check := checkMesh(t, repo, octoberStart)
			if check.Consistent() {
				t.Fatalf("check = %+v, want the edit caught", check)
			}
			if got := check.Columns(); len(got) != 2 || got[0] != "priced_calls" || got[1] != "total_cost" {
				t.Errorf("differing columns = %v, want priced_calls and total_cost", got)
			}
		})
	}
}

func TestPeriodDrift_FindsNoneWhenTheGovernorWroteBoth(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	insertSpend(t, repo,
		pricedCallBy(idgen.New(), octoberStart, spend.Cost{UncachedInput: 1, Output: 2, CacheRead: 3, CacheWrite: 4}),
		unpricedCallBy("bob", midOctober),
		unpricedCallBy("bob", novemberStart-1),
		pricedCallBy(idgen.New(), novemberStart, spend.Cost{Output: 9}),
	)

	for _, period := range []int64{octoberStart, novemberStart, decemberStart} {
		if drift := periodDrift(t, repo, period); len(drift) != 0 {
			t.Errorf("period %d drift = %+v, want none", period, drift)
		}
	}
}

func TestPeriodDrift_ReportsEachWayAUserRowCanDisagree(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	edited, missing := idgen.New(), idgen.New()
	insertSpend(t, repo, pricedCallBy(edited, midOctober, spend.Cost{Output: 5}))
	insertLedgerOnly(t, db, pricedCallBy(missing, midOctober, spend.Cost{Output: 6}))
	if _, err := db.ExecContext(t.Context(),
		`UPDATE token_spend_budget_period SET total_cost = 99, output_cost = 99 WHERE subject_ref = ?`, edited); err != nil {
		t.Fatalf("edit a user row: %v", err)
	}
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO token_spend_budget_period (subject_kind, subject_ref, period_start, period_end, priced_calls, unpriced_calls,
			uncached_input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
			uncached_input_cost, output_cost, cache_read_cost, cache_write_cost, total_cost)
		VALUES ('sub', 'ghost', ?, ?, 1, 0, 0, 0, 0, 0, 0, 7, 0, 0, 7)`, octoberStart, novemberStart); err != nil {
		t.Fatalf("add a row the ledger does not back: %v", err)
	}

	drift := periodDrift(t, repo, octoberStart)
	bySubject := make(map[BudgetPeriodSubject]BudgetPeriodDrift, len(drift))
	for _, d := range drift {
		bySubject[d.Subject] = d
	}
	if len(bySubject) != 4 {
		t.Fatalf("drift = %+v, want the edited, the missing and the unbacked row, and the mesh row the missing call left short", drift)
	}
	if d := bySubject[BudgetPeriodSubject{Kind: BudgetPeriodUserID, Ref: edited}]; d.Stored.TotalCost != 99 || d.Ledger.TotalCost != 5 {
		t.Errorf("edited row drift = %+v", d)
	}
	if d := bySubject[BudgetPeriodSubject{Kind: BudgetPeriodUserID, Ref: missing}]; d.Stored != (BudgetPeriodTotals{}) || d.Ledger.TotalCost != 6 {
		t.Errorf("missing row drift = %+v", d)
	}
	if d := bySubject[BudgetPeriodSubject{Kind: BudgetPeriodUserSub, Ref: "ghost"}]; d.Stored.TotalCost != 7 || d.Ledger != (BudgetPeriodTotals{}) {
		t.Errorf("unbacked row drift = %+v", d)
	}
	if d := bySubject[BudgetPeriodMeshSubject]; d.Stored.TotalCost != 5 || d.Ledger.TotalCost != 11 {
		t.Errorf("mesh row drift = %+v, want the mesh row short of the missing call", d)
	}
}

func TestPeriodDrift_CatchesAMeshRowEdit(t *testing.T) {
	db := TestDB(t)
	repo := NewSpendLedgerRepo(db)
	insertSpend(t, repo, pricedCallBy(idgen.New(), midOctober, spend.Cost{Output: 5}), unpricedCallBy("bob", midOctober))
	if _, err := db.ExecContext(t.Context(), `UPDATE token_spend_budget_period SET unpriced_calls = 3 WHERE subject_kind = 'mesh'`); err != nil {
		t.Fatalf("edit the mesh row: %v", err)
	}

	drift := periodDrift(t, repo, octoberStart)
	if len(drift) != 1 || drift[0].Subject != BudgetPeriodMeshSubject {
		t.Fatalf("drift = %+v, want only the mesh row", drift)
	}
	if got := drift[0].Columns(); len(got) != 1 || got[0] != "unpriced_calls" {
		t.Errorf("differing columns = %v, want unpriced_calls", got)
	}
}

func TestTokenSpendBudgetPeriodMigration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "token_spend_budget_period", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate up to %d: %v", version-1, err)
		}
		if ledgerTableExists(t, db, "token_spend_budget_period") {
			t.Fatal("the budget period table exists before its migration")
		}
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		call := pricedCallBy(idgen.New(), midOctober, spend.Cost{Output: 1})
		insertAtShippedLedgerSchema(t, db, call)
		if err := db.WithTx(ctx, func(tx *dbutil.Tx) error { return addToBudgetPeriods(ctx, tx, []*SpendLedgerEntry{&call}) }); err != nil {
			t.Fatalf("add the insert to its budget periods: %v", err)
		}
		if n := len(readBudgetPeriodRows(t, db)); n != 2 {
			t.Errorf("budget period rows = %d after an insert, want the user's and the mesh's", n)
		}
		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down: %v", err)
		}
		if ledgerTableExists(t, db, "token_spend_budget_period") {
			t.Fatal("the budget period table survived the rollback")
		}
		if err := db.Migrate(ctx); err != nil {
			t.Fatalf("re-apply after rollback: %v", err)
		}
		if !ledgerTableExists(t, db, "token_spend_budget_period") {
			t.Fatal("the budget period table is absent after re-apply")
		}
		insertSpend(t, NewSpendLedgerRepo(db), pricedCallBy(idgen.New(), midOctober, spend.Cost{Output: 1}))
		if n := len(readBudgetPeriodRows(t, db)); n != 2 {
			t.Errorf("budget period rows = %d after an insert, want the user's and the mesh's", n)
		}
	})
}

func TestTokenSpendBudgetPeriodMigration_RefusesAMalformedRow(t *testing.T) {
	db := TestDB(t)
	insert := `INSERT INTO token_spend_budget_period (subject_kind, subject_ref, period_start, period_end, priced_calls, unpriced_calls,
		uncached_input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
		uncached_input_cost, output_cost, cache_read_cost, cache_write_cost, total_cost)
		VALUES (?, ?, ?, ?, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)`
	for name, args := range map[string][]any{
		"an unknown kind":          {"team", "t", octoberStart, novemberStart},
		"a mesh row with a ref":    {"mesh", "m", octoberStart, novemberStart},
		"a period that ends first": {"id", "u", novemberStart, octoberStart},
	} {
		if _, err := db.ExecContext(t.Context(), insert, args...); err == nil {
			t.Errorf("%s: the table accepted it", name)
		}
	}
}

func TestBudgetPeriod_ConcurrentInsertsOnPostgresAddEveryCallOnceWithoutDeadlock(t *testing.T) {
	db := TestDB(t)
	if db.Dialect() != "postgres" {
		t.Skip("only Postgres files spend from several replicas at once")
	}
	repo := NewSpendLedgerRepo(db)
	users := []string{idgen.New(), idgen.New(), idgen.New(), idgen.New(), idgen.New()}
	const writers, batchesEach = 8, 15

	var wg sync.WaitGroup
	errs := make([]error, writers)
	for w := range writers {
		wg.Go(func() {
			for b := range batchesEach {
				batch := make([]SpendLedgerEntry, len(users))
				for i := range users {
					user := users[(i+w+b)%len(users)]
					if (w+b)%2 == 1 {
						user = users[len(users)-1-(i+w+b)%len(users)]
					}
					batch[i] = pricedCallBy(user, midOctober, spend.Cost{Output: 3})
				}
				if _, err := repo.Insert(t.Context(), batch); err != nil {
					errs[w] = err
					return
				}
			}
		})
	}
	wg.Wait()
	for w, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", w, err)
		}
	}

	calls := int64(writers * batchesEach * len(users))
	rows := readBudgetPeriodRows(t, db)
	if mesh := rows[meshKey(octoberStart)]; mesh.PricedCalls != calls || mesh.TotalCost != 3*calls {
		t.Errorf("mesh row = %d calls costing %d, want %d costing %d", mesh.PricedCalls, mesh.TotalCost, calls, 3*calls)
	}
	for _, u := range users {
		if row := rows[userIDKey(u, octoberStart)]; row.PricedCalls != calls/int64(len(users)) {
			t.Errorf("user %s row = %d calls, want %d", u, row.PricedCalls, calls/int64(len(users)))
		}
	}
	if check := checkMesh(t, repo, octoberStart); !check.Consistent() {
		t.Errorf("mesh row no longer equals the sum of its users: %+v", check)
	}
}
