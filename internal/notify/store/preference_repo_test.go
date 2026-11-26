// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import "testing"

func TestPreferenceRepo_DefaultEnabledAndOptOut(t *testing.T) {
	ctx := t.Context()
	repo := NewPreferenceRepo(openTestDB(t))

	if dis, err := repo.DisabledChannels(ctx, "trent"); err != nil || len(dis) != 0 {
		t.Fatalf("expected no disabled channels by default, got %v (err=%v)", dis, err)
	}

	if err := repo.Set(ctx, "trent", "email", "", false); err != nil {
		t.Fatalf("Set: %v", err)
	}
	dis, err := repo.DisabledChannels(ctx, "trent")
	if err != nil {
		t.Fatalf("DisabledChannels: %v", err)
	}
	if !dis[PrefKey("email", "")] || len(dis) != 1 {
		t.Fatalf("expected only email disabled, got %v", dis)
	}

	rows, err := repo.List(ctx, "trent")
	if err != nil || len(rows) != 1 || rows[0].ChannelType != "email" || rows[0].Enabled != 0 {
		t.Fatalf("unexpected List result: %+v (err=%v)", rows, err)
	}

	if err := repo.Set(ctx, "trent", "email", "", true); err != nil {
		t.Fatalf("Set re-enable: %v", err)
	}
	if dis, _ := repo.DisabledChannels(ctx, "trent"); len(dis) != 0 {
		t.Fatalf("expected no disabled channels after re-enable, got %v", dis)
	}

	if dis, _ := repo.DisabledChannels(ctx, "other"); len(dis) != 0 {
		t.Fatalf("other user should have no disabled channels, got %v", dis)
	}
}

func TestPreferenceRepo_PerDestination(t *testing.T) {
	ctx := t.Context()
	repo := NewPreferenceRepo(openTestDB(t))

	if err := repo.Set(ctx, "trent", "connector", "slack_send_x", false); err != nil {
		t.Fatalf("Set slack connector: %v", err)
	}
	dis, err := repo.DisabledChannels(ctx, "trent")
	if err != nil {
		t.Fatalf("DisabledChannels: %v", err)
	}
	if !dis[PrefKey("connector", "slack_send_x")] {
		t.Fatalf("slack connector should be disabled, got %v", dis)
	}
	if dis[PrefKey("connector", "teams_send_y")] {
		t.Fatalf("teams connector must remain enabled, got %v", dis)
	}
	if len(dis) != 1 {
		t.Fatalf("only one destination disabled, got %v", dis)
	}

	rows, err := repo.List(ctx, "trent")
	if err != nil || len(rows) != 1 || rows[0].Destination != "slack_send_x" {
		t.Fatalf("List should carry the destination: %+v (err=%v)", rows, err)
	}
}
