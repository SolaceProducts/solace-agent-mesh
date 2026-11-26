// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package wire

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

var testTemplate *dbtest.Template

func TestMain(m *testing.M) {
	tmpl, err := dbtest.NewTemplateFromURL(
		os.Getenv("SAM_TEST_PLATFORM_DB_URL"),
		"configapply_wire",
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
	testTemplate = tmpl
	code := m.Run()
	testTemplate.Close()
	os.Exit(code)
}
