// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import "testing"

func TestCategoryPreferenceRepo_DefaultEnabledAndMute(t *testing.T) {
	ctx := t.Context()
	repo := NewCategoryPreferenceRepo(openTestDB(t))

	if dis, err := repo.DisabledCategories(ctx, "trent"); err != nil || len(dis) != 0 {
		t.Fatalf("expected no muted categories by default, got %v (err=%v)", dis, err)
	}

	if err := repo.SetCategory(ctx, "trent", "scheduled_task", false); err != nil {
		t.Fatalf("SetCategory: %v", err)
	}
	dis, err := repo.DisabledCategories(ctx, "trent")
	if err != nil {
		t.Fatalf("DisabledCategories: %v", err)
	}
	if !dis["scheduled_task"] || len(dis) != 1 {
		t.Fatalf("expected only scheduled_task muted, got %v", dis)
	}

	if err := repo.SetCategory(ctx, "trent", "scheduled_task", true); err != nil {
		t.Fatalf("SetCategory re-enable: %v", err)
	}
	if dis, _ := repo.DisabledCategories(ctx, "trent"); len(dis) != 0 {
		t.Fatalf("expected no muted categories after re-enable, got %v", dis)
	}

	if dis, _ := repo.DisabledCategories(ctx, "other"); len(dis) != 0 {
		t.Fatalf("other user should have no muted categories, got %v", dis)
	}
}

func TestCategoryPreferenceRepo_PerCategory(t *testing.T) {
	ctx := t.Context()
	repo := NewCategoryPreferenceRepo(openTestDB(t))

	if err := repo.SetCategory(ctx, "trent", "agent_message", false); err != nil {
		t.Fatalf("SetCategory agent_message: %v", err)
	}
	dis, err := repo.DisabledCategories(ctx, "trent")
	if err != nil {
		t.Fatalf("DisabledCategories: %v", err)
	}
	if !dis["agent_message"] {
		t.Fatalf("agent_message should be muted, got %v", dis)
	}
	if dis["hil"] || dis["scheduled_task"] {
		t.Fatalf("other categories must remain enabled, got %v", dis)
	}
	if len(dis) != 1 {
		t.Fatalf("only one category muted, got %v", dis)
	}
}
