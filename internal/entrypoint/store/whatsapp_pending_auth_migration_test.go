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

const pendingAuthMigrationVersion = 48

func requirePendingAuthIndex(t *testing.T, db *DB, present bool) {
	t.Helper()
	var q string
	if db.Dialect() == "postgres" {
		q = `SELECT COUNT(*) FROM pg_indexes WHERE tablename = 'whatsapp_pending_auth' AND indexname = 'ix_whatsapp_pending_auth_expiry'`
	} else {
		q = `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND tbl_name = 'whatsapp_pending_auth' AND name = 'ix_whatsapp_pending_auth_expiry'`
	}
	n, err := dbutil.QueryVal[int64](context.Background(), db, q)
	if err != nil {
		t.Fatalf("index lookup: %v", err)
	}
	if want := map[bool]int64{true: 1, false: 0}[present]; n != want {
		t.Errorf("ix_whatsapp_pending_auth_expiry present = %d, want %d", n, want)
	}
}

func TestWhatsAppPendingAuth_MigrationRoundTrips(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	repo := NewWhatsAppPendingAuthRepo(db)

	if err := repo.Upsert(ctx, nil, pendingAuthRow("15551234567", 1000)); err != nil {
		t.Fatalf("Upsert before the round-trip: %v", err)
	}
	requirePendingAuthIndex(t, db, true)

	if err := db.MigrateDownTo(ctx, pendingAuthMigrationVersion-1); err != nil {
		t.Fatalf("MigrateDownTo(%d): %v", pendingAuthMigrationVersion-1, err)
	}
	if _, err := dbutil.QueryVal[int64](ctx, db, `SELECT COUNT(*) FROM whatsapp_pending_auth`); err == nil {
		t.Fatal("whatsapp_pending_auth is still readable after the down migration: the table was never dropped")
	}
	requirePendingAuthIndex(t, db, false)

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("Migrate back up: %v", err)
	}
	requirePendingAuthIndex(t, db, true)

	live, err := repo.Live(ctx, "wa-gw", "15551234567", 500)
	if err != nil {
		t.Fatalf("Live after the round-trip: %v", err)
	}
	if live != nil {
		t.Errorf("a row survived the round-trip: %+v — the down migration preserved data it claims to drop", live)
	}
	if err := repo.Upsert(ctx, nil, pendingAuthRow("15551234567", 1000)); err != nil {
		t.Fatalf("Upsert after the round-trip: %v", err)
	}
}
