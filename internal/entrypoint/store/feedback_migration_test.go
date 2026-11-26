// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const (
	fbMigVersion       = 36
	fbMigBeforeVersion = fbMigVersion - 1

	fbMigSession        = "00000000-0000-0000-0000-000000000051"
	fbMigTaskDup        = "00000000-0000-0000-0000-000000000071"
	fbMigTaskAgent      = "00000000-0000-0000-0000-000000000072"
	fbMigTaskWorkflow   = "00000000-0000-0000-0000-000000000073"
	fbMigTaskFakePrefix = "00000000-0000-0000-0000-000000000074"
	fbMigTaskWfFake     = "00000000-0000-0000-0000-000000000076"
	fbMigTaskEmptyName  = "00000000-0000-0000-0000-000000000077"
	fbMigTaskGone       = "00000000-0000-0000-0000-000000000075"

	fbMigAgentCard    = "agent_019fa42f_536f_71b1_b08a_c96e81ae5831"
	fbMigAgentUUID    = "019fa42f-536f-71b1-b08a-c96e81ae5831"
	fbMigWorkflowCard = "workflow_019e47f4_2be0_75d8_828e_77a0d1d9221f"
	fbMigWorkflowUUID = "019e47f4-2be0-75d8-828e-77a0d1d9221f"
)

func requireFeedbackIndex(t *testing.T, db *DB, name string) {
	t.Helper()
	var q string
	if db.Dialect() == "postgres" {
		q = `SELECT COUNT(*) FROM pg_indexes WHERE tablename = 'feedback' AND indexname = ?`
	} else {
		q = `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = 'feedback' AND name = ?`
	}
	n, err := dbutil.QueryVal[int64](context.Background(), db, q, name)
	if err != nil {
		t.Fatalf("index lookup %s: %v", name, err)
	}
	if n != 1 {
		t.Errorf("index %s missing from the rebuilt feedback table", name)
	}
}

func feedbackMigrationRebuildBody(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()

	seedTask := func(id string, agentName *string) {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO tasks (id, user_id, start_time, execution_mode, agent_name)
			 VALUES (?, 'u1', 1, 'foreground', ?)`, id, agentName); err != nil {
			t.Fatalf("seed task %s: %v", id, err)
		}
	}
	name := func(s string) *string { return &s }
	seedTask(fbMigTaskDup, nil)
	seedTask(fbMigTaskAgent, name(fbMigAgentCard))
	seedTask(fbMigTaskWorkflow, name(fbMigWorkflowCard))
	seedTask(fbMigTaskFakePrefix, name("agent_test"))
	seedTask(fbMigTaskWfFake, name("workflow_test"))
	seedTask(fbMigTaskEmptyName, name(""))

	oldInsert := func(id, taskID, user, rating string, createdTime int, comment *string, share int) {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO feedback (id, session_id, task_id, user_id, rating, comment, created_time, share_content)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			id, fbMigSession, taskID, user, rating, comment, createdTime, share); err != nil {
			t.Fatalf("insert old-shape row %s: %v", id, err)
		}
	}
	oldInsert("40000000-0000-0000-0000-0000000000f9", fbMigTaskDup, "u1", "up", 100, name("old opinion"), 1)
	oldInsert("40000000-0000-0000-0000-0000000000f2", fbMigTaskDup, "u1", "down", 200, name("final opinion"), 1)
	oldInsert("40000000-0000-0000-0000-0000000000d1", fbMigTaskDup, "u4", "up", 700, nil, 0)
	oldInsert("40000000-0000-0000-0000-0000000000d2", fbMigTaskDup, "u4", "down", 700, nil, 0)
	oldInsert("40000000-0000-0000-0000-0000000000e1", fbMigTaskAgent, "u1", "up", 300, nil, 0)
	oldInsert("40000000-0000-0000-0000-0000000000e2", fbMigTaskWorkflow, "u2", "down", 400, name("wf comment"), 0)
	oldInsert("40000000-0000-0000-0000-0000000000e3", fbMigTaskFakePrefix, "u1", "up", 500, nil, 0)
	oldInsert("40000000-0000-0000-0000-0000000000e4", fbMigTaskGone, "u3", "down", 600, name("orphan"), 1)
	oldInsert("40000000-0000-0000-0000-0000000000e5", fbMigTaskWfFake, "u1", "up", 800, nil, 0)
	oldInsert("40000000-0000-0000-0000-0000000000e6", fbMigTaskEmptyName, "u1", "down", 900, nil, 0)

	if err := db.MigrateUpTo(ctx, fbMigVersion); err != nil {
		t.Fatalf("migrate up to %d with old rows present: %v", fbMigVersion, err)
	}

	requireFeedbackIndex(t, db, "idx_feedback_session")
	requireFeedbackIndex(t, db, "idx_feedback_subject_updated")

	type migrated struct {
		ID             string  `db:"id"`
		Rating         string  `db:"rating"`
		Comment        *string `db:"comment"`
		ShareContent   int     `db:"share_content"`
		SubjectID      string  `db:"subject_id"`
		SubjectType    string  `db:"subject_type"`
		EntrypointType string  `db:"entrypoint_type"`
		CreatedAt      int64   `db:"created_at"`
		UpdatedAt      int64   `db:"updated_at"`
	}
	fetch := func(taskID, user string) []*migrated {
		rows, err := dbutil.QueryRowPtrs[migrated](ctx, db,
			`SELECT id, rating, comment, share_content, subject_id, subject_type, entrypoint_type, created_at, updated_at
			 FROM feedback WHERE task_id = ? AND user_id = ?`, taskID, user)
		if err != nil {
			t.Fatalf("fetch %s/%s: %v", taskID, user, err)
		}
		return rows
	}

	dup := fetch(fbMigTaskDup, "u1")
	if len(dup) != 1 {
		t.Fatalf("duplicated pair: got %d rows, want 1", len(dup))
	}
	if dup[0].ID != "40000000-0000-0000-0000-0000000000f2" || dup[0].Rating != "down" || dup[0].CreatedAt != 200 || dup[0].UpdatedAt != 200 {
		t.Errorf("dedup must keep the newest by created_time: got id=%s rating=%s createdAt=%d updatedAt=%d", dup[0].ID, dup[0].Rating, dup[0].CreatedAt, dup[0].UpdatedAt)
	}
	if dup[0].ShareContent != 1 || dup[0].Comment == nil || *dup[0].Comment != "final opinion" {
		t.Errorf("share_content/comment must survive: %+v", dup[0])
	}
	if dup[0].SubjectID != SubjectTypeUnknown || dup[0].SubjectType != SubjectTypeUnknown {
		t.Errorf("unattributed task must yield sentinel subject, got (%s, %s)", dup[0].SubjectID, dup[0].SubjectType)
	}

	assertSubject := func(taskID, user, wantID, wantType string) {
		t.Helper()
		rows := fetch(taskID, user)
		if len(rows) != 1 {
			t.Fatalf("%s: got %d rows, want 1", taskID, len(rows))
		}
		if rows[0].SubjectID != wantID || rows[0].SubjectType != wantType {
			t.Errorf("%s: subject = (%s, %s), want (%s, %s)", taskID, rows[0].SubjectID, rows[0].SubjectType, wantID, wantType)
		}
		if rows[0].EntrypointType != EntrypointTypeUnknown {
			t.Errorf("%s: migrated rows must carry entrypoint_type=unknown, got %s", taskID, rows[0].EntrypointType)
		}
	}
	assertSubject(fbMigTaskAgent, "u1", fbMigAgentUUID, SubjectTypeAgent)
	assertSubject(fbMigTaskWorkflow, "u2", fbMigWorkflowUUID, SubjectTypeWorkflow)
	assertSubject(fbMigTaskFakePrefix, "u1", "agent_test", SubjectTypeAgent)
	assertSubject(fbMigTaskWfFake, "u1", "workflow_test", SubjectTypeAgent)
	assertSubject(fbMigTaskEmptyName, "u1", SubjectTypeUnknown, SubjectTypeUnknown)
	assertSubject(fbMigTaskGone, "u3", SubjectTypeUnknown, SubjectTypeUnknown)

	tie := fetch(fbMigTaskDup, "u4")
	if len(tie) != 1 || tie[0].ID != "40000000-0000-0000-0000-0000000000d2" {
		t.Errorf("timestamp tie must resolve by id DESC: %+v", tie)
	}

	agentRows := fetch(fbMigTaskAgent, "u1")
	if agentRows[0].Comment != nil {
		t.Errorf("NULL comment must stay NULL, got %v", *agentRows[0].Comment)
	}

	newInsert := func(id, taskID, userID string) error {
		_, e := db.RawDB().ExecContext(ctx,
			`INSERT INTO feedback (id, task_id, user_id, session_id, rating, comment, share_content,
				subject_id, subject_type, entrypoint_type, created_at, updated_at)
			 VALUES ('`+id+`', '`+taskID+`', '`+userID+`', '`+fbMigSession+`', 'up', NULL, 0,
				'agent-1', 'agent', 'webui', 1000, 1000)`)
		return e
	}
	if err := newInsert("40000000-0000-0000-0000-0000000000a1", fbMigTaskDup, "u1"); !dbutil.IsUniqueViolation(err) {
		t.Fatalf("insert for a migrated (task_id, user_id): want unique-constraint violation, got %v", err)
	}
	if err := newInsert("40000000-0000-0000-0000-0000000000a2", fbMigTaskDup, "u9"); err != nil {
		t.Fatalf("same task, different user must coexist: %v", err)
	}

	if err := db.MigrateDownTo(ctx, fbMigBeforeVersion); err != nil {
		t.Fatalf("migrate down to %d: %v", fbMigBeforeVersion, err)
	}
	requireFeedbackIndex(t, db, "idx_feedback_session")
	var oldShape struct {
		Rating       string `db:"rating"`
		CreatedTime  int64  `db:"created_time"`
		ShareContent int    `db:"share_content"`
		SessionID    string `db:"session_id"`
	}
	row := db.RawDB().QueryRowContext(ctx,
		`SELECT rating, created_time, share_content, session_id FROM feedback WHERE task_id = '`+fbMigTaskDup+`' AND user_id = 'u1'`)
	if err := row.Scan(&oldShape.Rating, &oldShape.CreatedTime, &oldShape.ShareContent, &oldShape.SessionID); err != nil {
		t.Fatalf("down-migrated row must exist in old shape: %v", err)
	}
	if oldShape.Rating != "down" || oldShape.CreatedTime != 200 {
		t.Errorf("down must map updated_at back to created_time: %+v", oldShape)
	}
	if oldShape.ShareContent != 1 || oldShape.SessionID != fbMigSession {
		t.Errorf("down must carry consent and session attribution through the copy list: %+v", oldShape)
	}
	var postUpCount int
	if err := db.RawDB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM feedback WHERE user_id = 'u9'`).Scan(&postUpCount); err != nil || postUpCount != 1 {
		t.Errorf("row captured after the up must survive the down (count=%d, err=%v)", postUpCount, err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("re-apply after down: %v", err)
	}
	if rows := fetch(fbMigTaskDup, "u1"); len(rows) != 1 {
		t.Errorf("row lost on the second rebuild")
	}
	assertSubject(fbMigTaskDup, "u9", SubjectTypeUnknown, SubjectTypeUnknown)
	assertSubject(fbMigTaskAgent, "u1", fbMigAgentUUID, SubjectTypeAgent)
}

func TestFeedbackMigration_RebuildsAndIsReversible(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "feedback.db")
	db, err := Open("sqlite:///"+dbPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := db.MigrateUpTo(t.Context(), fbMigBeforeVersion); err != nil {
		t.Fatalf("migrate up to %d: %v", fbMigBeforeVersion, err)
	}
	feedbackMigrationRebuildBody(t, db)
}

func TestFeedbackMigration_Postgres_RebuildsAndIsReversible(t *testing.T) {
	t.Parallel()
	dbURL := os.Getenv("SAM_TEST_GATEWAY_DB_URL")
	if !strings.HasPrefix(dbURL, "postgres://") && !strings.HasPrefix(dbURL, "postgresql://") {
		t.Skip("postgres-only test; set SAM_TEST_GATEWAY_DB_URL")
	}

	tmpl, err := dbtest.NewTemplateFromURL(dbURL, "gateway_feedback_rebuild",
		func(u string) error {
			db, openErr := Open(u, slog.Default())
			if openErr != nil {
				return openErr
			}
			defer func() { _ = db.Close() }()
			return db.MigrateUpTo(context.Background(), fbMigBeforeVersion)
		},
	)
	if err != nil {
		t.Fatalf("template: %v", err)
	}
	t.Cleanup(tmpl.Close)

	db, err := Open(tmpl.Clone(t), slog.Default())
	if err != nil {
		t.Fatalf("open clone: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	feedbackMigrationRebuildBody(t, db)

	var n int
	if scanErr := db.RawDB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM pg_constraint
		 WHERE conname IN ('feedback_pkey', 'feedback_task_id_user_id_key')`).Scan(&n); scanErr != nil {
		t.Fatalf("constraint lookup: %v", scanErr)
	}
	if n != 2 {
		t.Errorf("post-swap constraint names: got %d of 2 (feedback_pkey, feedback_task_id_user_id_key)", n)
	}

	var uuidCols int
	if scanErr := db.RawDB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM information_schema.columns
		 WHERE table_name = 'feedback' AND column_name IN ('id', 'task_id', 'session_id')
		   AND data_type = 'uuid'`).Scan(&uuidCols); scanErr != nil {
		t.Fatalf("column type lookup: %v", scanErr)
	}
	if uuidCols != 3 {
		t.Errorf("rebuilt uuid columns: got %d of 3 (id, task_id, session_id)", uuidCols)
	}

	consented, err := NewFeedbackRepo(db).ConsentedTaskIDs(context.Background(), []string{fbMigTaskDup, fbMigTaskAgent})
	if err != nil {
		t.Fatalf("ConsentedTaskIDs across the rebuild: %v", err)
	}
	if !consented[fbMigTaskDup] || consented[fbMigTaskAgent] {
		t.Errorf("consent join = %v, want %s only", consented, fbMigTaskDup)
	}
}
