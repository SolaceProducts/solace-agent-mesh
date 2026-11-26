// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const (
	testTakeoverWindow = checkpointTakeoverAfter
	testPurgeBatch     = 1000
)

func heldOwner(t *testing.T, store session.Store, taskID string) string {
	t.Helper()
	owner, _, err := store.CheckpointOwner(t.Context(), taskID)
	if err != nil {
		t.Fatalf("read checkpoint owner: %v", err)
	}
	return owner
}

func databaseAnswering(c *taskCheckpointer) {
	if c == nil {
		return
	}
	c.healthMu.Lock()
	c.dbAnsweringSince = time.Now().Add(-24 * time.Hour)
	c.healthMu.Unlock()
}

func ownerDies(t *testing.T, store session.Store, taskID string) {
	t.Helper()
	if err := store.ReleaseOwnership(t.Context(), taskID, heldOwner(t, store, taskID)); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func takeOver(t *testing.T, store session.Store, taskID, newOwner string) (*session.TaskCheckpoint, error) {
	t.Helper()
	ownerDies(t, store, taskID)
	return store.AdoptCheckpoint(t.Context(), taskID, newOwner, time.Now(), checkpointLease, checkpointTakeoverAfter, false)
}

func newSQLiteCheckpointStore(t *testing.T) (*session.SQLStore, *dbutil.DB) {
	t.Helper()
	db, err := dbutil.Open("sqlite:///"+filepath.Join(t.TempDir(), "sess.db"), slog.Default())
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := session.NewSQLStore(db)
	if err := store.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store, db
}

func silenceOwner(t *testing.T, db *dbutil.DB, taskID string, d time.Duration) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(),
		`UPDATE task_checkpoints SET renewed_at = renewed_at - ? WHERE task_id = ?`, d.Milliseconds(), taskID); err != nil {
		t.Fatalf("silence owner: %v", err)
	}
}
