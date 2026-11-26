// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var (
	entTestTemplate *dbtest.Template

	platformCoreTemplate *dbtest.Template
)

func TestMain(m *testing.M) {
	tmpl, err := dbtest.NewTemplateFromURL(
		os.Getenv("SAM_TEST_PLATFORM_DB_URL"),
		"enterprise_platform",
		func(u string) error {
			db, openErr := store.Open(u, nil)
			if openErr != nil {
				return openErr
			}
			defer func() { _ = db.Close() }()
			return db.Migrate(context.Background())
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbtest template: %v\n", err)
		os.Exit(1)
	}
	entTestTemplate = tmpl

	coreTmpl, err := dbtest.NewTemplateFromURL("", "platform_core", migratePlatformCoreSchemas)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dbtest platform core template: %v\n", err)
		entTestTemplate.Close()
		os.Exit(1)
	}
	platformCoreTemplate = coreTmpl

	code := m.Run()
	platformCoreTemplate.Close()
	entTestTemplate.Close()
	os.Exit(code)
}

var withheldTemplateMigrations []func(ctx context.Context, db *dbutil.DB) error

func migratePlatformCoreSchemas(dbURL string) error {
	db, err := store.Open(dbURL, nil)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if err := db.Migrate(context.Background()); err != nil {
		return err
	}
	for _, migrate := range withheldTemplateMigrations {
		if err := migrate(context.Background(), db.DB); err != nil {
			return err
		}
	}
	return nil
}

func platformCoreDBURL(t *testing.T) string {
	t.Helper()
	return platformCoreTemplate.Clone(t)
}

func testOpenDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(entTestTemplate.Clone(t), nil)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
