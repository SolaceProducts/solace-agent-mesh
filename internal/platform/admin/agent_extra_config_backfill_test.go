// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestBackfillAgentExtraConfigToCamelCase_FlipsSnakeCaseRow(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := t.Context()

	repo := store.NewAgentRepo(db)
	legacy := []byte(`{"supports_streaming":true,"agent_card_publishing":{"interval_seconds":30}}`)
	a := &store.Agent{
		ID:                       "11111111-1111-1111-1111-111111111111",
		Name:                     "legacy-snake",
		Description:              strPtr("desc"),
		SystemPrompt:             strPtr("prompt"),
		Type:                     "standard",
		AdditionalConfigurations: legacy,
		CreatedBy:                "u",
		UpdatedBy:                "u",
	}
	if err := repo.Create(ctx, a, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	if err := backfillAgentExtraConfigToCamelCase(ctx, db, slog.Default()); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	got, _, _, _, _, err := repo.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(got.AdditionalConfigurations, &parsed); err != nil {
		t.Fatalf("unmarshal post-backfill blob: %v", err)
	}
	if v, ok := parsed["supportsStreaming"].(bool); !ok || !v {
		t.Errorf("expected camelCase 'supportsStreaming'=true, got: %v (full=%v)", parsed["supportsStreaming"], parsed)
	}
	if _, present := parsed["supports_streaming"]; present {
		t.Errorf("legacy snake_case 'supports_streaming' must be gone after backfill, got: %v", parsed)
	}
	pub, _ := parsed["agentCardPublishing"].(map[string]any)
	if pub == nil {
		t.Fatalf("expected nested camelCase 'agentCardPublishing' block, got: %v", parsed["agentCardPublishing"])
	}
	if v, _ := pub["intervalSeconds"].(float64); v != 30 {
		t.Errorf("expected intervalSeconds=30 inside flipped block, got: %v", pub)
	}
}

func TestBackfillAgentExtraConfigToCamelCase_Idempotent(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := t.Context()

	repo := store.NewAgentRepo(db)
	legacy := []byte(`{"supports_streaming":true,"agent_card_publishing":{"interval_seconds":15}}`)
	a := &store.Agent{
		ID:                       "22222222-2222-2222-2222-222222222222",
		Name:                     "idempotent",
		Description:              strPtr("desc"),
		SystemPrompt:             strPtr("prompt"),
		Type:                     "standard",
		AdditionalConfigurations: legacy,
		CreatedBy:                "u",
		UpdatedBy:                "u",
	}
	if err := repo.Create(ctx, a, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if backErr := backfillAgentExtraConfigToCamelCase(ctx, db, slog.Default()); backErr != nil {
		t.Fatalf("first backfill: %v", backErr)
	}
	row1, _, _, _, _, err := repo.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("get-1: %v", err)
	}
	if backErr := backfillAgentExtraConfigToCamelCase(ctx, db, slog.Default()); backErr != nil {
		t.Fatalf("second backfill: %v", backErr)
	}
	row2, _, _, _, _, err := repo.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("get-2: %v", err)
	}
	if string(row1.AdditionalConfigurations) != string(row2.AdditionalConfigurations) {
		t.Errorf("idempotency broken: pass-1=%s, pass-2=%s",
			row1.AdditionalConfigurations, row2.AdditionalConfigurations)
	}
}

func TestBackfillAgentExtraConfigToCamelCase_PreservesOpaqueAndUnknown(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := t.Context()

	repo := store.NewAgentRepo(db)
	legacy := []byte(`{
		"supports_streaming":true,
		"agent_card":{"documentationUrl":"https://example.test/docs","defaultInputModes":["text"]},
		"agent_init_function":"py_init",
		"some_experimental_unknown_key":{"opaque":[1,2,3]}
	}`)
	a := &store.Agent{
		ID:                       "33333333-3333-3333-3333-333333333333",
		Name:                     "carve-outs",
		Description:              strPtr("desc"),
		SystemPrompt:             strPtr("prompt"),
		Type:                     "standard",
		AdditionalConfigurations: legacy,
		CreatedBy:                "u",
		UpdatedBy:                "u",
	}
	if err := repo.Create(ctx, a, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := backfillAgentExtraConfigToCamelCase(ctx, db, slog.Default()); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	got, _, _, _, _, err := repo.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("get-by-id: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(got.AdditionalConfigurations, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	card, _ := parsed["agentCard"].(map[string]any)
	if card == nil {
		t.Fatalf("expected camelCase 'agentCard' block; got %v", parsed)
	}
	if got, _ := card["documentationUrl"].(string); got != "https://example.test/docs" {
		t.Errorf("agentCard.documentationUrl lost in carve-out: %v", card)
	}
	if got, _ := parsed["agent_init_function"].(string); got != "py_init" {
		t.Errorf("agent_init_function identity broken: %v", parsed["agent_init_function"])
	}
	if _, present := parsed["some_experimental_unknown_key"]; !present {
		t.Errorf("unknown key was dropped during backfill: %v", parsed)
	}
	if v, _ := parsed["supportsStreaming"].(bool); !v {
		t.Errorf("supportsStreaming missing/false post-backfill: %v", parsed)
	}
}

func TestBackfillAgentExtraConfigToCamelCase_NilDBIsNoOp(t *testing.T) {
	t.Parallel()
	if err := backfillAgentExtraConfigToCamelCase(t.Context(), nil, slog.Default()); err != nil {
		t.Errorf("expected nil error for nil DB, got %v", err)
	}
}

func TestBackfillAgentExtraConfigToCamelCase_NonObjectRowSkipped(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := t.Context()

	repo := store.NewAgentRepo(db)
	a := &store.Agent{
		ID:           "66666666-6666-6666-6666-666666666666",
		Name:         "non-object",
		Description:  strPtr("desc"),
		SystemPrompt: strPtr("prompt"),
		Type:         "standard",
		CreatedBy:    "u",
		UpdatedBy:    "u",
	}
	if err := repo.Create(ctx, a, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	const malformed = `["not","an","object"]`
	if _, err := db.ExecContext(ctx,
		`UPDATE agents SET additional_configurations = ? WHERE id = ?`,
		malformed, a.ID); err != nil {
		t.Fatalf("seed non-object via raw SQL: %v", err)
	}

	if err := backfillAgentExtraConfigToCamelCase(ctx, db, slog.Default()); err != nil {
		t.Fatalf("backfill should not error on non-object row: %v", err)
	}

	var got string
	if err := db.QueryRowContext(ctx,
		`SELECT additional_configurations FROM agents WHERE id = ?`,
		a.ID).Scan(&got); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if !jsonStrEqual(got, malformed) {
		t.Errorf("expected malformed row preserved verbatim, got %q", got)
	}
}

func TestBackfillAgentExtraConfigToCamelCase_AgentCardDescendantsAreNotRewritten(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := t.Context()

	repo := store.NewAgentRepo(db)
	legacy := []byte(`{"agent_card":{"documentation_url":"https://example.test/legacy","documentationUrl":"https://example.test/canonical"}}`)
	a := &store.Agent{
		ID:                       "55555555-5555-5555-5555-555555555555",
		Name:                     "agent-card-descendant",
		Description:              strPtr("desc"),
		SystemPrompt:             strPtr("prompt"),
		Type:                     "standard",
		AdditionalConfigurations: legacy,
		CreatedBy:                "u",
		UpdatedBy:                "u",
	}
	if err := repo.Create(ctx, a, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := backfillAgentExtraConfigToCamelCase(ctx, db, slog.Default()); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	got, _, _, _, _, err := repo.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("get-by-id: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(got.AdditionalConfigurations, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	card, _ := parsed["agentCard"].(map[string]any)
	if card == nil {
		t.Fatalf("expected outer block to flip to camelCase 'agentCard', got: %v", parsed)
	}
	if got, _ := card["documentation_url"].(string); got != "https://example.test/legacy" {
		t.Errorf("expected legacy snake_case 'documentation_url' preserved verbatim under agentCard (identity carve-out for descendants), got: %v", card)
	}
	if got, _ := card["documentationUrl"].(string); got != "https://example.test/canonical" {
		t.Errorf("expected canonical camelCase 'documentationUrl' preserved, got: %v", card)
	}
}

func TestBackfillAgentExtraConfigToCamelCase_EmptyDefaultIsNoOp(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	ctx := t.Context()
	repo := store.NewAgentRepo(db)
	a := &store.Agent{
		ID:           "44444444-4444-4444-4444-444444444444",
		Name:         "empty",
		Description:  strPtr("desc"),
		SystemPrompt: strPtr("prompt"),
		Type:         "standard",
		CreatedBy:    "u",
		UpdatedBy:    "u",
	}
	if err := repo.Create(ctx, a, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := backfillAgentExtraConfigToCamelCase(ctx, db, slog.Default()); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	got, _, _, _, _, err := repo.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("get-by-id: %v", err)
	}
	if got.AdditionalConfigurations != nil && !isEmptyJSONObject(got.AdditionalConfigurations) {
		t.Errorf("expected default empty object preserved, got %s", got.AdditionalConfigurations)
	}
}
