// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"slices"
	"testing"
	"time"
)

func TestDB_PlatformUserIDForIsEmptyForAnUnseenLogin(t *testing.T) {
	db := openTestDB(t)

	got, err := db.PlatformUserIDFor(t.Context(), adoptLogin)
	if err != nil || got != "" {
		t.Fatalf("PlatformUserIDFor(unseen) = %q, %v; want \"\", nil", got, err)
	}
}

func TestDB_RecordAliasIsLastSeenWins(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	const later = "0192f3a4-0000-7000-8000-000000000002"

	for _, id := range []string{adoptUUID, adoptUUID, later} {
		if err := db.RecordAlias(ctx, adoptLogin, id); err != nil {
			t.Fatalf("RecordAlias(%s): %v", id, err)
		}
	}

	got, err := db.PlatformUserIDFor(ctx, adoptLogin)
	if err != nil || got != later {
		t.Fatalf("PlatformUserIDFor = %q, %v; want the users.id seen last", got, err)
	}
}

func TestDB_EveryUserKeyedTableIsAdopted(t *testing.T) {
	db := openTestDB(t)
	query := `SELECT m.name FROM sqlite_master m JOIN pragma_table_info(m.name) p
	          WHERE m.type = 'table' AND p.name = 'user_id'`
	if db.Dialect() == "postgres" {
		query = `SELECT table_name FROM information_schema.columns
		         WHERE column_name = 'user_id' AND table_schema = current_schema()`
	}
	rows, err := db.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatalf("list user-keyed tables: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	slices.Sort(tables)
	want := slices.Sorted(slices.Values(userKeyedTables))
	if !slices.Equal(tables, want) {
		t.Fatalf("tables with a user_id column = %v, AdoptUser moves %v", tables, want)
	}
}

func startUserChannelConfirmation(t *testing.T, chans *UserChannelRepo, userID, address, codeHash string) {
	t.Helper()
	if err := chans.Upsert(t.Context(), UserChannel{UserID: userID, ChannelType: "slack", Address: address}); err != nil {
		t.Fatalf("seed candidate: %v", err)
	}
	if err := chans.StartConfirmation(t.Context(), userID, "slack", address, codeHash, time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatalf("start confirmation: %v", err)
	}
}

func TestDB_AdoptUserCarriesAnOutstandingLoginCodeToAPlatformChannelWithoutOne(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	chans := NewUserChannelRepo(db)
	startUserChannelConfirmation(t, chans, adoptLogin, `{"login":true}`, "login-hash")
	if err := chans.Upsert(ctx, UserChannel{UserID: adoptUUID, ChannelType: "slack", Address: `{"platform":true}`}); err != nil {
		t.Fatalf("seed platform channel: %v", err)
	}

	if err := db.AdoptUser(ctx, adoptLogin, adoptUUID); err != nil {
		t.Fatalf("AdoptUser: %v", err)
	}

	if outcome, err := chans.ConfirmByCode(ctx, adoptUUID, "slack", "login-hash", 5); err != nil || outcome != ConfirmOK {
		t.Fatalf("confirming with the login's code = %v, %v; want it carried over", outcome, err)
	}
}

func TestDB_AdoptUserKeepsAPlatformChannelsOwnOutstandingCode(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	chans := NewUserChannelRepo(db)
	startUserChannelConfirmation(t, chans, adoptLogin, `{"login":true}`, "login-hash")
	startUserChannelConfirmation(t, chans, adoptUUID, `{"platform":true}`, "platform-hash")

	if err := db.AdoptUser(ctx, adoptLogin, adoptUUID); err != nil {
		t.Fatalf("AdoptUser: %v", err)
	}

	if outcome, _ := chans.ConfirmByCode(ctx, adoptUUID, "slack", "login-hash", 5); outcome == ConfirmOK {
		t.Fatal("the login's code must not replace the platform channel's own")
	}
	if outcome, err := chans.ConfirmByCode(ctx, adoptUUID, "slack", "platform-hash", 5); err != nil || outcome != ConfirmOK {
		t.Fatalf("confirming with the platform channel's code = %v, %v; want ConfirmOK", outcome, err)
	}
}
