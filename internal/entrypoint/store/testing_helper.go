// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

var getTestTemplate = sync.OnceValues(func() (*dbtest.Template, error) {
	return dbtest.NewTemplateFromURL(
		os.Getenv("SAM_TEST_GATEWAY_DB_URL"),
		perProcessTemplateName(),
		func(u string) error {
			db, err := Open(u, slog.Default())
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }()
			return db.Migrate(context.Background())
		},
	)
})

func perProcessTemplateName() string {
	return fmt.Sprintf("gateway_store_xpkg_%d", os.Getpid())
}

func TestDB(t *testing.T) *DB {
	t.Helper()
	t.Parallel()
	tmpl, err := getTestTemplate()
	if err != nil {
		t.Fatalf("dbtest template: %v", err)
	}
	db, err := Open(tmpl.Clone(t), slog.Default())
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestDBURL(t *testing.T) string {
	t.Helper()
	tmpl, err := getTestTemplate()
	if err != nil {
		t.Fatalf("dbtest template: %v", err)
	}
	return tmpl.Clone(t)
}

func TestSQLiteDBURL(t *testing.T) string {
	t.Helper()
	if dbURL := os.Getenv("SAM_TEST_GATEWAY_DB_URL"); strings.HasPrefix(dbURL, "postgres://") || strings.HasPrefix(dbURL, "postgresql://") {
		t.Skip("sqlite-only test; the SQLite gateway job runs it")
	}
	return TestDBURL(t)
}

func SeedFeedback(t *testing.T, db *DB, f *Feedback) {
	t.Helper()
	now := time.Now().UnixMilli()
	f.ID = cmp.Or(f.ID, idgen.New())
	f.SubjectID = cmp.Or(f.SubjectID, "agent-1")
	f.SubjectType = cmp.Or(f.SubjectType, SubjectTypeAgent)
	f.EntrypointType = cmp.Or(f.EntrypointType, EntrypointTypeWebUI)
	f.CreatedAt = cmp.Or(f.CreatedAt, now)
	f.UpdatedAt = cmp.Or(f.UpdatedAt, now)
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO feedback (`+feedbackColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, f.TaskID, f.UserID, f.SessionID, f.Rating, f.Comment, dbutil.BoolToInt(f.ShareContent),
		f.SubjectID, f.SubjectType, f.EntrypointType, f.DetailsSubmittedTime, f.CreatedAt, f.UpdatedAt,
	); err != nil {
		t.Fatalf("seed feedback %s: %v", f.TaskID, err)
	}
}
