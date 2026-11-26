// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestModelConfigRepo_CRUD(t *testing.T) {
	db := TestDB(t)
	repo := NewModelConfigRepo(db)
	ctx := context.Background()

	mc := &ModelConfiguration{
		Alias:           "gpt4-turbo",
		Provider:        "openai",
		ModelName:       "gpt-4-turbo",
		ModelAuthType:   "apikey",
		ModelAuthConfig: `{"type":"apikey","api_key":"sk-test"}`,
		ModelParams:     `{"temperature":0.7}`,
		CreatedBy:       "user1",
		UpdatedBy:       "user1",
	}
	if err := repo.Create(ctx, mc); err != nil {
		t.Fatalf("create: %v", err)
	}
	if mc.ID == "" {
		t.Fatal("expected ID to be set")
	}
	if mc.CreatedTime == 0 {
		t.Fatal("expected CreatedTime to be set")
	}

	got, err := repo.GetByAlias(ctx, "gpt4-turbo")
	if err != nil {
		t.Fatalf("get by alias: %v", err)
	}
	if got == nil {
		t.Fatal("expected model config")
	}
	if got.Provider != "openai" {
		t.Errorf("provider = %q, want openai", got.Provider)
	}
	if got.ModelName != "gpt-4-turbo" {
		t.Errorf("model_name = %q, want gpt-4-turbo", got.ModelName)
	}

	missing, err := repo.GetByAlias(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if missing != nil {
		t.Fatal("expected nil for missing alias")
	}

	gotAlias, err := repo.GetByAliasOrID(ctx, "gpt4-turbo")
	if err != nil {
		t.Fatalf("get by alias or id (alias): %v", err)
	}
	if gotAlias == nil || gotAlias.ID != mc.ID {
		t.Fatal("expected model config by alias")
	}

	gotID, err := repo.GetByAliasOrID(ctx, mc.ID)
	if err != nil {
		t.Fatalf("get by alias or id (id): %v", err)
	}
	if gotID == nil || gotID.Alias != "gpt4-turbo" {
		t.Fatal("expected model config by ID")
	}

	for _, spelling := range []string{strings.ToUpper(mc.ID), "{" + mc.ID + "}"} {
		gotSpelled, lookupErr := repo.GetByAliasOrID(ctx, spelling)
		if lookupErr != nil {
			t.Fatalf("get by alias or id (%s): %v", spelling, lookupErr)
		}
		if gotSpelled == nil || gotSpelled.ID != mc.ID {
			t.Fatalf("GetByAliasOrID(%q) = %+v, want the row with id %s", spelling, gotSpelled, mc.ID)
		}
	}

	gotNone, err := repo.GetByAliasOrID(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	if err != nil {
		t.Fatalf("get by alias or id (missing): %v", err)
	}
	if gotNone != nil {
		t.Fatal("expected nil for missing alias/id")
	}

	gotNonUUID, err := repo.GetByAliasOrID(ctx, "not-a-uuid-and-not-an-alias")
	if err != nil {
		t.Fatalf("get by alias or id (non-uuid): %v", err)
	}
	if gotNonUUID != nil {
		t.Fatal("expected nil for non-uuid lookup")
	}

	gotNonUUID, err = repo.GetByID(ctx, "not-a-uuid")
	if err != nil {
		t.Fatalf("get by id (non-uuid): %v", err)
	}
	if gotNonUUID != nil {
		t.Fatal("expected nil for non-uuid id")
	}

	exists, err := repo.ExistsByAlias(ctx, "gpt4-turbo")
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if !exists {
		t.Error("expected alias to exist")
	}
	exists, err = repo.ExistsByAlias(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("exists nonexistent: %v", err)
	}
	if exists {
		t.Error("expected alias not to exist")
	}

	count, err := repo.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("count = %d, want 1", count)
	}

	dup := &ModelConfiguration{
		Alias:           "gpt4-turbo",
		Provider:        "openai",
		ModelName:       "gpt-4-turbo-2",
		ModelAuthType:   "none",
		ModelAuthConfig: `{}`,
		ModelParams:     `{}`,
		CreatedBy:       "user1",
		UpdatedBy:       "user1",
	}
	if err = repo.Create(ctx, dup); err == nil {
		t.Fatal("expected error for duplicate alias")
	}

	got.Provider = "anthropic"
	got.ModelName = "claude-sonnet"
	got.UpdatedBy = "user2"
	if err = repo.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	updated, err := repo.GetByAlias(ctx, "gpt4-turbo")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if updated.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", updated.Provider)
	}
	if updated.UpdatedBy != "user2" {
		t.Errorf("updated_by = %q, want user2", updated.UpdatedBy)
	}
	if updated.UpdatedTime < mc.CreatedTime {
		t.Error("expected updated_time >= created_time")
	}

	time.Sleep(2 * time.Millisecond)

	mc2 := &ModelConfiguration{
		Alias:           "claude-3",
		Provider:        "anthropic",
		ModelName:       "claude-3-opus",
		ModelAuthType:   "apikey",
		ModelAuthConfig: `{"type":"apikey","api_key":"sk-ant-test"}`,
		ModelParams:     `{}`,
		CreatedBy:       "user1",
		UpdatedBy:       "user1",
	}
	if err = repo.Create(ctx, mc2); err != nil {
		t.Fatalf("create second: %v", err)
	}

	all, err := repo.GetAll(ctx)
	if err != nil {
		t.Fatalf("get all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("len(all) = %d, want 2", len(all))
	}
	if all[0].Alias != "claude-3" {
		t.Errorf("first = %q, want claude-3 (most recent)", all[0].Alias)
	}

	count, err = repo.Count(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}

	if err = repo.Delete(ctx, mc.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	gone, err := repo.GetByAlias(ctx, "gpt4-turbo")
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if gone != nil {
		t.Fatal("expected nil after delete")
	}

	if err = repo.Delete(ctx, "nonexistent-id"); err == nil {
		t.Fatal("expected error deleting non-existent ID")
	}
}

func TestModelConfigRepo_CaseSensitiveAlias(t *testing.T) {
	db := TestDB(t)
	repo := NewModelConfigRepo(db)
	ctx := context.Background()

	mc := &ModelConfiguration{
		Alias:           "General",
		Provider:        "openai",
		ModelName:       "gpt-4",
		ModelAuthType:   "none",
		ModelAuthConfig: `{}`,
		ModelParams:     `{}`,
		CreatedBy:       "user1",
		UpdatedBy:       "user1",
	}
	if err := repo.Create(ctx, mc); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByAlias(ctx, "General")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected to find General")
	}

	lower, err := repo.GetByAlias(ctx, "general")
	if err != nil {
		t.Fatalf("get lowercase: %v", err)
	}
	if lower != nil {
		t.Fatal("expected case-sensitive miss for 'general' vs 'General'")
	}
}

func TestModelConfigRepo_NullableFields(t *testing.T) {
	db := TestDB(t)
	repo := NewModelConfigRepo(db)
	ctx := context.Background()

	mc := &ModelConfiguration{
		Alias:           "minimal",
		Provider:        "openai",
		ModelName:       "gpt-4",
		ModelAuthType:   "none",
		ModelAuthConfig: `{}`,
		ModelParams:     `{}`,
		CreatedBy:       "user1",
		UpdatedBy:       "user1",
	}
	if err := repo.Create(ctx, mc); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByAlias(ctx, "minimal")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.APIBase != nil {
		t.Errorf("api_base should be nil, got %v", got.APIBase)
	}
	if got.Description != nil {
		t.Errorf("description should be nil, got %v", got.Description)
	}

	apiBase := "https://api.openai.com/v1"
	desc := "Test model"
	mc2 := &ModelConfiguration{
		Alias:           "full",
		Provider:        "openai",
		ModelName:       "gpt-4",
		APIBase:         &apiBase,
		ModelAuthType:   "apikey",
		ModelAuthConfig: `{"type":"apikey","api_key":"sk-test"}`,
		ModelParams:     `{"temperature":0.5}`,
		Description:     &desc,
		CreatedBy:       "user1",
		UpdatedBy:       "user1",
	}
	if err = repo.Create(ctx, mc2); err != nil {
		t.Fatalf("create full: %v", err)
	}

	got2, err := repo.GetByAlias(ctx, "full")
	if err != nil {
		t.Fatalf("get full: %v", err)
	}
	if got2.APIBase == nil || *got2.APIBase != apiBase {
		t.Errorf("api_base = %v, want %q", got2.APIBase, apiBase)
	}
	if got2.Description == nil || *got2.Description != desc {
		t.Errorf("description = %v, want %q", got2.Description, desc)
	}
}

func TestModelConfigRepo_PriceColumnsRoundTripIndividually(t *testing.T) {
	db := TestDB(t)
	repo := NewModelConfigRepo(db)
	ctx := context.Background()

	priced := func(in, out, read, write int64) *ModelConfiguration {
		return &ModelConfiguration{
			Alias:           "priced",
			Provider:        "openai",
			ModelName:       "gpt-4",
			ModelAuthType:   "none",
			ModelAuthConfig: `{}`,
			ModelParams:     `{}`,
			CreatedBy:       "user1",
			UpdatedBy:       "user1",
			PriceInput:      &in,
			PriceOutput:     &out,
			PriceCacheRead:  &read,
			PriceCacheWrite: &write,
		}
	}

	assertRates := func(t *testing.T, stage string, got *ModelConfiguration, in, out, read, write int64) {
		t.Helper()
		for _, c := range []struct {
			column string
			got    *int64
			want   int64
		}{
			{"price_input", got.PriceInput, in},
			{"price_output", got.PriceOutput, out},
			{"price_cache_read", got.PriceCacheRead, read},
			{"price_cache_write", got.PriceCacheWrite, write},
		} {
			switch {
			case c.got == nil:
				t.Errorf("%s: %s is nil, want %d", stage, c.column, c.want)
			case *c.got != c.want:
				t.Errorf("%s: %s = %d, want %d", stage, c.column, *c.got, c.want)
			}
		}
	}

	mc := priced(10_000, 20_000, 30_000, 40_000)
	if err := repo.Create(ctx, mc); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByAlias(ctx, "priced")
	if err != nil {
		t.Fatalf("get after create: %v", err)
	}
	assertRates(t, "after create", got, 10_000, 20_000, 30_000, 40_000)

	got.PriceInput, got.PriceOutput = ptrTo(int64(50_000)), ptrTo(int64(60_000))
	got.PriceCacheRead, got.PriceCacheWrite = ptrTo(int64(70_000)), ptrTo(int64(80_000))
	if err = repo.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}

	updated, err := repo.GetByAlias(ctx, "priced")
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	assertRates(t, "after update", updated, 50_000, 60_000, 70_000, 80_000)

	updated.PriceInput, updated.PriceOutput = nil, nil
	updated.PriceCacheRead, updated.PriceCacheWrite = nil, nil
	if err = repo.Update(ctx, updated); err != nil {
		t.Fatalf("update to clear: %v", err)
	}

	cleared, err := repo.GetByAlias(ctx, "priced")
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if cleared.PriceInput != nil || cleared.PriceOutput != nil ||
		cleared.PriceCacheRead != nil || cleared.PriceCacheWrite != nil {
		t.Errorf("rates survived a clear: %v / %v / %v / %v",
			cleared.PriceInput, cleared.PriceOutput, cleared.PriceCacheRead, cleared.PriceCacheWrite)
	}
}

func ptrTo[T any](v T) *T { return &v }

func TestModelConfigRepo_GetPageTakesOffsetAndLimit(t *testing.T) {
	repo := NewModelConfigRepo(TestDB(t))
	ctx := t.Context()
	for _, alias := range []string{"a", "b", "c"} {
		mc := &ModelConfiguration{
			Alias: alias, Provider: "openai", ModelName: "gpt-4",
			ModelAuthType: "apikey", ModelAuthConfig: `{"type":"apikey"}`, ModelParams: `{}`,
			CreatedBy: "u", UpdatedBy: "u",
		}
		if err := repo.Create(ctx, mc); err != nil {
			t.Fatalf("create %s: %v", alias, err)
		}
	}

	page, err := repo.GetPage(ctx, 1, 1, "alias", false)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("len(page) = %d, want 1", len(page))
	}
	if page[0].Alias != "b" {
		t.Errorf("alias = %q, want %q", page[0].Alias, "b")
	}

	desc, err := repo.GetPage(ctx, 0, 2, "alias", true)
	if err != nil {
		t.Fatalf("GetPage desc: %v", err)
	}
	if len(desc) != 2 || desc[0].Alias != "c" || desc[1].Alias != "b" {
		t.Errorf("desc page = %v, want [c b]", aliasesOf(desc))
	}

	total, err := repo.CountAll(ctx)
	if err != nil {
		t.Fatalf("CountAll: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
}

func TestModelConfigRepo_GetPageUnknownSortFallsBackToNewestFirst(t *testing.T) {
	repo := NewModelConfigRepo(TestDB(t))
	ctx := t.Context()
	for i, alias := range []string{"first", "second"} {
		mc := &ModelConfiguration{
			Alias: alias, Provider: "openai", ModelName: "gpt-4",
			ModelAuthType: "apikey", ModelAuthConfig: `{"type":"apikey"}`, ModelParams: `{}`,
			CreatedBy: "u", UpdatedBy: "u",
			CreatedTime: int64(1000 + i),
		}
		if err := repo.Create(ctx, mc); err != nil {
			t.Fatalf("create %s: %v", alias, err)
		}
	}

	page, err := repo.GetPage(ctx, 0, 10, "alias; DROP TABLE model_configurations", false)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}
	if got := aliasesOf(page); len(got) != 2 || got[0] != "second" {
		t.Errorf("aliases = %v, want [second first]", got)
	}
}

func aliasesOf(rows []*ModelConfiguration) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Alias
	}
	return out
}

func TestModelConfigRepo_GetPageIsStableAcrossTies(t *testing.T) {
	db := TestDB(t)
	repo := NewModelConfigRepo(db)
	ctx := t.Context()
	for _, alias := range []string{"first", "second"} {
		mc := &ModelConfiguration{
			Alias: alias, Provider: "openai", ModelName: "gpt-4",
			ModelAuthType: "apikey", ModelAuthConfig: `{"type":"apikey"}`, ModelParams: `{}`,
			CreatedBy: "u", UpdatedBy: "u",
		}
		if err := repo.Create(ctx, mc); err != nil {
			t.Fatalf("create %s: %v", alias, err)
		}
	}
	for alias, id := range map[string]string{
		"first":  "00000000-0000-7000-8000-00000000000b",
		"second": "00000000-0000-7000-8000-00000000000a",
	} {
		if _, err := db.ExecContext(ctx,
			`UPDATE model_configurations SET id = ?, created_time = 1000 WHERE alias = ?`, id, alias); err != nil {
			t.Fatalf("force tie for %s: %v", alias, err)
		}
	}

	for _, sortField := range []string{"createdTime", "unknown"} {
		var seen []string
		for offset := range 2 {
			page, err := repo.GetPage(ctx, offset, 1, sortField, true)
			if err != nil {
				t.Fatalf("GetPage(%s, offset %d): %v", sortField, offset, err)
			}
			seen = append(seen, aliasesOf(page)...)
		}
		if len(seen) != 2 || seen[0] != "second" || seen[1] != "first" {
			t.Errorf("sort %q: paging yielded %v, want [second first] (id order within the tie)", sortField, seen)
		}
	}
}
