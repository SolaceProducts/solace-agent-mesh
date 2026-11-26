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

func TestDataRetentionService_Cleanup_TeamsFeedbackCorrelationRunsWithRetentionDisabled(t *testing.T) {
	ctx := t.Context()
	db := store.TestDB(t)
	repo := store.NewTeamsFeedbackCorrelationRepo(db)

	const sessionID = "20000000-0000-0000-0000-000000000001"
	for _, row := range []struct {
		messageID string
		taskID    string
		age       time.Duration
	}{
		{"past", "20000000-0000-0000-0000-0000000000a1", TeamsFeedbackCorrelationRetention + time.Hour},
		{"inside", "20000000-0000-0000-0000-0000000000a2", TeamsFeedbackCorrelationRetention / 2},
	} {
		createdAt := time.Now().Add(-row.age).UnixMilli()
		if _, err := db.ExecContext(ctx,
			`INSERT INTO teams_feedback_correlation
			     (conversation_id, message_id, task_id, session_id, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			"conv-1", row.messageID, row.taskID, sessionID, createdAt); err != nil {
			t.Fatalf("seed %s: %v", row.messageID, err)
		}
	}

	svc := NewDataRetentionService(DataRetentionConfig{Enabled: false}, store.NewTaskRepo(db), store.NewFeedbackRepo(db),
		store.NewSSEEventRepo(db), store.NewTaskEventRepo(db), nil, slog.Default()).
		WithTeamsFeedbackCorrelation(repo)
	if !svc.HasTeamsFeedbackCorrelation() {
		t.Fatal("WithTeamsFeedbackCorrelation did not attach the repo")
	}

	if err := svc.cleanup(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if _, ok, err := repo.Get(ctx, "conv-1", "past", 10*TeamsFeedbackCorrelationRetention); err != nil || ok {
		t.Errorf("correlation past the horizon = %v (err %v), want it pruned", ok, err)
	}
	if _, ok, err := repo.Get(ctx, "conv-1", "inside", 10*TeamsFeedbackCorrelationRetention); err != nil || !ok {
		t.Errorf("correlation inside the horizon = %v (err %v), want it kept", ok, err)
	}
}
