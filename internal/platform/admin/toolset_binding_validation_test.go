// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestApplyToolsetConfigs_TypeMismatch_Rejects422(t *testing.T) {
	db := testOpenDB(t)
	ctx := context.Background()
	repo := store.NewToolPackageRepo(db)

	schema := `[{"key":"timeout","type":"integer"},{"key":"api_key","type":"string","secret":true}]`
	pkg := &store.ToolPackage{
		Name: "typed_tools", DiscoveryStatus: "ready",
		CreatedBy: "tester", UpdatedBy: "tester",
	}
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}
	if err := repo.UpsertToolFile(ctx, &store.ToolPackageFile{
		PackageID: pkg.ID, ToolName: "do_thing", ConfigSchema: &schema,
	}); err != nil {
		t.Fatalf("upsert tool file: %v", err)
	}

	bindings := []ToolsetConfigBinding{
		{
			ToolsetName: "typed_tools",
			ConfigValues: map[string]any{
				"timeout": "not-a-number",
				"api_key": "sk-real",
			},
		},
	}
	err := applyToolsetConfigs(ctx, "agent-id-1", bindings, []string{"typed_tools"}, nil, repo)
	if err == nil {
		t.Fatal("expected ValidationError for type mismatch, got nil")
	}
	var valErr *ValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if !strings.Contains(valErr.Message, "typed_tools") {
		t.Errorf("error message should mention toolset name; got: %s", valErr.Message)
	}
	if len(valErr.Details) == 0 {
		t.Fatalf("expected per-field Details, got none")
	}
	key := "typed_tools.timeout"
	if _, ok := valErr.Details[key]; !ok {
		t.Errorf("expected Details[%q] entry, got: %v", key, valErr.Details)
	}
}

func TestApplyToolsetConfigs_UnknownField_Rejects422(t *testing.T) {
	db := testOpenDB(t)
	ctx := context.Background()
	repo := store.NewToolPackageRepo(db)

	schema := `[{"key":"api_key","type":"string","secret":true}]`
	pkg := &store.ToolPackage{
		Name: "schema_pkg", DiscoveryStatus: "ready",
		CreatedBy: "tester", UpdatedBy: "tester",
	}
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}
	if err := repo.UpsertToolFile(ctx, &store.ToolPackageFile{
		PackageID: pkg.ID, ToolName: "do_thing", ConfigSchema: &schema,
	}); err != nil {
		t.Fatalf("upsert tool file: %v", err)
	}

	bindings := []ToolsetConfigBinding{
		{
			ToolsetName: "schema_pkg",
			ConfigValues: map[string]any{
				"api_key":  "sk-real",
				"unknown":  "garbage",
				"also_bad": 42,
			},
		},
	}
	err := applyToolsetConfigs(ctx, "agent-id-2", bindings, []string{"schema_pkg"}, nil, repo)
	if err == nil {
		t.Fatal("expected ValidationError for unknown fields, got nil")
	}
	var valErr *ValidationError
	if !errors.As(err, &valErr) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	if _, ok := valErr.Details["schema_pkg.unknown"]; !ok {
		t.Errorf("expected Details[schema_pkg.unknown]; got %v", valErr.Details)
	}
	if _, ok := valErr.Details["schema_pkg.also_bad"]; !ok {
		t.Errorf("expected Details[schema_pkg.also_bad]; got %v", valErr.Details)
	}
}

func TestApplyToolsetConfigs_RedactedSentinel_Allowed(t *testing.T) {
	db := testOpenDB(t)
	ctx := context.Background()
	repo := store.NewToolPackageRepo(db)
	agentRepo := store.NewAgentRepo(db)

	schema := `[{"key":"api_key","type":"string","secret":true}]`
	pkg := &store.ToolPackage{
		Name: "round_trip_pkg", DiscoveryStatus: "ready",
		CreatedBy: "tester", UpdatedBy: "tester",
	}
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}
	if err := repo.UpsertToolFile(ctx, &store.ToolPackageFile{
		PackageID: pkg.ID, ToolName: "do_thing", ConfigSchema: &schema,
	}); err != nil {
		t.Fatalf("upsert tool file: %v", err)
	}

	agent := &store.Agent{
		Description: strPtr("test agent for fixture purposes"),
		Name:        "RT Agent", SystemPrompt: strPtr("You help."),
		CreatedBy: "u", UpdatedBy: "u",
	}
	if err := agentRepo.Create(ctx, agent, []string{"round_trip_pkg"}, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	bindings := []ToolsetConfigBinding{
		{
			ToolsetName: "round_trip_pkg",
			ConfigValues: map[string]any{
				"do_thing": map[string]any{"api_key": "<REDACTED>"},
			},
		},
	}
	if err := applyToolsetConfigs(ctx, agent.ID, bindings, []string{"round_trip_pkg"}, nil, repo); err != nil {
		t.Fatalf("expected REDACTED sentinel to be accepted by type validator; got: %v", err)
	}
}
