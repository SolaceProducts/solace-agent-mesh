// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const (
	grantedAt   int64 = 1_000
	periodEnd   int64 = 2_000
	nextPeriod  int64 = 3_000
	afterExpiry int64 = periodEnd
)

type topUpFixture struct {
	assignmentFixture
	topUps *BudgetTopUpsRepo
}

func budgetTopUpRepoTest(t *testing.T, fn func(t *testing.T, f topUpFixture)) {
	t.Helper()
	budgetAssignmentRepoTest(t, func(t *testing.T, f assignmentFixture) {
		f2 := topUpFixture{assignmentFixture: f, topUps: NewBudgetTopUpsRepo(f.db)}
		f2.setAmount(t, SeededDefaultUserBudgetID, new(int64(250)))
		f2.setAmount(t, SeededMeshBudgetID, new(int64(5000)))
		fn(t, f2)
	})
}

func (f topUpFixture) setAmount(t *testing.T, budgetID string, amount *int64) {
	t.Helper()
	if _, err := f.db.ExecContext(t.Context(), `UPDATE budgets SET amount = ?, warn_percentage = NULL WHERE id = ?`, amount, budgetID); err != nil {
		t.Fatalf("set budget %s amount: %v", budgetID, err)
	}
}

func userGrant(ref string, amount int64) BudgetTopUpGrant {
	return BudgetTopUpGrant{
		SubjectType: BudgetSubjectUser, SubjectRef: ref, Amount: amount,
		ExpiresTime: periodEnd, Actor: "granter", NowMs: grantedAt,
	}
}

func meshGrant(amount int64) BudgetTopUpGrant {
	g := userGrant(BudgetMeshSubjectRef, amount)
	g.SubjectType = BudgetSubjectMesh
	return g
}

func (f topUpFixture) grant(t *testing.T, g BudgetTopUpGrant) *BudgetTopUpGranted {
	t.Helper()
	granted, err := f.topUps.Grant(t.Context(), g)
	if err != nil {
		t.Fatalf("grant %+v: %v", g, err)
	}
	return granted
}

func (f topUpFixture) rowCount(t *testing.T) int {
	t.Helper()
	n, err := dbutil.QueryVal[int](t.Context(), f.db, `SELECT COUNT(*) FROM budget_top_ups`)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBudgetTopUpsRepo_GrantThenRegrant(t *testing.T) {
	t.Parallel()
	budgetTopUpRepoTest(t, func(t *testing.T, f topUpFixture) {
		first := f.grant(t, userGrant(refA, 50))
		if !first.Created || first.PreviousAmount != nil {
			t.Errorf("first grant = created %v, previous %v; want created, no previous", first.Created, first.PreviousAmount)
		}
		got := first.TopUp
		if got.SubjectType != BudgetSubjectUser || got.SubjectRef != refA || got.Amount != 50 || got.ExpiresTime != periodEnd ||
			got.CreatedBy != "granter" || got.UpdatedBy != "granter" || got.CreatedTime != grantedAt || got.UpdatedTime != grantedAt {
			t.Errorf("granted = %+v", got)
		}

		regrant := userGrant(refA, 80)
		regrant.Actor, regrant.NowMs = "regranter", grantedAt+10
		second := f.grant(t, regrant)
		if second.Created || second.PreviousAmount == nil || *second.PreviousAmount != 50 {
			t.Errorf("regrant = created %v, previous %v; want replaced 50", second.Created, second.PreviousAmount)
		}
		if second.TopUp.ID != got.ID || second.TopUp.Amount != 80 {
			t.Errorf("regrant = %+v; want %s replaced, not added to", second.TopUp, got.ID)
		}
		if second.TopUp.CreatedBy != "granter" || second.TopUp.UpdatedBy != "regranter" || second.TopUp.UpdatedTime != grantedAt+10 {
			t.Errorf("regrant audit fields = %+v", second.TopUp)
		}
		if n := f.rowCount(t); n != 1 {
			t.Errorf("rows after a regrant = %d, want 1", n)
		}
	})
}

func TestBudgetTopUpsRepo_ExpiredTopUpsAreInvisible(t *testing.T) {
	t.Parallel()
	budgetTopUpRepoTest(t, func(t *testing.T, f topUpFixture) {
		ctx := t.Context()
		old := f.grant(t, userGrant(refA, 50)).TopUp

		if got, err := f.topUps.Active(ctx, BudgetSubjectUser, refA, periodEnd-1); err != nil || got == nil || got.ID != old.ID {
			t.Errorf("Active a millisecond before expiry = %+v, %v; want it still active", got, err)
		}
		if got, err := f.topUps.Active(ctx, BudgetSubjectUser, refA, afterExpiry); err != nil || got != nil {
			t.Errorf("Active after expiry = %+v, %v; want nil", got, err)
		}
		if _, err := f.topUps.Delete(ctx, old.ID, afterExpiry); !errors.Is(err, ErrBudgetTopUpNotFound) {
			t.Errorf("Delete after expiry = %v, want ErrBudgetTopUpNotFound", err)
		}

		next := userGrant(refA, 30)
		next.NowMs, next.ExpiresTime = afterExpiry, nextPeriod
		granted := f.grant(t, next)
		if !granted.Created || granted.TopUp.ID == old.ID {
			t.Errorf("grant in the next period = %+v; want a new row", granted)
		}
		if n := f.rowCount(t); n != 2 {
			t.Errorf("rows = %d, want the expired one kept beside the new", n)
		}
	})
}

func TestBudgetTopUpsRepo_DeleteExpiredRemovesOnlyTopUpsWhosePeriodHasEnded(t *testing.T) {
	t.Parallel()
	budgetTopUpRepoTest(t, func(t *testing.T, f topUpFixture) {
		ctx := t.Context()
		f.grant(t, userGrant(refA, 50))
		f.grant(t, meshGrant(500))
		nextPeriodGrant := userGrant(refB, 30)
		nextPeriodGrant.NowMs, nextPeriodGrant.ExpiresTime = afterExpiry, nextPeriod
		kept := f.grant(t, nextPeriodGrant).TopUp

		if n, err := f.topUps.DeleteExpired(ctx, periodEnd-1); err != nil || n != 0 {
			t.Errorf("DeleteExpired a millisecond before the period ends = %d, %v; want nothing deleted", n, err)
		}
		if n, err := f.topUps.DeleteExpired(ctx, afterExpiry); err != nil || n != 2 {
			t.Errorf("DeleteExpired as the period ends = %d, %v; want the two expired top-ups", n, err)
		}
		if n := f.rowCount(t); n != 1 {
			t.Errorf("rows = %d, want only the next period's top-up", n)
		}
		if got, err := f.topUps.Active(ctx, BudgetSubjectUser, refB, afterExpiry); err != nil || got == nil || got.ID != kept.ID {
			t.Errorf("Active for the next period's subject = %+v, %v; want %s", got, err, kept.ID)
		}

		regrant := userGrant(refA, 20)
		regrant.NowMs, regrant.ExpiresTime = afterExpiry, nextPeriod
		if granted := f.grant(t, regrant); !granted.Created || granted.TopUp.Amount != 20 {
			t.Errorf("grant after the purge = %+v; want a new top-up", granted)
		}
	})
}

func TestBudgetTopUpsRepo_RefusesAnUnlimitedBudget(t *testing.T) {
	t.Parallel()
	budgetTopUpRepoTest(t, func(t *testing.T, f topUpFixture) {
		ctx := t.Context()
		unlimited := createBudget(t, f.budgets, &Budget{Name: "Unlimited"})
		limited := createBudget(t, f.budgets, &Budget{Name: "Engineering", Amount: new(int64(100))})
		f.assign(t, refB, unlimited.ID)
		f.assign(t, refC, limited.ID)
		f.setAmount(t, SeededDefaultUserBudgetID, nil)

		for _, c := range []struct {
			name       string
			grant      BudgetTopUpGrant
			budgetName string
		}{
			{"a user on an unlimited default", userGrant(refA, 50), "Default User"},
			{"a user assigned an unlimited budget", userGrant(refB, 50), "Unlimited"},
		} {
			_, err := f.topUps.Grant(ctx, c.grant)
			unlimitedErr, ok := errors.AsType[*BudgetTopUpUnlimitedError](err)
			if !ok || !errors.Is(err, ErrBudgetTopUpUnlimited) || unlimitedErr.BudgetName != c.budgetName {
				t.Errorf("%s: grant = %v; want unlimited, naming %q", c.name, err, c.budgetName)
			}
		}
		if granted := f.grant(t, userGrant(refC, 50)); !granted.Created {
			t.Error("a user assigned a limited budget was refused while the default is unlimited")
		}

		f.setAmount(t, SeededMeshBudgetID, nil)
		if _, err := f.topUps.Grant(ctx, meshGrant(500)); !errors.Is(err, ErrBudgetTopUpUnlimited) {
			t.Errorf("mesh grant on an unlimited mesh budget = %v, want unlimited", err)
		}
		if n := f.rowCount(t); n != 1 {
			t.Errorf("rows = %d, want only the limited user's", n)
		}
	})
}

func TestBudgetTopUpsRepo_RefusesAMissingUser(t *testing.T) {
	t.Parallel()
	budgetTopUpRepoTest(t, func(t *testing.T, f topUpFixture) {
		_, err := f.topUps.Grant(t.Context(), userGrant("0b5e3c1a-0000-4000-8000-0000000000ff", 50))
		if !errors.Is(err, ErrBudgetTopUpNoSubject) {
			t.Errorf("grant for a missing user = %v, want ErrBudgetTopUpNoSubject", err)
		}
	})
}

func TestBudgetTopUpsRepo_Mesh(t *testing.T) {
	t.Parallel()
	budgetTopUpRepoTest(t, func(t *testing.T, f topUpFixture) {
		first := f.grant(t, meshGrant(500))
		if first.TopUp.SubjectType != BudgetSubjectMesh || first.TopUp.SubjectRef != BudgetMeshSubjectRef ||
			first.TopUp.SubjectDisplayName != nil || first.TopUp.SubjectIsSystemUser {
			t.Errorf("mesh top-up = %+v", first.TopUp)
		}
		second := f.grant(t, meshGrant(700))
		if second.Created || second.TopUp.ID != first.TopUp.ID {
			t.Errorf("a second mesh grant = %+v; want the first replaced", second)
		}
	})
}

func TestBudgetTopUpsRepo_ActiveJoinsTheSubject(t *testing.T) {
	t.Parallel()
	budgetTopUpRepoTest(t, func(t *testing.T, f topUpFixture) {
		bot := seedAssignmentUser(t, f.db, "bot", provisionedViaSystem, nil, new("bot@example.com"))
		f.grant(t, userGrant(bot, 50))
		got, err := f.topUps.Active(t.Context(), BudgetSubjectUser, bot, grantedAt)
		if err != nil || got == nil {
			t.Fatalf("Active bot = %v, %v", got, err)
		}
		if !got.SubjectIsSystemUser || got.SubjectDisplayName != nil || got.SubjectEmail == nil || *got.SubjectEmail != "bot@example.com" {
			t.Errorf("joined subject = %+v", got)
		}
	})
}

func TestBudgetTopUpsRepo_Delete(t *testing.T) {
	t.Parallel()
	budgetTopUpRepoTest(t, func(t *testing.T, f topUpFixture) {
		ctx := t.Context()
		kept := f.grant(t, userGrant(refB, 30)).TopUp
		topUp := f.grant(t, userGrant(refA, 50)).TopUp

		deleted, err := f.topUps.Delete(ctx, topUp.ID, grantedAt)
		if err != nil || deleted.ID != topUp.ID || deleted.Amount != 50 || deleted.SubjectRef != refA {
			t.Fatalf("Delete = %+v, %v; want the top-up as it was", deleted, err)
		}
		if got, err := f.topUps.Active(ctx, BudgetSubjectUser, refA, grantedAt); err != nil || got != nil {
			t.Errorf("Active after delete = %+v, %v; want nil", got, err)
		}
		if _, err := f.topUps.Delete(ctx, topUp.ID, grantedAt); !errors.Is(err, ErrBudgetTopUpNotFound) {
			t.Errorf("a second delete = %v, want ErrBudgetTopUpNotFound", err)
		}
		if _, err := f.topUps.Delete(ctx, "not-a-uuid", grantedAt); !errors.Is(err, ErrBudgetTopUpNotFound) {
			t.Errorf("delete of a malformed id = %v, want ErrBudgetTopUpNotFound", err)
		}
		if got, err := f.topUps.Active(ctx, BudgetSubjectUser, refB, grantedAt); err != nil || got == nil || got.ID != kept.ID {
			t.Errorf("another subject's top-up after the delete = %v, %v; want it kept", got, err)
		}
	})
}

func TestBudgetTopUpsRepo_ConcurrentFirstGrantsConverge(t *testing.T) {
	t.Parallel()
	budgetTopUpRepoTest(t, func(t *testing.T, f topUpFixture) {
		if f.db.Dialect() != "postgres" {
			t.Skip("SQLite serialises writes on one connection")
		}
		ctx := t.Context()
		other, err := f.db.BeginTx(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = other.Rollback() }()
		if err := insertTopUpRow(t, other, topUpRow{
			subjectType: BudgetSubjectUser, subjectRef: refA, amount: 50, expiresTime: periodEnd, createdTime: grantedAt,
		}); err != nil {
			t.Fatal(err)
		}

		type result struct {
			granted *BudgetTopUpGranted
			err     error
		}
		done := make(chan result, 1)
		go func() {
			granted, err := f.topUps.Grant(ctx, userGrant(refA, 80))
			done <- result{granted, err}
		}()
		dbtest.WaitForLockWait(t, f.db, "INSERT INTO budget_top_ups")
		if err := other.Commit(); err != nil {
			t.Fatal(err)
		}

		r := <-done
		if r.err != nil {
			t.Fatalf("grant = %v", r.err)
		}
		if r.granted.Created || r.granted.PreviousAmount == nil || *r.granted.PreviousAmount != 50 || r.granted.TopUp.Amount != 80 {
			t.Errorf("grant = created %v, previous %v, amount %d; want 50 replaced by 80",
				r.granted.Created, r.granted.PreviousAmount, r.granted.TopUp.Amount)
		}
		if n := f.rowCount(t); n != 1 {
			t.Errorf("rows = %d, want 1", n)
		}
	})
}

func TestDeleteSystemUser_RemovesItsTopUps(t *testing.T) {
	t.Parallel()
	budgetTopUpRepoTest(t, func(t *testing.T, f topUpFixture) {
		ctx := t.Context()
		bot := seedAssignmentUser(t, f.db, "orders-bot", provisionedViaSystem, nil, nil)
		f.grant(t, userGrant(bot, 50))
		current := userGrant(bot, 30)
		current.NowMs, current.ExpiresTime = afterExpiry, nextPeriod
		f.grant(t, current)
		f.grant(t, userGrant(refA, 50))
		f.grant(t, meshGrant(500))

		removed, err := deleteSystemUser(ctx, f.assignmentFixture, bot)
		if err != nil || removed.TopUps != 2 || removed.Assignments != 0 {
			t.Fatalf("delete = %+v, %v; want 2 top-ups removed", removed, err)
		}
		if n := f.rowCount(t); n != 2 {
			t.Errorf("rows after the delete = %d, want another user's and the mesh's kept", n)
		}
	})
}

func TestDeleteSystemUser_KeepsItsTopUpsWhenTheDeleteFails(t *testing.T) {
	t.Parallel()
	budgetTopUpRepoTest(t, func(t *testing.T, f topUpFixture) {
		bot := seedAssignmentUser(t, f.db, "orders-bot", provisionedViaSystem, nil, nil)
		f.grant(t, userGrant(bot, 50))
		refuseDeletesFrom(t, f.db, "users")

		if _, err := deleteSystemUser(t.Context(), f.assignmentFixture, bot); err == nil {
			t.Fatal("delete succeeded through the trigger")
		}
		if n := f.rowCount(t); n != 1 {
			t.Errorf("top-ups after the failed delete = %d, want 1", n)
		}
	})
}

func TestListUserSubjects_ShowsOneActiveTopUp(t *testing.T) {
	t.Parallel()
	budgetTopUpRepoTest(t, func(t *testing.T, f topUpFixture) {
		raj := seedAssignmentUser(t, f.db, "raj", "jit", new("Raj"), nil)
		for _, r := range []topUpRow{
			{subjectType: BudgetSubjectUser, subjectRef: raj, amount: 50, expiresTime: periodEnd, createdTime: grantedAt},
			{subjectType: BudgetSubjectUser, subjectRef: raj, amount: 70, expiresTime: nextPeriod, createdTime: grantedAt},
			{subjectType: BudgetSubjectUser, subjectRef: raj, amount: 90, expiresTime: grantedAt, createdTime: grantedAt - 1},
		} {
			withCommit(t, f.db, func(tx *dbutil.Tx) {
				if err := insertTopUpRow(t, tx, r); err != nil {
					t.Fatal(err)
				}
			})
		}

		subjects, err := f.repo.ListUserSubjects(t.Context(), UserBudgetSubjectFilter{Search: "raj", NowMs: grantedAt})
		if err != nil {
			t.Fatal(err)
		}
		if len(subjects) != 1 {
			t.Fatalf("subjects = %d rows, want raj once", len(subjects))
		}
		if got := subjects[0]; got.TopUpAmount == nil || *got.TopUpAmount != 70 || *got.TopUpExpiresTime != nextPeriod {
			t.Errorf("raj's top-up = %v, want the one expiring last", got.TopUpAmount)
		}

		later, err := f.repo.ListUserSubjects(t.Context(), UserBudgetSubjectFilter{IDs: []string{raj}, NowMs: nextPeriod})
		if err != nil || len(later) != 1 || later[0].TopUpID != nil {
			t.Errorf("after every top-up expired = %+v, %v; want raj with none", later, err)
		}
	})
}

func TestBudgetTopUpsRepo_ActivePicksTheLatestExpiry(t *testing.T) {
	t.Parallel()
	budgetTopUpRepoTest(t, func(t *testing.T, f topUpFixture) {
		for _, r := range []topUpRow{
			{subjectType: BudgetSubjectMesh, subjectRef: BudgetMeshSubjectRef, amount: 70, expiresTime: nextPeriod, createdTime: grantedAt},
			{subjectType: BudgetSubjectMesh, subjectRef: BudgetMeshSubjectRef, amount: 50, expiresTime: periodEnd, createdTime: grantedAt},
		} {
			withCommit(t, f.db, func(tx *dbutil.Tx) {
				if err := insertTopUpRow(t, tx, r); err != nil {
					t.Fatal(err)
				}
			})
		}
		got, err := f.topUps.Active(t.Context(), BudgetSubjectMesh, BudgetMeshSubjectRef, grantedAt)
		if err != nil || got == nil || got.Amount != 70 {
			t.Errorf("Active = %+v, %v; want the one expiring last", got, err)
		}
		if got, err := f.topUps.Active(t.Context(), BudgetSubjectMesh, BudgetMeshSubjectRef, nextPeriod); err != nil || got != nil {
			t.Errorf("Active after both expired = %+v, %v; want none", got, err)
		}
	})
}

func TestBudgetTopUpReads_RefuseAMissingTime(t *testing.T) {
	t.Parallel()
	budgetTopUpRepoTest(t, func(t *testing.T, f topUpFixture) {
		ctx := t.Context()
		if _, err := f.topUps.Active(ctx, BudgetSubjectUser, refA, 0); !errors.Is(err, errNoNow) {
			t.Errorf("Active = %v, want errNoNow", err)
		}
		if _, err := f.topUps.Delete(ctx, refA, 0); !errors.Is(err, errNoNow) {
			t.Errorf("Delete = %v, want errNoNow", err)
		}
		if _, err := f.topUps.Active(ctx, BudgetSubjectMesh, BudgetMeshSubjectRef, 0); !errors.Is(err, errNoNow) {
			t.Errorf("Active = %v, want errNoNow", err)
		}
		if _, err := f.topUps.DeleteExpired(ctx, 0); !errors.Is(err, errNoNow) {
			t.Errorf("DeleteExpired = %v, want errNoNow", err)
		}
		if _, err := f.repo.ListUserSubjects(ctx, UserBudgetSubjectFilter{}); !errors.Is(err, errNoNow) {
			t.Errorf("ListUserSubjects = %v, want errNoNow", err)
		}
	})
}

func TestBudgetTopUpsRepo_GrantWaitsForAUserDelete(t *testing.T) {
	t.Parallel()
	budgetTopUpRepoTest(t, func(t *testing.T, f topUpFixture) {
		if f.db.Dialect() != "postgres" {
			t.Skip("SQLite serialises writes on one connection")
		}
		ctx := t.Context()
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
			_, err := f.topUps.Grant(ctx, userGrant(victim, 50))
			done <- err
		}()
		dbtest.WaitForLockWait(t, f.db, "SELECT id, email, sub, issuer, provisioned_via FROM users WHERE id IN")
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}

		if err := <-done; !errors.Is(err, ErrBudgetTopUpNoSubject) {
			t.Errorf("Grant racing the user's delete = %v, want ErrBudgetTopUpNoSubject", err)
		}
		if n := f.rowCount(t); n != 0 {
			t.Errorf("the race left %d orphaned top-ups", n)
		}
	})
}

func TestBudgetTopUpsRepo_GrantAcrossAPeriodBoundary(t *testing.T) {
	t.Parallel()
	budgetTopUpRepoTest(t, func(t *testing.T, f topUpFixture) {
		ctx := t.Context()
		seed := func(ref string, amount, expiresTime, writtenAt int64) {
			t.Helper()
			withCommit(t, f.db, func(tx *dbutil.Tx) {
				if err := insertTopUpRow(t, tx, topUpRow{
					subjectType: BudgetSubjectUser, subjectRef: ref, amount: amount, expiresTime: expiresTime, createdTime: writtenAt,
				}); err != nil {
					t.Fatal(err)
				}
			})
		}
		active := func(ref string) *BudgetTopUp {
			t.Helper()
			got, err := f.topUps.Active(ctx, BudgetSubjectUser, ref, grantedAt)
			if err != nil {
				t.Fatal(err)
			}
			return got
		}
		rowsFor := func(ref string) int {
			t.Helper()
			n, err := dbutil.QueryVal[int](ctx, f.db, `SELECT COUNT(*) FROM budget_top_ups WHERE subject_ref = ?`, ref)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}

		seed(refA, 70, nextPeriod, grantedAt+1)
		if _, err := f.topUps.Grant(ctx, userGrant(refA, 80)); !errors.Is(err, ErrBudgetTopUpPeriodChanged) {
			t.Errorf("grant behind a next-period grant = %v, want ErrBudgetTopUpPeriodChanged", err)
		}
		if got := active(refA); got.Amount != 70 || got.ExpiresTime != nextPeriod || rowsFor(refA) != 1 {
			t.Errorf("next-period top-up = %d until %d (%d rows); want 70 until %d, untouched", got.Amount, got.ExpiresTime, rowsFor(refA), nextPeriod)
		}

		seed(refB, 50, periodEnd, grantedAt-1)
		seed(refB, 70, nextPeriod, grantedAt+1)
		if _, err := f.topUps.Grant(ctx, userGrant(refB, 80)); !errors.Is(err, ErrBudgetTopUpPeriodChanged) {
			t.Errorf("grant beside both periods' top-ups = %v, want ErrBudgetTopUpPeriodChanged", err)
		}
		if got := active(refB); got.Amount != 70 || rowsFor(refB) != 2 {
			t.Errorf("refB = %d active (%d rows); want 70 and both rows kept", got.Amount, rowsFor(refB))
		}

		seed(refC, 90, nextPeriod, grantedAt-1)
		granted := f.grant(t, userGrant(refC, 80))
		if granted.Created || granted.PreviousAmount == nil || *granted.PreviousAmount != 90 ||
			granted.TopUp.Amount != 80 || granted.TopUp.ExpiresTime != periodEnd || rowsFor(refC) != 1 {
			t.Errorf("grant over an older rule's top-up = created %v, previous %v, %d until %d; want 90 replaced by 80 until %d",
				granted.Created, granted.PreviousAmount, granted.TopUp.Amount, granted.TopUp.ExpiresTime, periodEnd)
		}

		seed(refD, 70, periodEnd, grantedAt+1)
		granted = f.grant(t, userGrant(refD, 80))
		if granted.Created || granted.PreviousAmount == nil || *granted.PreviousAmount != 70 || granted.TopUp.Amount != 80 {
			t.Errorf("same-period grant = created %v, previous %v, amount %d; want 70 replaced by 80",
				granted.Created, granted.PreviousAmount, granted.TopUp.Amount)
		}
	})
}

func withCommit(t *testing.T, db *DB, fn func(tx *dbutil.Tx)) {
	t.Helper()
	if err := db.WithTx(t.Context(), func(tx *dbutil.Tx) error { fn(tx); return nil }); err != nil {
		t.Fatal(err)
	}
}
