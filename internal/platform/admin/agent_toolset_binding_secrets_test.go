// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func seedSecretToolPackage(t *testing.T, db *store.DB, agentName string) (agentID, packageID, toolsetName string) {
	t.Helper()
	packageID, toolsetName = seedSecretPackage(t, db, agentName)
	return seedAgentBoundTo(t, db, agentName, toolsetName), packageID, toolsetName
}

func seedSecretPackage(t *testing.T, db *store.DB, suffix string) (packageID, toolsetName string) {
	t.Helper()
	repo := store.NewToolPackageRepo(db)
	ctx := t.Context()
	toolsetName = "secret-pkg-" + suffix
	pkg := &store.ToolPackage{Name: toolsetName, DiscoveryStatus: "ready", CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(ctx, pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}
	schema := `[{"key":"api_key","type":"string","secret":true,"required":true},{"key":"region","type":"string"}]`
	if err := repo.UpsertToolFile(ctx, &store.ToolPackageFile{PackageID: pkg.ID, ToolName: "fetch", ConfigSchema: &schema}); err != nil {
		t.Fatalf("upsert tool file: %v", err)
	}
	return pkg.ID, toolsetName
}

func seedAgentBoundTo(t *testing.T, db *store.DB, name string, toolsets ...string) string {
	t.Helper()
	agent := &store.Agent{
		Name:         name,
		Description:  strPtr("test agent for fixture purposes"),
		SystemPrompt: strPtr("You help."),
		CreatedBy:    "u",
		UpdatedBy:    "u",
	}
	if err := store.NewAgentRepo(db).Create(t.Context(), agent, toolsets, nil, nil, nil, nil); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return agent.ID
}

func storedConfigValues(t *testing.T, repo *store.ToolPackageRepo, agentID, packageID string) map[string]any {
	t.Helper()
	rows, err := repo.GetToolsetConfigsForAgent(t.Context(), agentID)
	if err != nil {
		t.Fatalf("read stored toolset configs: %v", err)
	}
	for _, r := range rows {
		if r.PackageID != packageID {
			continue
		}
		if r.ConfigValues == nil || *r.ConfigValues == "" {
			return map[string]any{}
		}
		var vals map[string]any
		if err := json.Unmarshal([]byte(*r.ConfigValues), &vals); err != nil {
			t.Fatalf("stored config values are not valid JSON: %v", err)
		}
		return vals
	}
	t.Fatalf("no stored toolset config row for agent %s package %s", agentID, packageID)
	return nil
}

func toolValues(t *testing.T, repo *store.ToolPackageRepo, agentID, packageID, toolName string) map[string]any {
	t.Helper()
	stored := storedConfigValues(t, repo, agentID, packageID)
	vals, ok := stored[toolName].(map[string]any)
	if !ok {
		t.Fatalf("no stored values for tool %q: %+v", toolName, stored)
	}
	return vals
}

func TestApplyToolsetConfigs_RedactedSentinelDoesNotOverwriteSecret(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	agentID, packageID, toolsetName := seedSecretToolPackage(t, db, "SecretRoundTrip")
	ctx := t.Context()

	first := []ToolsetConfigBinding{{
		ToolsetName:  toolsetName,
		ConfigValues: map[string]any{"fetch": map[string]any{"api_key": "s3cret", "region": "us-east-1"}},
	}}
	if err := applyToolsetConfigs(ctx, agentID, first, []string{toolsetName}, nil, repo); err != nil {
		t.Fatalf("initial apply: %v", err)
	}

	roundTrip := []ToolsetConfigBinding{{
		ToolsetName:  toolsetName,
		ConfigValues: map[string]any{"fetch": map[string]any{"api_key": redactedSentinel, "region": "eu-west-1"}},
	}}
	if err := applyToolsetConfigs(ctx, agentID, roundTrip, []string{toolsetName}, nil, repo); err != nil {
		t.Fatalf("round-trip apply: %v", err)
	}

	fetch := toolValues(t, repo, agentID, packageID, "fetch")
	if fetch["api_key"] != "s3cret" {
		t.Errorf("api_key = %v, want the stored secret preserved (never the sentinel)", fetch["api_key"])
	}
	if fetch["region"] != "eu-west-1" {
		t.Errorf("region = %v, want the authored edit applied", fetch["region"])
	}
}

func TestApplyToolsetConfigs_RealSecretStillReplaces(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	agentID, packageID, toolsetName := seedSecretToolPackage(t, db, "SecretRotate")
	ctx := t.Context()

	bind := func(apiKey string) []ToolsetConfigBinding {
		return []ToolsetConfigBinding{{
			ToolsetName:  toolsetName,
			ConfigValues: map[string]any{"fetch": map[string]any{"api_key": apiKey, "region": "us-east-1"}},
		}}
	}
	if err := applyToolsetConfigs(ctx, agentID, bind("old"), []string{toolsetName}, nil, repo); err != nil {
		t.Fatalf("initial apply: %v", err)
	}
	if err := applyToolsetConfigs(ctx, agentID, bind("rotated"), []string{toolsetName}, nil, repo); err != nil {
		t.Fatalf("rotate apply: %v", err)
	}

	fetch := toolValues(t, repo, agentID, packageID, "fetch")
	if fetch["api_key"] != "rotated" {
		t.Errorf("api_key = %v, want the rotated secret", fetch["api_key"])
	}
}

func TestApplyToolsetConfigs_SentinelOnNonSecretFieldIsLiteral(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	agentID, packageID, toolsetName := seedSecretToolPackage(t, db, "SentinelLiteral")
	ctx := t.Context()

	seed := []ToolsetConfigBinding{{
		ToolsetName:  toolsetName,
		ConfigValues: map[string]any{"fetch": map[string]any{"api_key": "k1", "region": "us-east-1"}},
	}}
	if err := applyToolsetConfigs(ctx, agentID, seed, []string{toolsetName}, nil, repo); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	bindings := []ToolsetConfigBinding{{
		ToolsetName:  toolsetName,
		ConfigValues: map[string]any{"fetch": map[string]any{"api_key": "k2", "region": redactedSentinel}},
	}}
	if err := applyToolsetConfigs(ctx, agentID, bindings, []string{toolsetName}, nil, repo); err != nil {
		t.Fatalf("apply: %v", err)
	}

	fetch := toolValues(t, repo, agentID, packageID, "fetch")
	if fetch["region"] != redactedSentinel {
		t.Errorf("region = %v, want the literal string on a non-secret field", fetch["region"])
	}
	if fetch["api_key"] != "k2" {
		t.Errorf("api_key = %v, want the new secret applied", fetch["api_key"])
	}
}

func TestLoadToolsetConfigBindings_StillRedactsSecrets(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	agentID, _, toolsetName := seedSecretToolPackage(t, db, "RedactOnRead")
	ctx := t.Context()

	bindings := []ToolsetConfigBinding{{
		ToolsetName:  toolsetName,
		ConfigValues: map[string]any{"fetch": map[string]any{"api_key": "s3cret", "region": "us-east-1"}},
	}}
	if err := applyToolsetConfigs(ctx, agentID, bindings, []string{toolsetName}, nil, repo); err != nil {
		t.Fatalf("apply: %v", err)
	}

	loaded, err := loadToolsetConfigBindings(ctx, agentID, repo)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("want one binding, got %+v", loaded)
	}
	fetch, ok := loaded[0].ConfigValues["fetch"].(map[string]any)
	if !ok {
		t.Fatalf("no values for tool 'fetch': %+v", loaded[0].ConfigValues)
	}
	if fetch["api_key"] != redactedSentinel {
		t.Errorf("api_key = %v, want it redacted on read", fetch["api_key"])
	}
	if fetch["region"] != "us-east-1" {
		t.Errorf("region = %v, want the non-secret value passed through", fetch["region"])
	}
}

func TestApplyToolsetConfigs_AuthBlockSecretSurvives(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	agentID, packageID, toolsetName := seedSecretToolPackage(t, db, "AuthRoundTrip")
	ctx := t.Context()

	authBlock := func(secret string) map[string]any {
		return map[string]any{
			"type":       "oauth2",
			"credential": map[string]any{"client_id": "cid", "client_secret": secret},
		}
	}

	seed := []ToolsetConfigBinding{{
		ToolsetName:  toolsetName,
		ConfigValues: map[string]any{"auth": authBlock("realClientSecret")},
	}}
	if err := applyToolsetConfigs(ctx, agentID, seed, []string{toolsetName}, nil, repo); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	roundTrip := []ToolsetConfigBinding{{
		ToolsetName:  toolsetName,
		ConfigValues: map[string]any{"auth": authBlock(redactedSentinel)},
	}}
	if err := applyToolsetConfigs(ctx, agentID, roundTrip, []string{toolsetName}, nil, repo); err != nil {
		t.Fatalf("round-trip apply: %v", err)
	}

	stored := storedConfigValues(t, repo, agentID, packageID)
	auth, ok := stored["auth"].(map[string]any)
	if !ok {
		t.Fatalf("no stored auth block: %+v", stored)
	}
	cred, ok := auth["credential"].(map[string]any)
	if !ok {
		t.Fatalf("no credential in stored auth block: %+v", auth)
	}
	if cred["client_secret"] != "realClientSecret" {
		t.Errorf("client_secret = %v, want the stored secret preserved", cred["client_secret"])
	}
	if cred["client_id"] != "cid" {
		t.Errorf("client_id = %v, want the non-secret field intact", cred["client_id"])
	}
}

func TestApplyToolsetConfigs_SentinelWithNoPriorIsNotStored(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	agentID, packageID, toolsetName := seedSecretToolPackage(t, db, "SentinelNoPrior")
	ctx := t.Context()

	bindings := []ToolsetConfigBinding{{
		ToolsetName:  toolsetName,
		ConfigValues: map[string]any{"fetch": map[string]any{"api_key": redactedSentinel, "region": "us-east-1"}},
	}}
	if err := applyToolsetConfigs(ctx, agentID, bindings, []string{toolsetName}, nil, repo); err != nil {
		t.Fatalf("apply: %v", err)
	}

	fetch := toolValues(t, repo, agentID, packageID, "fetch")
	if got, present := fetch["api_key"]; present {
		t.Errorf("api_key = %v (present=%v), want the unresolvable sentinel dropped, never stored", got, present)
	}
	if fetch["region"] != "us-east-1" {
		t.Errorf("region = %v, want the non-secret value stored", fetch["region"])
	}
}

func TestApplyToolsetConfigs_UnchangedRoundTripFromRead(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	agentID, packageID, toolsetName := seedSecretToolPackage(t, db, "UnchangedRoundTrip")
	ctx := t.Context()

	seed := []ToolsetConfigBinding{{
		ToolsetName: toolsetName,
		ConfigValues: map[string]any{
			"fetch": map[string]any{"api_key": "s3cret", "region": "us-east-1"},
			"auth":  map[string]any{"type": "oauth2", "credential": map[string]any{"client_id": "cid", "client_secret": "realClientSecret"}},
		},
	}}
	if err := applyToolsetConfigs(ctx, agentID, seed, []string{toolsetName}, nil, repo); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	readBack, err := loadToolsetConfigBindings(ctx, agentID, repo)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(readBack) != 1 {
		t.Fatalf("want one binding from the read path, got %+v", readBack)
	}
	if err := applyToolsetConfigs(ctx, agentID, readBack, []string{toolsetName}, nil, repo); err != nil {
		t.Fatalf("round-trip apply: %v", err)
	}

	fetch := toolValues(t, repo, agentID, packageID, "fetch")
	if fetch["api_key"] != "s3cret" {
		t.Errorf("api_key = %v, want it preserved through an unchanged round trip", fetch["api_key"])
	}
	stored := storedConfigValues(t, repo, agentID, packageID)
	auth, _ := stored["auth"].(map[string]any)
	cred, _ := auth["credential"].(map[string]any)
	if cred["client_secret"] != "realClientSecret" {
		t.Errorf("auth client_secret = %v, want it preserved through an unchanged round trip", cred["client_secret"])
	}
}

func TestApplyToolsetConfigs_TwoPackagesResolveIndependently(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	packageA, toolsetA := seedSecretPackage(t, db, "TwoPkgA")
	packageB, toolsetB := seedSecretPackage(t, db, "TwoPkgB")
	agentID := seedAgentBoundTo(t, db, "TwoPackageAgent", toolsetA, toolsetB)
	ctx := t.Context()

	bind := func(name, key string) ToolsetConfigBinding {
		return ToolsetConfigBinding{
			ToolsetName:  name,
			ConfigValues: map[string]any{"fetch": map[string]any{"api_key": key, "region": "us-east-1"}},
		}
	}
	bound := []string{toolsetA, toolsetB}
	if err := applyToolsetConfigs(ctx, agentID, []ToolsetConfigBinding{bind(toolsetA, "secret-A"), bind(toolsetB, "secret-B")}, bound, nil, repo); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	if err := applyToolsetConfigs(ctx, agentID, []ToolsetConfigBinding{bind(toolsetA, redactedSentinel), bind(toolsetB, redactedSentinel)}, bound, nil, repo); err != nil {
		t.Fatalf("round-trip apply: %v", err)
	}

	if got := toolValues(t, repo, agentID, packageA, "fetch")["api_key"]; got != "secret-A" {
		t.Errorf("package A api_key = %v, want secret-A", got)
	}
	if got := toolValues(t, repo, agentID, packageB, "fetch")["api_key"]; got != "secret-B" {
		t.Errorf("package B api_key = %v, want secret-B", got)
	}
}

func TestValidateToolsetConfigBindings_RejectsNonStringSecretBeforeWrite(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	agentID, _, toolsetName := seedSecretToolPackage(t, db, "NonStringSecret")
	ctx := t.Context()
	_ = agentID

	bindings := []ToolsetConfigBinding{{
		ToolsetName:  toolsetName,
		ConfigValues: map[string]any{"fetch": map[string]any{"api_key": 1234}},
	}}
	err := validateToolsetConfigBindings(ctx, bindings, []string{toolsetName}, nil, repo)
	if err == nil {
		t.Fatal("expected a pre-write rejection for a non-string secret value")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError so the API answers 422, got %T", err)
	}
}
