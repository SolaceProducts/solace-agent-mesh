// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"log/slog"
	"testing"

	gatewaystore "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestStoreMigratesIndependentlyOfGateway(t *testing.T) {
	t.Parallel()
	url := "sqlite:///" + t.TempDir() + "/shared.db"
	ctx := context.Background()

	paDB, err := Open(url, slog.Default())
	if err != nil {
		t.Fatalf("open analytics store: %v", err)
	}
	defer func() { _ = paDB.Close() }()
	if migErr := paDB.Migrate(ctx); migErr != nil {
		t.Fatalf("migrate analytics store: %v", migErr)
	}

	gwDB, err := gatewaystore.Open(url, slog.Default())
	if err != nil {
		t.Fatalf("open gateway store: %v", err)
	}
	defer func() { _ = gwDB.Close() }()
	if err := gwDB.Migrate(ctx); err != nil {
		t.Fatalf("gateway migrations must not collide with the analytics store: %v", err)
	}

	for _, table := range []string{"product_analytics_goose_version", "gateway_goose_version"} {
		var name string
		if err := paDB.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, table).Scan(&name); err != nil {
			t.Errorf("expected %s to exist after both stores migrated: %v", table, err)
		}
	}

	if _, _, err := NewProductAnalyticsRepo(paDB).EnsureIdentity(ctx); err != nil {
		t.Errorf("analytics store unusable after gateway migrations ran: %v", err)
	}
}

func TestStoreOwnsItsGooseVersionTable(t *testing.T) {
	t.Parallel()
	db, err := Open("sqlite:///"+t.TempDir()+"/only.db", slog.Default())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	if migErr := db.Migrate(context.Background()); migErr != nil {
		t.Fatalf("migrate: %v", migErr)
	}

	var name string
	err = db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name = 'gateway_goose_version'`).Scan(&name)
	if err == nil {
		t.Error("analytics store created the gateway's version table — it must own its own")
	}
}
