// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"path/filepath"
	"testing"
)

func TestEvalTakeoverReportsADatabaseFailure(t *testing.T) {
	db, err := Open("sqlite:///"+filepath.Join(t.TempDir(), "closed.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	runs := NewEvalRunRepo(db)
	generations := NewEvalGenerationOperationRepo(db)
	checks := map[string]error{
		"pending runs":       func() error { _, err := runs.ListPendingIDsBefore(ctx, 0); return err }(),
		"run takeover":       func() error { _, _, err := runs.TakeOver(ctx, "r1"); return err }(),
		"half-saved results": func() error { _, err := runs.DeleteHalfSavedResults(ctx, "r1", []string{"e1"}); return err }(),
		"reset progress":     runs.ResetProgress(ctx, "r1", 0),
		"fail running":       func() error { _, err := runs.FailRunning(ctx, "r1", "stopped"); return err }(),
		"cancel active":      func() error { _, _, err := runs.CancelActive(ctx, "r1"); return err }(),
		"generations":        func() error { _, err := generations.ListInProgressBefore(ctx, 0); return err }(),
		"generation takeover": func() error {
			_, _, err := generations.TakeOver(ctx, "g1", 0)
			return err
		}(),
	}
	for name, err := range checks {
		if err == nil {
			t.Errorf("%s on a closed database reported no error", name)
		}
	}
}
