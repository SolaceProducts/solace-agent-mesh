// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

func TestMigrationRoundTripsDownAndBackUp(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	cfg, err := db.migrateConfig()
	if err != nil {
		t.Fatalf("migrate config: %v", err)
	}

	repo := NewProductAnalyticsRepo(db)
	if setErr := repo.SetConsent(ctx, "granted"); setErr != nil {
		t.Fatalf("precondition write: %v", setErr)
	}

	if downErr := dbutil.MigrateDown(ctx, cfg); downErr != nil {
		t.Fatalf("rollback: %v", downErr)
	}
	if _, getErr := repo.Get(ctx); getErr == nil {
		t.Error("expected the table to be gone after rollback")
	}

	if upErr := db.Migrate(ctx); upErr != nil {
		t.Fatalf("re-apply: %v", upErr)
	}

	state, err := repo.ConsentState(ctx)
	if err != nil {
		t.Fatalf("read after re-apply: %v", err)
	}
	if state != "" {
		t.Errorf("consent = %q after a down and up round trip, want empty", state)
	}
	if setErr := repo.SetConsent(ctx, consentStateDenied); setErr != nil {
		t.Errorf("table is not writable after re-apply: %v", setErr)
	}
}
