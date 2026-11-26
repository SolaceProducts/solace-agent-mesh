// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
)

func TestSeedState_MarkAndProbe(t *testing.T) {
	t.Parallel()

	db := openTemplateClone(t, migratedSQLiteTemplate)
	ctx := t.Context()

	const key = "sample_key"

	applied, err := IsSeedApplied(ctx, db, key)
	if err != nil {
		t.Fatalf("IsSeedApplied (absent): %v", err)
	}
	if applied {
		t.Fatal("marker must be absent before it is written")
	}

	if err = MarkSeedApplied(ctx, db, key); err != nil {
		t.Fatalf("MarkSeedApplied: %v", err)
	}

	applied, err = IsSeedApplied(ctx, db, key)
	if err != nil {
		t.Fatalf("IsSeedApplied (present): %v", err)
	}
	if !applied {
		t.Fatal("marker must be present after it is written")
	}

	if err = MarkSeedApplied(ctx, db, key); err != nil {
		t.Fatalf("MarkSeedApplied (idempotent): %v", err)
	}

	if err = db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err = IsSeedApplied(ctx, db, key); err == nil {
		t.Error("IsSeedApplied must error on a closed DB")
	}
	if err = MarkSeedApplied(ctx, db, key); err == nil {
		t.Error("MarkSeedApplied must error on a closed DB")
	}
}
