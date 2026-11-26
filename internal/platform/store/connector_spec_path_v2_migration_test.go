// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

const (
	specPathOwnV1ID     = "0190f5ec-6f7a-7b8c-ad0e-000000000001"
	specPathTypedID     = "0190f5ec-6f7a-7b8c-ad0e-000000000002"
	specPathOtherID     = "0190f5ec-6f7a-7b8c-ad0e-000000000003"
	specPathOwnV2ID     = "0190f5ec-6f7a-7b8c-ad0e-000000000004"
	specPathHostID      = "0190f5ec-6f7a-7b8c-ad0e-000000000005"
	specPathMalformedID = "0190f5ec-6f7a-7b8c-ad0e-000000000006"
)

type specPathRow struct {
	id, before, afterUp string
}

func specPathRows() []specPathRow {
	ownV1 := `{"base_url":"https://api.example.com","specification_url":"/api/v1/platform/connectors/` + specPathOwnV1ID + `/resource"}`
	ownV1Migrated := `{"base_url":"https://api.example.com","specification_url":"/api/v2/platform/connectors/` + specPathOwnV1ID + `/resource"}`
	typed := `{"specification_url":"https://specs.example.com/api/v1/platform/connectors/` + specPathTypedID + `/resource"}`
	other := `{"specification_url":"/api/v1/platform/connectors/` + specPathOtherID + `-not-mine/resource"}`
	ownV2 := `{"specification_url":"/api/v2/platform/connectors/` + specPathOwnV2ID + `/resource"}`
	withHost := `{"specification_url":"https://sam.example.test/api/v1/platform/connectors/` + specPathHostID + `/resource"}`
	malformed := `{"specification_url":"/api/v1/platform/connectors/` + specPathMalformedID
	return []specPathRow{
		{specPathOwnV1ID, ownV1, ownV1Migrated},
		{specPathTypedID, typed, typed},
		{specPathOtherID, other, other},
		{specPathOwnV2ID, ownV2, ownV2},
		{specPathHostID, withHost, withHost},
		{specPathMalformedID, malformed, malformed},
	}
}

func TestConnectorSpecPathV2Migration_RoundTrip(t *testing.T) {
	t.Parallel()
	forEachPlatformDialectAt(t, "connector_spec_path_v2", func(t *testing.T, db *DB, version int64) {
		ctx := t.Context()
		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate up to %d: %v", version-1, err)
		}
		rows := specPathRows()
		for _, r := range rows {
			if _, err := db.ExecContext(ctx, `INSERT INTO connectors (id, name, description, type, subtype, "values", resource_key, created_by, updated_by, created_time, updated_time)
				VALUES (?, ?, '', 'api', 'openapi', ?, ?, 'alice', 'alice', 1, 1)`, r.id, "conn-"+r.id, r.before, "connectors/"+r.id+"/spec.yaml"); err != nil {
				t.Fatalf("seed connector %s: %v", r.id, err)
			}
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("migrate up to %d: %v", version, err)
		}
		for _, r := range rows {
			assertConnectorValues(t, db, "after up", r.id, r.afterUp)
		}

		if err := dbutil.MigrateDownTo(ctx, db.migrateConfig(), version-1); err != nil {
			t.Fatalf("migrate down to %d: %v", version-1, err)
		}
		for _, r := range rows {
			want := r.before
			if r.id == specPathOwnV2ID {
				want = `{"specification_url":"/api/v1/platform/connectors/` + specPathOwnV2ID + `/resource"}`
			}
			assertConnectorValues(t, db, "after down", r.id, want)
		}

		if err := dbutil.MigrateUpTo(ctx, db.migrateConfig(), version); err != nil {
			t.Fatalf("re-apply after rollback: %v", err)
		}
		for _, r := range rows {
			want := r.afterUp
			if r.id == specPathOwnV2ID {
				want = r.before
			}
			assertConnectorValues(t, db, "after re-apply", r.id, want)
		}
	})
}

func assertConnectorValues(t *testing.T, db *DB, stage, id, want string) {
	t.Helper()
	var got string
	if err := db.QueryRowContext(t.Context(), `SELECT "values" FROM connectors WHERE id = ?`, id).Scan(&got); err != nil {
		t.Fatalf("%s: read connector %s: %v", stage, id, err)
	}
	if got != want {
		t.Errorf("%s: connector %s values = %s, want %s", stage, id, got, want)
	}
}
