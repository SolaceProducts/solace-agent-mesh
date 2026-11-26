// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const versionBeforeNotificationCategory = 8

func TestNotificationCategoryMigration(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), versionBeforeNotificationCategory); err != nil {
		t.Fatalf("migrate down to %d: %v", versionBeforeNotificationCategory, err)
	}

	const legacyID = "11111111-1111-1111-1111-111111111111"
	if _, err := db.ExecContext(ctx,
		`INSERT INTO notifications (id, user_id, title, body, payload, source, status, created_at)
		 VALUES (?, 'trent', 'legacy', 'b', '', 'notify_user', 'unread', 1)`, legacyID); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	n, err := NewNotificationRepo(db).Get(ctx, "trent", legacyID)
	if err != nil {
		t.Fatalf("Get legacy row: %v", err)
	}
	if n.Category != "" {
		t.Errorf("legacy row category = %q, want uncategorized", n.Category)
	}

	if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), versionBeforeNotificationCategory); err != nil {
		t.Fatalf("migrate down again: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate up again: %v", err)
	}
}
