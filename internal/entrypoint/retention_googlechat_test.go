// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestDataRetentionService_Cleanup_GoogleChatClaimsRunWithRetentionDisabled(t *testing.T) {
	ctx := t.Context()
	db := store.TestDB(t)
	repo := store.NewGoogleChatProcessedRepo(db)

	for _, row := range []struct {
		key    string
		ageDay int
	}{
		{"spaces/A/messages/PAST", 9},
		{"spaces/A/messages/INSIDE", 6},
	} {
		claimedAt := time.Now().Add(-time.Duration(row.ageDay) * 24 * time.Hour).UnixMilli()
		if _, err := db.ExecContext(ctx,
			`INSERT INTO googlechat_processed_events (gateway_id, event_key, state, claimed_at, processed_at)
			 VALUES (?, ?, 'done', ?, ?)`, "gw-gc", row.key, claimedAt, claimedAt); err != nil {
			t.Fatalf("seed %s: %v", row.key, err)
		}
	}

	svc := NewDataRetentionService(DataRetentionConfig{Enabled: false}, store.NewTaskRepo(db), store.NewFeedbackRepo(db),
		store.NewSSEEventRepo(db), store.NewTaskEventRepo(db), nil, slog.Default()).
		WithGoogleChatClaims(repo)
	if !svc.HasGoogleChatClaims() {
		t.Fatal("WithGoogleChatClaims did not attach the repo")
	}

	if err := svc.cleanup(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if state, err := repo.State(ctx, "gw-gc", "spaces/A/messages/PAST"); err != nil || state != "" {
		t.Errorf("claim past the Pub/Sub horizon = %q (err %v), want it pruned", state, err)
	}
	if state, err := repo.State(ctx, "gw-gc", "spaces/A/messages/INSIDE"); err != nil || state != "done" {
		t.Errorf("claim inside the Pub/Sub horizon = %q (err %v), want it kept", state, err)
	}
}

func TestGoogleChatDedupRetentionClearsPubSubHorizon(t *testing.T) {
	const pubSubMaxRetention = 7 * 24 * time.Hour
	if GoogleChatClaimRetention <= pubSubMaxRetention {
		t.Fatalf("GoogleChatClaimRetention = %v, must exceed Pub/Sub's %v message retention", GoogleChatClaimRetention, pubSubMaxRetention)
	}
}
