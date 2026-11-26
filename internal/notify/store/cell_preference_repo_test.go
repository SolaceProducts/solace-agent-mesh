// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"
	"time"
)

func TestCellPreferenceRepo_StoresOnlyOptOutsAndUpserts(t *testing.T) {
	ctx := t.Context()
	repo := NewCellPreferenceRepo(openTestDB(t))

	if err := repo.ReplaceMatrix(ctx, "trent", []Cell{
		{Category: "scheduled_task", ChannelType: "slack", Enabled: false},
		{Category: "scheduled_task", ChannelType: "web", Enabled: true},
		{Category: "agent_message", ChannelType: "webhook", Destination: "wh_1", Enabled: false},
	}, nil); err != nil {
		t.Fatalf("ReplaceMatrix: %v", err)
	}
	disabled, err := repo.DisabledCells(ctx, "trent")
	if err != nil {
		t.Fatalf("DisabledCells: %v", err)
	}
	if len(disabled) != 2 || !disabled[CellKey("scheduled_task", "slack", "")] || !disabled[CellKey("agent_message", "webhook", "wh_1")] {
		t.Fatalf("disabled = %v, want the slack and webhook cells only", disabled)
	}

	if err := repo.ReplaceMatrix(ctx, "trent", []Cell{{Category: "scheduled_task", ChannelType: "slack", Enabled: true}}, nil); err != nil {
		t.Fatalf("ReplaceMatrix re-enable: %v", err)
	}
	disabled, _ = repo.DisabledCells(ctx, "trent")
	if len(disabled) != 1 || disabled[CellKey("scheduled_task", "slack", "")] {
		t.Fatalf("disabled = %v, want the slack cell back on", disabled)
	}
	if other, _ := repo.DisabledCells(ctx, "bob"); len(other) != 0 {
		t.Fatalf("another user has opt-outs: %v", other)
	}
}

func TestCellPreferenceRepo_ReplaceMatrixClearsTheRowAndColumnOptOutsItCovers(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	cells := NewCellPreferenceRepo(db)
	if err := NewCategoryPreferenceRepo(db).SetCategory(ctx, "trent", "scheduled_task", false); err != nil {
		t.Fatalf("mute category: %v", err)
	}
	if err := NewCategoryPreferenceRepo(db).SetCategory(ctx, "trent", "hil", false); err != nil {
		t.Fatalf("mute untouched category: %v", err)
	}
	prefs := NewPreferenceRepo(db)
	if err := prefs.Set(ctx, "trent", "slack", "", false); err != nil {
		t.Fatalf("opt out of channel: %v", err)
	}
	if err := prefs.Set(ctx, "trent", "teams", "", false); err != nil {
		t.Fatalf("opt out of untouched channel: %v", err)
	}

	if err := cells.ReplaceMatrix(ctx, "trent", []Cell{{Category: "scheduled_task", ChannelType: "slack", Enabled: true}}, nil); err != nil {
		t.Fatalf("ReplaceMatrix: %v", err)
	}

	muted, _ := NewCategoryPreferenceRepo(db).DisabledCategories(ctx, "trent")
	if muted["scheduled_task"] || !muted["hil"] {
		t.Errorf("muted categories = %v, want only the untouched hil", muted)
	}
	offChannels, _ := prefs.DisabledChannels(ctx, "trent")
	if offChannels[PrefKey("slack", "")] || !offChannels[PrefKey("teams", "")] {
		t.Errorf("opted-out channels = %v, want only the untouched teams", offChannels)
	}
}

func TestCellPreferenceRepo_PreservedCellsClearNothing(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	if err := NewCategoryPreferenceRepo(db).SetCategory(ctx, "trent", "chat_share", false); err != nil {
		t.Fatalf("mute category: %v", err)
	}
	prefs := NewPreferenceRepo(db)
	if err := prefs.Set(ctx, "trent", "teams", "", false); err != nil {
		t.Fatalf("opt out of channel: %v", err)
	}

	var seen OptOuts
	err := NewCellPreferenceRepo(db).ReplaceMatrix(ctx, "trent",
		[]Cell{{Category: "scheduled_task", ChannelType: "web", Enabled: true}},
		func(optOuts OptOuts) []Cell {
			seen = optOuts
			return []Cell{{Category: "chat_share", ChannelType: "teams", Enabled: false}}
		})
	if err != nil {
		t.Fatalf("ReplaceMatrix: %v", err)
	}

	if !seen.MutedCategories["chat_share"] || !seen.DisabledChannels[PrefKey("teams", "")] {
		t.Errorf("preserve saw %+v, want the mute and opt-out stored before the save", seen)
	}
	if muted, _ := NewCategoryPreferenceRepo(db).DisabledCategories(ctx, "trent"); !muted["chat_share"] {
		t.Errorf("a preserved cell cleared the chat_share mute: %v", muted)
	}
	if off, _ := prefs.DisabledChannels(ctx, "trent"); !off[PrefKey("teams", "")] {
		t.Errorf("a preserved cell cleared the teams opt-out: %v", off)
	}
	disabled, _ := NewCellPreferenceRepo(db).DisabledCells(ctx, "trent")
	if !disabled[CellKey("chat_share", "teams", "")] {
		t.Errorf("the preserved cell was not stored: %v", disabled)
	}
}

func TestCellPreferenceRepo_ReplaceMatrixOnlyTouchesTheUserAndDestinationItNames(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	categories := NewCategoryPreferenceRepo(db)
	prefs := NewPreferenceRepo(db)
	for _, user := range []string{"trent", "bob"} {
		if err := categories.SetCategory(ctx, user, "scheduled_task", false); err != nil {
			t.Fatalf("mute category for %s: %v", user, err)
		}
		for _, destination := range []string{"wh_1", "wh_2"} {
			if err := prefs.Set(ctx, user, "webhook", destination, false); err != nil {
				t.Fatalf("opt %s out of %s: %v", user, destination, err)
			}
		}
	}

	if err := NewCellPreferenceRepo(db).ReplaceMatrix(ctx, "trent", []Cell{{Category: "scheduled_task", ChannelType: "webhook", Destination: "wh_1", Enabled: true}}, nil); err != nil {
		t.Fatalf("ReplaceMatrix: %v", err)
	}

	trentOff, _ := prefs.DisabledChannels(ctx, "trent")
	if trentOff[PrefKey("webhook", "wh_1")] || !trentOff[PrefKey("webhook", "wh_2")] {
		t.Errorf("trent's opted-out destinations = %v, want only wh_2", trentOff)
	}
	if muted, _ := categories.DisabledCategories(ctx, "trent"); muted["scheduled_task"] {
		t.Errorf("trent's mute was not cleared: %v", muted)
	}
	bobOff, _ := prefs.DisabledChannels(ctx, "bob")
	if !bobOff[PrefKey("webhook", "wh_1")] || !bobOff[PrefKey("webhook", "wh_2")] {
		t.Errorf("bob's opt-outs = %v, want both destinations untouched", bobOff)
	}
	if muted, _ := categories.DisabledCategories(ctx, "bob"); !muted["scheduled_task"] {
		t.Errorf("bob's mute was cleared: %v", muted)
	}
}

func TestCellPreferenceRepo_ConcurrentSavesOfOneUserAreSerialized(t *testing.T) {
	const holdBeforeSecondSaveStarts = 300 * time.Millisecond
	ctx := t.Context()
	repo := NewCellPreferenceRepo(openTestDB(t))
	firstCell := Cell{Category: "scheduled_task", ChannelType: "teams", Enabled: false}
	secondCell := Cell{Category: "agent_message", ChannelType: "teams", Enabled: true}

	firstInside := make(chan struct{})
	releaseFirst := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-releaseFirst:
		default:
			close(releaseFirst)
		}
	})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- repo.ReplaceMatrix(ctx, "trent", []Cell{firstCell}, func(OptOuts) []Cell {
			close(firstInside)
			<-releaseFirst
			return nil
		})
	}()
	select {
	case <-firstInside:
	case err := <-firstDone:
		t.Fatalf("first save ended before its preserve callback ran: %v", err)
	}

	secondSaw := make(chan OptOuts, 1)
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- repo.ReplaceMatrix(ctx, "trent", []Cell{secondCell}, func(optOuts OptOuts) []Cell {
			secondSaw <- optOuts
			return nil
		})
	}()

	select {
	case <-secondSaw:
		t.Fatal("the second save read the opt-outs while the first save was still open")
	case <-time.After(holdBeforeSecondSaveStarts):
	}
	close(releaseFirst)

	if err := <-firstDone; err != nil {
		t.Fatalf("first save: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second save: %v", err)
	}
	if seen := <-secondSaw; !seen.DisabledCells[CellKey(firstCell.Category, firstCell.ChannelType, "")] {
		t.Errorf("the second save did not see the first save's committed cell: %+v", seen)
	}
	disabled, _ := repo.DisabledCells(ctx, "trent")
	if !disabled[CellKey(firstCell.Category, firstCell.ChannelType, "")] || disabled[CellKey(secondCell.Category, secondCell.ChannelType, "")] {
		t.Errorf("disabled cells = %v, want the first save's off cell kept and the second's cell on", disabled)
	}
}

func TestCellPreferenceRepo_PreserveCannotOverrideTheRequestOrTurnCellsOn(t *testing.T) {
	ctx := t.Context()
	repo := NewCellPreferenceRepo(openTestDB(t))

	err := repo.ReplaceMatrix(ctx, "trent",
		[]Cell{{Category: "scheduled_task", ChannelType: "teams", Enabled: true}},
		func(OptOuts) []Cell {
			return []Cell{
				{Category: "scheduled_task", ChannelType: "teams", Enabled: false},
				{Category: "agent_message", ChannelType: "teams", Enabled: true},
				{Category: "chat_share", ChannelType: "teams", Enabled: false},
			}
		})
	if err != nil {
		t.Fatalf("ReplaceMatrix: %v", err)
	}

	disabled, _ := repo.DisabledCells(ctx, "trent")
	if len(disabled) != 1 || !disabled[CellKey("chat_share", "teams", "")] {
		t.Errorf("disabled = %v, want only the preserved off cell that was not requested", disabled)
	}
}

func TestCellPreferenceRepo_SaveReadsOnlyTheSavingUsersOptOuts(t *testing.T) {
	ctx := t.Context()
	db := openTestDB(t)
	repo := NewCellPreferenceRepo(db)
	if err := NewCategoryPreferenceRepo(db).SetCategory(ctx, "bob", "chat_share", false); err != nil {
		t.Fatalf("mute category for bob: %v", err)
	}
	if err := NewPreferenceRepo(db).Set(ctx, "bob", "teams", "", false); err != nil {
		t.Fatalf("opt bob out of a channel: %v", err)
	}
	if err := repo.ReplaceMatrix(ctx, "bob", []Cell{{Category: "agent_message", ChannelType: "slack", Enabled: false}}, nil); err != nil {
		t.Fatalf("opt bob out of a cell: %v", err)
	}

	var seen OptOuts
	err := repo.ReplaceMatrix(ctx, "trent", []Cell{{Category: "scheduled_task", ChannelType: "web", Enabled: true}}, func(optOuts OptOuts) []Cell {
		seen = optOuts
		return nil
	})
	if err != nil {
		t.Fatalf("ReplaceMatrix: %v", err)
	}

	if len(seen.MutedCategories) != 0 || len(seen.DisabledChannels) != 0 || len(seen.DisabledCells) != 0 {
		t.Errorf("trent's save saw bob's opt-outs: %+v", seen)
	}
}

func TestOffCellsNotRequested(t *testing.T) {
	requested := []Cell{
		{Category: "scheduled_task", ChannelType: "webhook", Destination: "wh_1", Enabled: true},
		{Category: "agent_message", ChannelType: "web", Enabled: true},
	}
	preserved := []Cell{
		{Category: "scheduled_task", ChannelType: "webhook", Destination: "wh_1", Enabled: false},
		{Category: "scheduled_task", ChannelType: "webhook", Destination: "wh_2", Enabled: false},
		{Category: "chat_share", ChannelType: "teams", Enabled: true},
		{Category: "chat_share", ChannelType: "slack", Enabled: false},
		{Category: "chat_share", ChannelType: "slack", Enabled: false},
	}

	got := offCellsNotRequested(preserved, requested)

	want := []Cell{preserved[1], preserved[3], preserved[4]}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cell %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if offCellsNotRequested(nil, requested) != nil {
		t.Error("nothing preserved should stay nothing")
	}
}
