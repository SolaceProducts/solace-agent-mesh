// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
	"time"
)

const (
	adoptLogin = "ben@example.com"
	adoptUUID  = "0192f3a4-0000-7000-8000-000000000001"
)

func TestDB_AdoptUserMovesLoginRowsUnderPlatformID(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	inbox := NewNotificationRepo(db)
	prefs := NewPreferenceRepo(db)
	cats := NewCategoryPreferenceRepo(db)
	chans := NewUserChannelRepo(db)

	if _, err := inbox.Add(ctx, adoptLogin, "Shared", "a chat", nil, "chat_share", ""); err != nil {
		t.Fatalf("seed inbox: %v", err)
	}
	if _, err := inbox.ClaimDedup(ctx, adoptLogin, "k-login"); err != nil {
		t.Fatalf("seed dedup: %v", err)
	}
	if err := prefs.Set(ctx, adoptLogin, "webhook", "wh_1", false); err != nil {
		t.Fatalf("seed pref: %v", err)
	}
	if err := cats.SetCategory(ctx, adoptLogin, "scheduled_task", false); err != nil {
		t.Fatalf("seed category: %v", err)
	}
	if err := chans.Upsert(ctx, UserChannel{UserID: adoptLogin, ChannelType: "slack", Address: `{"user_id":"U1"}`}); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	if has, err := db.HasRowsFor(ctx, adoptLogin); err != nil || !has {
		t.Fatalf("HasRowsFor(login) before = (%v, %v), want true", has, err)
	}
	if err := db.AdoptUser(ctx, adoptLogin, adoptUUID); err != nil {
		t.Fatalf("AdoptUser: %v", err)
	}
	if has, err := db.HasRowsFor(ctx, adoptLogin); err != nil || has {
		t.Fatalf("HasRowsFor(login) after = (%v, %v), want false", has, err)
	}

	if rows, _ := inbox.ListByUser(ctx, adoptUUID, InboxFilter{}, 10, 0); len(rows) != 1 || rows[0].Title != "Shared" {
		t.Fatalf("inbox under platform id = %+v, want the shared notification", rows)
	}
	if claimed, _ := inbox.ClaimDedup(ctx, adoptUUID, "k-login"); claimed {
		t.Fatal("the login id's dedup key must move with it")
	}
	if dis, _ := prefs.DisabledChannels(ctx, adoptUUID); !dis[PrefKey("webhook", "wh_1")] {
		t.Fatalf("channel opt-out did not move: %v", dis)
	}
	if dis, _ := cats.DisabledCategories(ctx, adoptUUID); !dis["scheduled_task"] {
		t.Fatalf("category mute did not move: %v", dis)
	}
	if uc, _ := chans.GetByType(ctx, adoptUUID, "slack"); uc == nil || uc.Address != `{"user_id":"U1"}` {
		t.Fatalf("learned channel did not move: %+v", uc)
	}
}

func TestDB_AdoptUserKeepsPlatformPreferencesAndNewestChannel(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	cats := NewCategoryPreferenceRepo(db)
	chans := NewUserChannelRepo(db)

	if err := cats.SetCategory(ctx, adoptUUID, "agent_message", true); err != nil {
		t.Fatalf("seed platform category: %v", err)
	}
	if err := cats.SetCategory(ctx, adoptLogin, "agent_message", false); err != nil {
		t.Fatalf("seed login category: %v", err)
	}
	if err := chans.Upsert(ctx, UserChannel{UserID: adoptUUID, ChannelType: "slack", Address: `{"old":true}`}); err != nil {
		t.Fatalf("seed platform channel: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE notify_user_channels SET updated_at = 1 WHERE user_id = ?`, adoptUUID); err != nil {
		t.Fatalf("age platform channel: %v", err)
	}
	if err := chans.Upsert(ctx, UserChannel{UserID: adoptLogin, ChannelType: "slack", Address: `{"new":true}`}); err != nil {
		t.Fatalf("seed login channel: %v", err)
	}

	if err := db.AdoptUser(ctx, adoptLogin, adoptUUID); err != nil {
		t.Fatalf("AdoptUser: %v", err)
	}

	if dis, _ := cats.DisabledCategories(ctx, adoptUUID); dis["agent_message"] {
		t.Fatal("a preference already set under the platform id must win")
	}
	if uc, _ := chans.GetByType(ctx, adoptUUID, "slack"); uc == nil || uc.Address != `{"new":true}` {
		t.Fatalf("the more recently seen address must win, got %+v", uc)
	}
	if has, _ := db.HasRowsFor(ctx, adoptLogin); has {
		t.Fatal("no row may stay under the login id")
	}
}

func TestDB_AdoptUserIsANoOpForTheSameKey(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	inbox := NewNotificationRepo(db)
	if _, err := inbox.Add(ctx, adoptLogin, "T", "B", nil, "s", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.AdoptUser(ctx, adoptLogin, adoptLogin); err != nil {
		t.Fatalf("AdoptUser: %v", err)
	}
	if n, _ := inbox.CountByUser(ctx, adoptLogin, InboxFilter{}); n != 1 {
		t.Fatalf("rows under the key = %d, want 1", n)
	}
}

func TestDB_AdoptDeleteKeepsLoginRowsNotYetCopied(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	inbox := NewNotificationRepo(db)
	prefs := NewPreferenceRepo(db)
	cats := NewCategoryPreferenceRepo(db)
	chans := NewUserChannelRepo(db)

	for _, key := range []string{adoptLogin, adoptUUID} {
		if _, err := inbox.ClaimDedup(ctx, key, "k-copied"); err != nil {
			t.Fatalf("seed dedup: %v", err)
		}
		if err := prefs.Set(ctx, key, "webhook", "wh_copied", false); err != nil {
			t.Fatalf("seed pref: %v", err)
		}
		if err := cats.SetCategory(ctx, key, "scheduled_task", false); err != nil {
			t.Fatalf("seed category: %v", err)
		}
	}
	if _, err := inbox.ClaimDedup(ctx, adoptLogin, "k-late"); err != nil {
		t.Fatalf("seed late dedup: %v", err)
	}
	if err := prefs.Set(ctx, adoptLogin, "webhook", "wh_late", false); err != nil {
		t.Fatalf("seed late pref: %v", err)
	}
	if err := cats.SetCategory(ctx, adoptLogin, "agent_message", false); err != nil {
		t.Fatalf("seed late category: %v", err)
	}
	if err := chans.Upsert(ctx, UserChannel{UserID: adoptLogin, ChannelType: "slack", Address: `{"user_id":"U1"}`}); err != nil {
		t.Fatalf("seed late channel: %v", err)
	}

	if err := deleteAdoptedLoginRows(ctx, db, adoptLogin, adoptUUID); err != nil {
		t.Fatalf("deleteAdoptedLoginRows: %v", err)
	}

	if claimed, _ := inbox.ClaimDedup(ctx, adoptLogin, "k-copied"); !claimed {
		t.Fatal("a dedup key already under the platform id must be deleted from the login id")
	}
	if claimed, _ := inbox.ClaimDedup(ctx, adoptLogin, "k-late"); claimed {
		t.Fatal("a dedup key not yet copied must survive under the login id")
	}
	dis, _ := prefs.DisabledChannels(ctx, adoptLogin)
	if dis[PrefKey("webhook", "wh_copied")] || !dis[PrefKey("webhook", "wh_late")] {
		t.Fatalf("login channel prefs after delete = %v, want only wh_late", dis)
	}
	cdis, _ := cats.DisabledCategories(ctx, adoptLogin)
	if cdis["scheduled_task"] || !cdis["agent_message"] {
		t.Fatalf("login category prefs after delete = %v, want only agent_message", cdis)
	}
	if uc, _ := chans.GetByType(ctx, adoptLogin, "slack"); uc == nil {
		t.Fatal("a learned channel not yet copied must survive under the login id")
	}
}

func confirmUserChannel(t *testing.T, chans *UserChannelRepo, userID, channelType, address string) {
	t.Helper()
	if err := chans.Upsert(t.Context(), UserChannel{UserID: userID, ChannelType: channelType, Address: address}); err != nil {
		t.Fatalf("seed candidate: %v", err)
	}
	if err := chans.StartConfirmation(t.Context(), userID, channelType, address, "hash", time.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatalf("start confirmation: %v", err)
	}
	if outcome, err := chans.ConfirmByCode(t.Context(), userID, channelType, "hash", 5); err != nil || outcome != ConfirmOK {
		t.Fatalf("confirm: outcome=%v err=%v", outcome, err)
	}
}

func TestDB_AdoptUserCarriesALoginConfirmationToAnUnconfirmedPlatformChannel(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	chans := NewUserChannelRepo(db)

	confirmUserChannel(t, chans, adoptLogin, "slack", `{"confirmed":true}`)
	if _, err := db.ExecContext(ctx, `UPDATE notify_user_channels SET updated_at = 1 WHERE user_id = ?`, adoptLogin); err != nil {
		t.Fatalf("age login channel: %v", err)
	}
	if err := chans.Upsert(ctx, UserChannel{UserID: adoptUUID, ChannelType: "slack", Address: `{"newer":true}`}); err != nil {
		t.Fatalf("seed platform channel: %v", err)
	}

	if err := db.AdoptUser(ctx, adoptLogin, adoptUUID); err != nil {
		t.Fatalf("AdoptUser: %v", err)
	}

	uc, _ := chans.GetByType(ctx, adoptUUID, "slack")
	if uc == nil || uc.ConfirmedAddress != `{"confirmed":true}` || uc.Address != `{"newer":true}` {
		t.Fatalf("channel = %+v, want the login confirmation kept and the newer presence address", uc)
	}
}

func TestDB_AdoptUserNeverReplacesAPlatformConfirmation(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	chans := NewUserChannelRepo(db)

	confirmUserChannel(t, chans, adoptUUID, "slack", `{"mine":true}`)
	confirmUserChannel(t, chans, adoptLogin, "slack", `{"login":true}`)

	if err := db.AdoptUser(ctx, adoptLogin, adoptUUID); err != nil {
		t.Fatalf("AdoptUser: %v", err)
	}

	if uc, _ := chans.GetByType(ctx, adoptUUID, "slack"); uc == nil || uc.ConfirmedAddress != `{"mine":true}` {
		t.Fatalf("channel = %+v, want the platform confirmation kept", uc)
	}
}
