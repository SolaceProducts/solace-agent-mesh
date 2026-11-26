// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"sync"
	"testing"
	"time"
)

func mustClaim(t *testing.T, repo *WhatsAppProcessedRepo, gatewayID, wamid string, lease time.Duration, want ClaimOutcome) WhatsAppClaim {
	t.Helper()
	got, err := repo.Claim(t.Context(), gatewayID, wamid, lease)
	if err != nil {
		t.Fatalf("Claim(%s,%s): %v", gatewayID, wamid, err)
	}
	if got.Outcome != want {
		t.Fatalf("Claim(%s,%s) outcome = %v, want %v", gatewayID, wamid, got.Outcome, want)
	}
	return got
}

func TestWhatsAppProcessedRepo_ClaimLifecycle(t *testing.T) {
	repo := NewWhatsAppProcessedRepo(newTestDB(t))
	ctx := t.Context()

	claim := mustClaim(t, repo, "gw-1", "wamid.A", time.Minute, ClaimAcquired)
	if state, err := repo.State(ctx, "gw-1", "wamid.A"); err != nil || state != WhatsAppMessageProcessing {
		t.Fatalf("state after Claim = %q (err %v), want %q", state, err, WhatsAppMessageProcessing)
	}
	mustClaim(t, repo, "gw-1", "wamid.A", time.Minute, ClaimInFlight)

	owned, err := repo.MarkDone(ctx, "gw-1", "wamid.A", claim.Token)
	if err != nil || !owned {
		t.Fatalf("MarkDone owned = %v (err %v), want true", owned, err)
	}
	mustClaim(t, repo, "gw-1", "wamid.A", 0, ClaimDuplicate)
}

func TestWhatsAppProcessedRepo_RetryingIsImmediatelyReclaimable(t *testing.T) {
	repo := NewWhatsAppProcessedRepo(newTestDB(t))
	ctx := t.Context()

	claim := mustClaim(t, repo, "gw-1", "wamid.R", time.Hour, ClaimAcquired)
	if claim.Attempts != 0 {
		t.Fatalf("first claim attempts = %d, want 0", claim.Attempts)
	}
	owned, err := repo.ReleaseForRetry(ctx, "gw-1", "wamid.R", claim.Token, "transient")
	if err != nil || !owned {
		t.Fatalf("ReleaseForRetry owned = %v (err %v), want true", owned, err)
	}
	if state, stateErr := repo.State(ctx, "gw-1", "wamid.R"); stateErr != nil || state != WhatsAppMessageRetrying {
		t.Fatalf("state after release = %q (err %v), want %q", state, stateErr, WhatsAppMessageRetrying)
	}

	next := mustClaim(t, repo, "gw-1", "wamid.R", time.Hour, ClaimAcquired)
	if next.Attempts != 1 {
		t.Fatalf("attempts after one failure = %d, want 1", next.Attempts)
	}
	if next.Token == claim.Token {
		t.Fatalf("reclaim reused token %d; each claim needs its own fence", next.Token)
	}
}

func TestWhatsAppProcessedRepo_SettleIsFencedByToken(t *testing.T) {
	repo := NewWhatsAppProcessedRepo(newTestDB(t))
	ctx := t.Context()

	stale := mustClaim(t, repo, "gw-1", "wamid.F", time.Hour, ClaimAcquired)
	live := mustClaim(t, repo, "gw-1", "wamid.F", 0, ClaimAcquired)

	owned, err := repo.ReleaseForRetry(ctx, "gw-1", "wamid.F", stale.Token, "transient")
	if err != nil {
		t.Fatalf("ReleaseForRetry: %v", err)
	}
	if owned {
		t.Fatal("a lapsed claim reported ownership; it must not be able to reset its successor's row")
	}
	if state, stateErr := repo.State(ctx, "gw-1", "wamid.F"); stateErr != nil || state != WhatsAppMessageProcessing {
		t.Fatalf("state after the stale release = %q (err %v), want the successor's %q",
			state, stateErr, WhatsAppMessageProcessing)
	}

	if owned, err = repo.MarkDone(ctx, "gw-1", "wamid.F", stale.Token); err != nil || owned {
		t.Fatalf("MarkDone with a lapsed token owned = %v (err %v), want false", owned, err)
	}
	if owned, err = repo.MarkDone(ctx, "gw-1", "wamid.F", live.Token); err != nil || !owned {
		t.Fatalf("MarkDone with the live token owned = %v (err %v), want true", owned, err)
	}
	mustClaim(t, repo, "gw-1", "wamid.F", 0, ClaimDuplicate)
}

func TestWhatsAppProcessedRepo_GatewayScoped(t *testing.T) {
	repo := NewWhatsAppProcessedRepo(newTestDB(t))
	mustClaim(t, repo, "gw-1", "wamid.SHARED", time.Minute, ClaimAcquired)
	mustClaim(t, repo, "gw-2", "wamid.SHARED", time.Minute, ClaimAcquired)
}

func TestWhatsAppProcessedRepo_ConcurrentClaimHasOneWinner(t *testing.T) {
	repo := NewWhatsAppProcessedRepo(newTestDB(t))
	ctx := t.Context()

	for _, tt := range []struct {
		name  string
		wamid string
		seed  bool
	}{
		{name: "fresh claim", wamid: "wamid.FRESH"},
		{name: "released row takeover", wamid: "wamid.RELEASED", seed: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.seed {
				seeded := mustClaim(t, repo, "gw-1", tt.wamid, time.Minute, ClaimAcquired)
				if _, err := repo.ReleaseForRetry(ctx, "gw-1", tt.wamid, seeded.Token, "transient"); err != nil {
					t.Fatalf("seed ReleaseForRetry: %v", err)
				}
			}
			const racers = 8
			var mu sync.Mutex
			counts := map[ClaimOutcome]int{}
			tokens := map[int64]bool{}
			var wg sync.WaitGroup
			start := make(chan struct{})
			for range racers {
				wg.Go(func() {
					<-start
					got, err := repo.Claim(ctx, "gw-1", tt.wamid, time.Minute)
					if err != nil {
						t.Errorf("Claim: %v", err)
						return
					}
					mu.Lock()
					counts[got.Outcome]++
					if got.Outcome == ClaimAcquired {
						tokens[got.Token] = true
					}
					mu.Unlock()
				})
			}
			close(start)
			wg.Wait()

			if counts[ClaimAcquired] != 1 {
				t.Fatalf("%d racers acquired the claim, want exactly 1 (counts = %v)", counts[ClaimAcquired], counts)
			}
			if len(tokens) != 1 {
				t.Fatalf("winners handed out %d distinct tokens, want 1", len(tokens))
			}
		})
	}
}

func TestWhatsAppProcessedRepo_TakeoverStrictlyAdvancesLease(t *testing.T) {
	db := newTestDB(t)
	repo := NewWhatsAppProcessedRepo(db)
	ctx := t.Context()

	claim := mustClaim(t, repo, "gw-1", "wamid.CAS", time.Minute, ClaimAcquired)

	seeded := time.Now().Add(10 * time.Second).UnixMilli()
	if _, err := db.ExecContext(ctx,
		`UPDATE whatsapp_processed_messages SET state = ?, claimed_at = ? WHERE gateway_id = ? AND wamid = ?`,
		WhatsAppMessageRetrying, seeded, "gw-1", "wamid.CAS",
	); err != nil {
		t.Fatalf("seed a future claim: %v", err)
	}

	takeover := mustClaim(t, repo, "gw-1", "wamid.CAS", 0, ClaimAcquired)
	if takeover.Token <= seeded {
		t.Fatalf("takeover token %d is not greater than the observed %d — a token that does not "+
			"advance leaves the compare-and-set matching for a second consumer", takeover.Token, seeded)
	}
	if takeover.Token <= claim.Token {
		t.Fatalf("takeover token %d did not advance past the previous claim's %d", takeover.Token, claim.Token)
	}
}

func TestWhatsAppProcessedRepo_DeleteOlderThan(t *testing.T) {
	db := newTestDB(t)
	repo := NewWhatsAppProcessedRepo(db)
	ctx := t.Context()

	old := time.Now().Add(-10 * 24 * time.Hour).UnixMilli()
	recent := time.Now().UnixMilli()
	for _, row := range []struct {
		wamid     string
		state     string
		claimedAt int64
	}{
		{"wamid.OLD-DONE", WhatsAppMessageDone, old},
		{"wamid.OLD-STUCK", WhatsAppMessageProcessing, old},
		{"wamid.OLD-RETRYING", WhatsAppMessageRetrying, old},
		{"wamid.NEW-DONE", WhatsAppMessageDone, recent},
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO whatsapp_processed_messages (gateway_id, wamid, state, claimed_at, processed_at, attempts)
			 VALUES (?, ?, ?, ?, NULL, 0)`,
			"gw-1", row.wamid, row.state, row.claimedAt,
		); err != nil {
			t.Fatalf("seed %s: %v", row.wamid, err)
		}
	}

	deleted, err := repo.DeleteOlderThan(ctx, time.Now().Add(-8*24*time.Hour), 2)
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("deleted %d rows, want 3 (every aged row, whatever its state)", deleted)
	}
	for _, gone := range []string{"wamid.OLD-DONE", "wamid.OLD-STUCK", "wamid.OLD-RETRYING"} {
		if state, stateErr := repo.State(ctx, "gw-1", gone); stateErr != nil || state != "" {
			t.Errorf("%s state = %q (err %v), want it pruned", gone, state, stateErr)
		}
	}
	if state, stateErr := repo.State(ctx, "gw-1", "wamid.NEW-DONE"); stateErr != nil || state != WhatsAppMessageDone {
		t.Errorf("recent row state = %q (err %v), want it kept — Meta can still redeliver it", state, stateErr)
	}
}

func TestClaimOutcomeNames(t *testing.T) {
	t.Parallel()
	for got, want := range map[ClaimOutcome]string{
		ClaimAcquired:   "acquired",
		ClaimDuplicate:  "duplicate",
		ClaimInFlight:   "in_flight",
		ClaimOutcome(9): "unknown",
	} {
		if got.String() != want {
			t.Errorf("ClaimOutcome(%d).String() = %q, want %q", int(got), got.String(), want)
		}
	}
}

func TestWhatsAppProcessedRepo_AttemptsAreCountedPerKind(t *testing.T) {
	repo := NewWhatsAppProcessedRepo(newTestDB(t))
	ctx := t.Context()

	for i := range 6 {
		claim := mustClaim(t, repo, "gw-1", "wamid.K", time.Hour, ClaimAcquired)
		if claim.Attempts != i {
			t.Fatalf("transient attempt %d reported attempts = %d", i, claim.Attempts)
		}
		if _, err := repo.ReleaseForRetry(ctx, "gw-1", "wamid.K", claim.Token, "transient"); err != nil {
			t.Fatalf("ReleaseForRetry: %v", err)
		}
	}

	claim := mustClaim(t, repo, "gw-1", "wamid.K", time.Hour, ClaimAcquired)
	if claim.Attempts != 6 || claim.FailureKind != "transient" {
		t.Fatalf("after the outage: attempts = %d kind = %q, want 6 / transient", claim.Attempts, claim.FailureKind)
	}
	if _, err := repo.ReleaseForRetry(ctx, "gw-1", "wamid.K", claim.Token, "unauthorized"); err != nil {
		t.Fatalf("ReleaseForRetry: %v", err)
	}
	next := mustClaim(t, repo, "gw-1", "wamid.K", time.Hour, ClaimAcquired)
	if next.Attempts != 1 || next.FailureKind != "unauthorized" {
		t.Fatalf("first authz failure after an outage: attempts = %d kind = %q, want 1 / unauthorized — "+
			"a shared counter would report 7 and decline immediately", next.Attempts, next.FailureKind)
	}
}

func TestWhatsAppProcessedRepo_LeaseTakeoverCountsAnAttempt(t *testing.T) {
	db := newTestDB(t)
	repo := NewWhatsAppProcessedRepo(db)
	ctx := t.Context()

	lapse := func() {
		t.Helper()
		if _, err := db.ExecContext(ctx,
			`UPDATE whatsapp_processed_messages SET claimed_at = ? WHERE gateway_id = ? AND wamid = ?`,
			time.Now().Add(-time.Hour).UnixMilli(), "gw-1", "wamid.S",
		); err != nil {
			t.Fatalf("backdate claim: %v", err)
		}
	}

	mustClaim(t, repo, "gw-1", "wamid.S", time.Minute, ClaimAcquired)
	for want := 1; want <= 3; want++ {
		lapse()
		got := mustClaim(t, repo, "gw-1", "wamid.S", time.Minute, ClaimAcquired)
		if got.Attempts != want || got.FailureKind != WhatsAppStalledFailure {
			t.Fatalf("takeover %d: attempts = %d kind = %q, want %d / %s",
				want, got.Attempts, got.FailureKind, want, WhatsAppStalledFailure)
		}
		if got.AttemptsTotal != want {
			t.Fatalf("takeover %d: attemptsTotal = %d, want %d", want, got.AttemptsTotal, want)
		}
	}
}

func TestWhatsAppProcessedRepo_TotalAttemptsSurviveKindChanges(t *testing.T) {
	repo := NewWhatsAppProcessedRepo(newTestDB(t))
	ctx := t.Context()

	kinds := []string{"transient", "unauthorized"}
	for i := range 6 {
		claim := mustClaim(t, repo, "gw-1", "wamid.ALT", time.Hour, ClaimAcquired)
		if claim.AttemptsTotal != i {
			t.Fatalf("alternation %d: attemptsTotal = %d, want %d", i, claim.AttemptsTotal, i)
		}
		if i > 0 && claim.Attempts != 1 {
			t.Fatalf("alternation %d: streak = %d, want 1 — the point is that it keeps resetting", i, claim.Attempts)
		}
		if _, err := repo.ReleaseForRetry(ctx, "gw-1", "wamid.ALT", claim.Token, kinds[i%2]); err != nil {
			t.Fatalf("ReleaseForRetry: %v", err)
		}
	}
}

func TestWhatsAppProcessedRepo_MarkDoneCannotResurrectAReleasedRow(t *testing.T) {
	repo := NewWhatsAppProcessedRepo(newTestDB(t))
	ctx := t.Context()

	claim := mustClaim(t, repo, "gw-1", "wamid.Z", time.Hour, ClaimAcquired)
	if _, err := repo.ReleaseForRetry(ctx, "gw-1", "wamid.Z", claim.Token, "transient"); err != nil {
		t.Fatalf("ReleaseForRetry: %v", err)
	}
	owned, err := repo.MarkDone(ctx, "gw-1", "wamid.Z", claim.Token)
	if err != nil || owned {
		t.Fatalf("MarkDone on a released row owned = %v (err %v), want false", owned, err)
	}
	if state, stateErr := repo.State(ctx, "gw-1", "wamid.Z"); stateErr != nil || state != WhatsAppMessageRetrying {
		t.Fatalf("state = %q (err %v), want it still %q", state, stateErr, WhatsAppMessageRetrying)
	}
}
