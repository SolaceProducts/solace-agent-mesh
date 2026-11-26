// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const feedbackDetailsMigration = "feedback_details_submitted"

func migrationVersion(t *testing.T, migrations fs.FS, name string) int64 {
	t.Helper()
	matches, err := fs.Glob(migrations, "*_"+name+".sql")
	if err != nil || len(matches) != 1 {
		t.Fatalf("migration %q: matches %v, err %v; want exactly one", name, matches, err)
	}
	prefix, _, _ := strings.Cut(matches[0], "_")
	version, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil {
		t.Fatalf("migration %q: version prefix %q: %v", matches[0], prefix, err)
	}
	return version
}

func feedbackDetailsMigrationVersion(t *testing.T, db *DB) int64 {
	t.Helper()
	migrations := sqliteMigrations()
	if db.Dialect() == "postgres" {
		migrations = postgresMigrations()
	}
	return migrationVersion(t, migrations, feedbackDetailsMigration)
}

func feedbackDetailsMigrationBody(t *testing.T, db *DB) {
	t.Helper()
	ctx := t.Context()
	fbDetailsMigVersion := feedbackDetailsMigrationVersion(t, db)
	insert := func(id, taskID string, comment *string, share, updatedAt int) {
		t.Helper()
		if _, err := db.ExecContext(ctx,
			`INSERT INTO feedback (id, task_id, user_id, session_id, rating, comment, share_content,
				subject_id, subject_type, entrypoint_type, created_at, updated_at)
			 VALUES (?, ?, 'u1', ?, 'down', ?, ?, 'agent-1', 'agent', 'webui', 1, ?)`,
			id, taskID, fbTestSession, comment, share, updatedAt); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	insert("40000000-0000-0000-0000-0000000000a1", "10000000-0000-0000-0000-0000000000a1", nil, 0, 100)
	insert("40000000-0000-0000-0000-0000000000a2", "10000000-0000-0000-0000-0000000000a2", new("legacy comment"), 0, 200)
	insert("40000000-0000-0000-0000-0000000000a3", "10000000-0000-0000-0000-0000000000a3", nil, 1, 300)
	insert("40000000-0000-0000-0000-0000000000a4", "10000000-0000-0000-0000-0000000000a4", new(""), 0, 400)

	if err := db.MigrateUpTo(ctx, fbDetailsMigVersion); err != nil {
		t.Fatalf("migrate up to %d: %v", fbDetailsMigVersion, err)
	}
	type row struct {
		ID      string `db:"id"`
		Details *int64 `db:"details_submitted_time"`
	}
	rows, err := dbutil.QueryRowPtrs[row](ctx, db, `SELECT id, details_submitted_time FROM feedback ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]*int64{
		"40000000-0000-0000-0000-0000000000a1": nil,
		"40000000-0000-0000-0000-0000000000a2": new(int64(200)),
		"40000000-0000-0000-0000-0000000000a3": new(int64(300)),
		"40000000-0000-0000-0000-0000000000a4": nil,
	}
	for _, r := range rows {
		if !ptrEq(r.Details, want[r.ID]) {
			t.Errorf("row %s details_submitted_time = %v, want %v", r.ID, r.Details, want[r.ID])
		}
	}

	repo := NewFeedbackRepo(db)
	_, err = repo.Submit(ctx, submission(FeedbackSubmission{TaskID: "10000000-0000-0000-0000-0000000000a2", Rating: "down", Comment: new("replacement")}))
	if err == nil {
		t.Fatal("legacy row with details must be frozen after the backfill")
	}
	added, err := repo.Submit(ctx, submission(FeedbackSubmission{TaskID: "10000000-0000-0000-0000-0000000000a4", Rating: "down", Comment: new("late reason")}))
	if err != nil || added.Outcome != FeedbackDetailsAdded {
		t.Fatalf("legacy row with an empty comment: outcome %q, err %v; want its details still open", added.Outcome, err)
	}

	if err := db.MigrateDownTo(ctx, fbDetailsMigVersion-1); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	if _, err := db.ExecContext(ctx, `SELECT details_submitted_time FROM feedback`); err == nil {
		t.Fatal("details_submitted_time survived the down migration")
	}
}

func TestB10_FeedbackDetailsMigrationFreezesLegacyDetails(t *testing.T) {
	t.Parallel()
	db, err := Open("sqlite:///"+filepath.Join(t.TempDir(), "feedback_details.db"), nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.MigrateUpTo(t.Context(), feedbackDetailsMigrationVersion(t, db)-1); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	feedbackDetailsMigrationBody(t, db)
}

func TestB10_FeedbackDetailsMigrationFreezesLegacyDetails_Postgres(t *testing.T) {
	t.Parallel()
	dbURL := os.Getenv("SAM_TEST_GATEWAY_DB_URL")
	if !strings.HasPrefix(dbURL, "postgres://") && !strings.HasPrefix(dbURL, "postgresql://") {
		t.Skip("postgres-only test; set SAM_TEST_GATEWAY_DB_URL")
	}
	tmpl, err := dbtest.NewTemplateFromURL(dbURL, "gateway_feedback_details", func(u string) error {
		db, openErr := Open(u, slog.Default())
		if openErr != nil {
			return openErr
		}
		defer func() { _ = db.Close() }()
		return db.MigrateUpTo(context.Background(), migrationVersion(t, postgresMigrations(), feedbackDetailsMigration)-1)
	})
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	t.Cleanup(tmpl.Close)
	db, err := Open(tmpl.Clone(t), slog.Default())
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	feedbackDetailsMigrationBody(t, db)
}
