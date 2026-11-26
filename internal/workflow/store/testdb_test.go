// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
)

var getTestTemplate = sync.OnceValues(func() (*dbtest.Template, error) {
	return dbtest.NewTemplateFromURL(
		os.Getenv("SAM_TEST_WORKFLOW_DB_URL"),
		fmt.Sprintf("workflow_store_%d", os.Getpid()),
		func(u string) error {
			db, err := Open(u, nil)
			if err != nil {
				return err
			}
			defer func() { _ = db.Close() }()
			return db.Migrate(context.Background())
		},
	)
})

func openTestDB(t *testing.T) *DB {
	t.Helper()
	t.Parallel()
	tmpl, err := getTestTemplate()
	if err != nil {
		t.Fatalf("dbtest template: %v", err)
	}
	db, err := Open(tmpl.Clone(t), nil)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
