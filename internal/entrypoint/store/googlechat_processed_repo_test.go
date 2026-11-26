// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
	"time"
)

func TestGoogleChatProcessedRepo_ClaimLifecycle(t *testing.T) {
	repo := NewGoogleChatProcessedRepo(newTestDB(t))
	ctx := t.Context()
	const gw, key = "gw-1", "spaces/AAA/messages/BBB"

	first, err := repo.Claim(ctx, gw, key, time.Minute)
	if err != nil || first.Outcome != ClaimAcquired {
		t.Fatalf("first Claim = (%v, %v), want acquired", first.Outcome, err)
	}

	if inFlight, _ := repo.Claim(ctx, gw, key, time.Minute); inFlight.Outcome != ClaimInFlight {
		t.Fatalf("second Claim inside the lease = %v, want in_flight", inFlight.Outcome)
	}

	if owned, releaseErr := repo.ReleaseForRetry(ctx, gw, key, first.Token, "transient"); releaseErr != nil || !owned {
		t.Fatalf("ReleaseForRetry = (%v, %v), want owned", owned, releaseErr)
	}
	retry, err := repo.Claim(ctx, gw, key, time.Minute)
	if err != nil || retry.Outcome != ClaimAcquired {
		t.Fatalf("Claim after release = (%v, %v), want acquired", retry.Outcome, err)
	}
	if retry.Attempts != 1 || retry.FailureKind != "transient" || retry.AttemptsTotal != 1 {
		t.Errorf("retry claim = %+v, want attempts=1 kind=transient total=1", retry)
	}

	if owned, _ := repo.MarkDone(ctx, gw, key, first.Token); owned {
		t.Error("MarkDone with the superseded token must not settle the successor's claim")
	}
	if owned, err := repo.MarkDone(ctx, gw, key, retry.Token); err != nil || !owned {
		t.Fatalf("MarkDone = (%v, %v), want owned", owned, err)
	}
	if dup, _ := repo.Claim(ctx, gw, key, time.Minute); dup.Outcome != ClaimDuplicate {
		t.Errorf("Claim after done = %v, want duplicate", dup.Outcome)
	}
	if state, _ := repo.State(ctx, gw, key); state != inboundClaimDone {
		t.Errorf("State = %q, want done", state)
	}
}

func TestGoogleChatProcessedRepo_LapsedLeaseIsTakenOverAsStalled(t *testing.T) {
	repo := NewGoogleChatProcessedRepo(newTestDB(t))
	ctx := t.Context()

	if _, err := repo.Claim(ctx, "gw-1", "k", time.Minute); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	takeover, err := repo.Claim(ctx, "gw-1", "k", 0)
	if err != nil || takeover.Outcome != ClaimAcquired {
		t.Fatalf("takeover Claim = (%v, %v), want acquired", takeover.Outcome, err)
	}
	if takeover.FailureKind != StalledClaimFailure || takeover.Attempts != 1 {
		t.Errorf("takeover = %+v, want a stalled attempt", takeover)
	}
}

func TestGoogleChatProcessedRepo_ScopedByGatewayAndPrunedByAge(t *testing.T) {
	repo := NewGoogleChatProcessedRepo(newTestDB(t))
	ctx := t.Context()

	for _, gw := range []string{"gw-1", "gw-2"} {
		if c, err := repo.Claim(ctx, gw, "same-key", time.Minute); err != nil || c.Outcome != ClaimAcquired {
			t.Fatalf("Claim(%s) = (%v, %v), want acquired: gateways must not share keys", gw, c.Outcome, err)
		}
	}

	deleted, err := repo.DeleteOlderThan(ctx, time.Now().Add(time.Hour), 1)
	if err != nil {
		t.Fatalf("DeleteOlderThan: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2 (batched until empty)", deleted)
	}
}
