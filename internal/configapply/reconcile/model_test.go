// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/resolve"
	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/wire"
)

func fixtureSpec(f func(map[string]any)) map[string]any {
	m := map[string]any{
		"provider":  "anthropic",
		"modelName": "claude-opus-4-7",
		"apiBase":   "https://api.anthropic.com",
		"authConfig": map[string]any{
			"apiKey": "sk-ant",
		},
		"modelParams": map[string]any{
			"temperature": 0.2,
		},
	}
	if f != nil {
		f(m)
	}
	return m
}

func resolved(name string, spec map[string]any) resolve.ResolvedResource {
	return resolve.ResolvedResource{
		Kind:        "model",
		Name:        name,
		SourceFile:  "models/" + name + ".yaml",
		Spec:        spec,
		Description: "fixture",
	}
}

func TestPlanModels_CreateAndDelete(t *testing.T) {
	desired := []resolve.ResolvedResource{resolved("planning", fixtureSpec(nil))}
	actual := []wire.ModelResponse{{
		ID:        "id-old",
		Alias:     "obsolete",
		Provider:  sp("openai"),
		ModelName: sp("gpt-5"),
	}}

	plan, err := planModels(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("planModels: %v", err)
	}
	if len(plan.Creates) != 1 || plan.Creates[0].Name != "planning" {
		t.Errorf("Creates = %+v", plan.Creates)
	}
	if len(plan.Deletes) != 1 || plan.Deletes[0].ID != "id-old" {
		t.Errorf("Deletes = %+v", plan.Deletes)
	}
	if len(plan.Updates) != 0 || len(plan.Unchanged) != 0 {
		t.Errorf("expected no updates/unchanged, got updates=%d unchanged=%d", len(plan.Updates), len(plan.Unchanged))
	}
}

func TestPlanModels_AdoptPlaceholder(t *testing.T) {
	desired := []resolve.ResolvedResource{resolved("general", fixtureSpec(nil))}
	actual := []wire.ModelResponse{{
		ID:        "ph-general",
		Alias:     "general",
		Provider:  nil,
		ModelName: nil,
	}}

	plan, err := planModels(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("planModels: %v", err)
	}
	if len(plan.Creates) != 0 {
		t.Errorf("expected no creates (placeholder must be adopted), got %+v", plan.Creates)
	}
	if len(plan.Updates) != 1 {
		t.Fatalf("expected 1 update adopting the placeholder, got %+v", plan.Updates)
	}
	if plan.Updates[0].ID != "ph-general" {
		t.Errorf("Update targets ID %q, want the placeholder's %q", plan.Updates[0].ID, "ph-general")
	}
	if len(plan.Deletes) != 0 {
		t.Errorf("expected no deletes, got %+v", plan.Deletes)
	}
}

func TestPlanModels_PrunePreservesPlaceholder(t *testing.T) {
	desired := []resolve.ResolvedResource{resolved("general", fixtureSpec(nil))}
	actual := []wire.ModelResponse{
		{ID: "ph-general", Alias: "general"},
		{ID: "ph-planning", Alias: "planning"},
		{ID: "id-obsolete", Alias: "obsolete", Provider: sp("openai"), ModelName: sp("gpt-5")},
	}

	plan, err := planModels(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("planModels: %v", err)
	}
	if len(plan.Deletes) != 1 || plan.Deletes[0].Name != "obsolete" {
		t.Errorf("Deletes = %+v; want only 'obsolete'", plan.Deletes)
	}
	for _, d := range plan.Deletes {
		if d.Name == "planning" {
			t.Errorf("placeholder 'planning' must not be a delete candidate")
		}
	}
	if len(plan.Updates) != 1 || plan.Updates[0].Name != "general" {
		t.Errorf("expected 1 update adopting 'general', got %+v", plan.Updates)
	}
}

func TestPlanModels_NoOpWhenEqual(t *testing.T) {
	desired := []resolve.ResolvedResource{resolved("planning", fixtureSpec(nil))}

	apiBase := "https://api.anthropic.com"
	desc := "fixture"
	actual := []wire.ModelResponse{{
		ID:        "id-1",
		Alias:     "planning",
		Provider:  sp("anthropic"),
		ModelName: sp("claude-opus-4-7"),
		APIBase:   &apiBase,
		AuthType:  "none",
		AuthConfig: map[string]any{
			"apiKey": "sk-ant",
		},
		ModelParams: map[string]any{
			"temperature": 0.2,
		},
		Description: &desc,
	}}

	plan, err := planModels(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("planModels: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("Unchanged = %+v", plan.Unchanged)
	}
	if len(plan.Creates)+len(plan.Updates)+len(plan.Deletes) != 0 {
		t.Errorf("expected no diff, got creates=%d updates=%d deletes=%d",
			len(plan.Creates), len(plan.Updates), len(plan.Deletes))
	}
}

func TestPlanModels_PaddedModelNameMatchesTrimmedServerValue(t *testing.T) {
	desired := []resolve.ResolvedResource{resolved("planning", fixtureSpec(func(m map[string]any) {
		m["modelName"] = "  claude-opus-4-7 "
	}))}

	apiBase := "https://api.anthropic.com"
	desc := "fixture"
	actual := []wire.ModelResponse{{
		ID:          "id-1",
		Alias:       "planning",
		Provider:    sp("anthropic"),
		ModelName:   sp("claude-opus-4-7"),
		APIBase:     &apiBase,
		AuthType:    "none",
		AuthConfig:  map[string]any{"apiKey": "sk-ant"},
		ModelParams: map[string]any{"temperature": 0.2},
		Description: &desc,
	}}

	plan, err := planModels(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("planModels: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("Unchanged = %+v, want the padded modelName to match the stored trimmed one", plan.Unchanged)
	}
	if len(plan.Creates)+len(plan.Updates)+len(plan.Deletes) != 0 {
		t.Errorf("expected no diff, got creates=%d updates=%d deletes=%d",
			len(plan.Creates), len(plan.Updates), len(plan.Deletes))
	}
}

func TestPlanModels_UpdateOnDifference(t *testing.T) {
	desired := []resolve.ResolvedResource{resolved("planning", fixtureSpec(func(m map[string]any) {
		m["modelParams"] = map[string]any{"temperature": 0.7}
	}))}
	apiBase := "https://api.anthropic.com"
	desc := "fixture"
	actual := []wire.ModelResponse{{
		ID:        "id-1",
		Alias:     "planning",
		Provider:  sp("anthropic"),
		ModelName: sp("claude-opus-4-7"),
		APIBase:   &apiBase,
		AuthType:  "none",
		AuthConfig: map[string]any{
			"apiKey": "sk-ant",
		},
		ModelParams: map[string]any{"temperature": 0.2},
		Description: &desc,
	}}

	plan, err := planModels(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("planModels: %v", err)
	}
	if len(plan.Updates) != 1 || plan.Updates[0].ID != "id-1" {
		t.Errorf("Updates = %+v", plan.Updates)
	}
	op := plan.Updates[0]
	if op.Actual == nil {
		t.Errorf("Update op missing Actual; verbose plan diff would render nothing")
	} else if op.Actual.ID != "id-1" {
		t.Errorf("Update op Actual.ID = %q, want %q", op.Actual.ID, "id-1")
	}
	if op.Create.Alias != "planning" || op.Create.ModelName != "claude-opus-4-7" {
		t.Errorf("Update op missing desired-state fields: Create=%+v", op.Create)
	}
}

func TestPlanModels_DuplicateNameInDesired(t *testing.T) {
	desired := []resolve.ResolvedResource{
		resolved("dup", fixtureSpec(nil)),
		resolved("dup", fixtureSpec(nil)),
	}
	if _, err := planModels(t.Context(), nil, desired, nil); err == nil {
		t.Fatal("expected duplicate-name error")
	}
}

func TestModelRedactionDynamic_NewProvider(t *testing.T) {
	desired := []resolve.ResolvedResource{resolved("planning", fixtureSpec(func(m map[string]any) {
		m["authConfig"] = map[string]any{
			"type":               "hypothetical_new_kind",
			"hypothetical_token": "real-secret-value",
			"region":             "us-east-1",
		}
	}))}

	apiBase := "https://api.anthropic.com"
	desc := "fixture"
	actual := []wire.ModelResponse{{
		ID:        "id-1",
		Alias:     "planning",
		Provider:  sp("anthropic"),
		ModelName: sp("claude-opus-4-7"),
		APIBase:   &apiBase,
		AuthType:  "hypothetical_new_kind",
		AuthConfig: map[string]any{
			"type":               "hypothetical_new_kind",
			"hypothetical_token": "<REDACTED>",
			"region":             "us-east-1",
		},
		ModelParams: map[string]any{"temperature": 0.2},
		Description: &desc,
	}}

	plan, err := planModels(t.Context(), nil, desired, actual)
	if err != nil {
		t.Fatalf("planModels: %v", err)
	}
	if len(plan.Unchanged) != 1 {
		t.Errorf("Unchanged = %+v", plan.Unchanged)
	}
	if got := len(plan.Creates) + len(plan.Updates) + len(plan.Deletes); got != 0 {
		t.Errorf("expected no diff for redacted hypothetical auth type, got creates=%d updates=%d deletes=%d",
			len(plan.Creates), len(plan.Updates), len(plan.Deletes))
	}
}
