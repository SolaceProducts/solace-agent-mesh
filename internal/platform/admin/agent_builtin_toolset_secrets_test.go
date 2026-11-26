// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

const (
	datadogToolset  = "observability_datadog"
	researchToolset = "builtin_research_tools"
	webToolset      = "builtin_web_request_tools"
)

func storedBuiltinToolValues(t *testing.T, repo *store.ToolPackageRepo, agentID, toolsetName, toolName string) map[string]any {
	t.Helper()
	prior, err := priorBuiltinToolsetConfigValues(t.Context(), agentID, repo)
	if err != nil {
		t.Fatalf("read stored builtin configs: %v", err)
	}
	vals, ok := prior[toolsetName][toolName].(map[string]any)
	if !ok {
		t.Fatalf("no stored values for %s/%s: %+v", toolsetName, toolName, prior)
	}
	return vals
}

func applyBuiltin(t *testing.T, repo *store.ToolPackageRepo, agentID string, toolsets []string, bindings ...ToolsetConfigBinding) {
	t.Helper()
	if err := applyToolsetConfigs(t.Context(), agentID, bindings, toolsets, nil, repo); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

func datadogBinding(apiKey, appKey string) ToolsetConfigBinding {
	return ToolsetConfigBinding{
		ToolsetName: datadogToolset,
		ConfigValues: map[string]any{"datadog_query_logs": map[string]any{
			"api_key": apiKey, "app_key": appKey, "site": "datadoghq.eu",
		}},
	}
}

func TestBuiltinToolsetSecrets_RedactedOnSingleAndBatchRead(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	agentID := seedAgentBoundTo(t, db, "DatadogRedact", datadogToolset)
	applyBuiltin(t, repo, agentID, []string{datadogToolset}, datadogBinding("dd-api-alice", "dd-app-alice"))

	single, err := loadToolsetConfigBindings(t.Context(), agentID, repo)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	batch := batchLoadToolsetConfigBindings(t.Context(), []string{agentID}, repo)[agentID]
	for name, loaded := range map[string][]ToolsetConfigBinding{"single": single, "batch": batch} {
		if len(loaded) != 1 {
			t.Fatalf("%s read = %+v, want one binding", name, loaded)
		}
		logs, _ := loaded[0].ConfigValues["datadog_query_logs"].(map[string]any)
		if logs["api_key"] != redactedSentinel || logs["app_key"] != redactedSentinel {
			t.Errorf("%s read = %+v, want both Datadog keys redacted", name, logs)
		}
		if logs["site"] != "datadoghq.eu" {
			t.Errorf("%s read site = %v, want the plain setting passed through", name, logs["site"])
		}
	}

	stored := storedBuiltinToolValues(t, repo, agentID, datadogToolset, "datadog_query_logs")
	if stored["api_key"] != "dd-api-alice" || stored["app_key"] != "dd-app-alice" {
		t.Errorf("stored = %+v, want the real keys kept for deploy", stored)
	}
}

func TestBuiltinToolsetSecrets_MergeOnWrite(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		second     ToolsetConfigBinding
		wantAPIKey any
		wantAppKey any
	}{
		{"sentinel keeps the stored key", datadogBinding(redactedSentinel, redactedSentinel), "dd-api-alice", "dd-app-alice"},
		{"a new value rotates it", datadogBinding("dd-api-bob", redactedSentinel), "dd-api-bob", "dd-app-alice"},
		{"an empty string keeps the stored key, as for a package secret with no default", datadogBinding("", redactedSentinel), "dd-api-alice", "dd-app-alice"},
		{"null keeps the stored key", ToolsetConfigBinding{
			ToolsetName:  datadogToolset,
			ConfigValues: map[string]any{"datadog_query_logs": map[string]any{"api_key": nil, "site": "datadoghq.eu"}},
		}, "dd-api-alice", "dd-app-alice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := testOpenDB(t)
			repo := store.NewToolPackageRepo(db)
			agentID := seedAgentBoundTo(t, db, "DatadogMerge", datadogToolset)
			applyBuiltin(t, repo, agentID, []string{datadogToolset}, datadogBinding("dd-api-alice", "dd-app-alice"))
			applyBuiltin(t, repo, agentID, []string{datadogToolset}, tc.second)

			stored := storedBuiltinToolValues(t, repo, agentID, datadogToolset, "datadog_query_logs")
			if stored["api_key"] != tc.wantAPIKey || stored["app_key"] != tc.wantAppKey {
				t.Errorf("stored = %+v, want api_key %v and app_key %v", stored, tc.wantAPIKey, tc.wantAppKey)
			}
		})
	}
}

func TestBuiltinToolsetSecrets_PlaceholderWithNothingStoredIsKept(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	agentID := seedAgentBoundTo(t, db, "PlaceholderOwed", datadogToolset)
	applyBuiltin(t, repo, agentID, []string{datadogToolset}, datadogBinding("<<__SAM_REQUIRED__>>", "dd-app-alice"))

	if got := storedBuiltinToolValues(t, repo, agentID, datadogToolset, "datadog_query_logs")["api_key"]; got != "<<__SAM_REQUIRED__>>" {
		t.Errorf("stored api_key = %v, want the placeholder kept to mark the credential owed", got)
	}
}

func TestBuiltinToolsetSecrets_FirstWriteEmptyKeyReadsBackRedacted(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	agentID := seedAgentBoundTo(t, db, "EmptyFirstWrite", datadogToolset)
	applyBuiltin(t, repo, agentID, []string{datadogToolset}, datadogBinding("", "dd-app-alice"))

	loaded, err := loadToolsetConfigBindings(t.Context(), agentID, repo)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	logs, _ := loaded[0].ConfigValues["datadog_query_logs"].(map[string]any)
	if got := logs["api_key"]; got != redactedSentinel {
		t.Errorf("read api_key = %v, want %q, as redactSecretFields reads any stored package secret", got, redactedSentinel)
	}
}

func TestBuiltinToolsetSecrets_UnresolvedSentinelIsNeverStored(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	agentID := seedAgentBoundTo(t, db, "SentinelFirstWrite", datadogToolset)
	applyBuiltin(t, repo, agentID, []string{datadogToolset}, datadogBinding(redactedSentinel, "dd-app-alice"))

	stored := storedBuiltinToolValues(t, repo, agentID, datadogToolset, "datadog_query_logs")
	if _, present := stored["api_key"]; present {
		t.Errorf("stored = %+v, want the unresolvable sentinel dropped", stored)
	}
}

func TestBuiltinToolsetSecrets_SentinelResolvesAfterToolMovesToolset(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	agentID := seedAgentBoundTo(t, db, "SearchMoves", researchToolset, webToolset)
	search := func(toolset, key string) ToolsetConfigBinding {
		return ToolsetConfigBinding{
			ToolsetName:  toolset,
			ConfigValues: map[string]any{"web_search_google": map[string]any{"google_search_api_key": key, "google_cse_id": "cse-alice"}},
		}
	}
	applyBuiltin(t, repo, agentID, []string{researchToolset}, search(researchToolset, "AIza-alice"))
	applyBuiltin(t, repo, agentID, []string{webToolset}, search(webToolset, redactedSentinel))

	stored := storedBuiltinToolValues(t, repo, agentID, webToolset, "web_search_google")
	if stored["google_search_api_key"] != "AIza-alice" {
		t.Errorf("stored = %+v, want the key carried over from the research toolset", stored)
	}
}

func TestBuiltinToolsetSecrets_Validation(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	bind := func(toolset, tool string, vals map[string]any) ToolsetConfigBinding {
		return ToolsetConfigBinding{ToolsetName: toolset, ConfigValues: map[string]any{tool: vals}}
	}

	accepted := []ToolsetConfigBinding{
		bind(researchToolset, "deep_research", map[string]any{"google_search_api_key": "AIza-alice", "max_iterations": 3, "sources": []any{"web"}}),
		bind(researchToolset, "web_search_google", map[string]any{"google_search_api_key": redactedSentinel, "undeclared_setting": true}),
		bind(researchToolset, "web_search_google", map[string]any{"google_cse_id": 7}),
	}
	for _, b := range accepted {
		if err := validateToolsetConfigBindings(t.Context(), []ToolsetConfigBinding{b}, []string{b.ToolsetName}, nil, repo); err != nil {
			t.Errorf("binding %+v refused: %v", b.ConfigValues, err)
		}
	}

	twoBadSecrets := bind(datadogToolset, "datadog_get_errors", map[string]any{"api_key": 42, "app_key": 43})
	wantPaths := []string{
		"observability_datadog.datadog_get_errors.api_key",
		"observability_datadog.datadog_get_errors.app_key",
	}
	checks := map[string]error{
		"write path": validateToolsetConfigBindings(t.Context(), []ToolsetConfigBinding{twoBadSecrets}, []string{datadogToolset}, nil, repo),
		"dry run":    ValidateBuiltinToolsetConfigBindings([]ToolsetConfigBinding{twoBadSecrets}, nil),
	}
	for name, err := range checks {
		var ve *ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("%s: %v, want a ValidationError", name, err)
			continue
		}
		for _, path := range wantPaths {
			if _, ok := ve.Details[path]; !ok {
				t.Errorf("%s: details = %v, want an entry at %q", name, ve.Details, path)
			}
		}
		if want := `toolset "observability_datadog": secret field "api_key" must be a string, got int`; !strings.Contains(ve.Message, want) {
			t.Errorf("%s: message = %q, want the package secret error shape %q", name, ve.Message, want)
		}
	}
}

func TestBuiltinToolConfigSchemas_EverySecretReadsBackRedacted(t *testing.T) {
	t.Parallel()
	for _, toolName := range slices.Sorted(maps.Keys(builtinToolConfigSchemas)) {
		t.Run(toolName, func(t *testing.T) {
			t.Parallel()
			toolset := builtinToolsetRegistering(t, toolName)
			db := testOpenDB(t)
			repo := store.NewToolPackageRepo(db)
			agentID := seedAgentBoundTo(t, db, "Redact_"+toolName, toolset)
			written := map[string]any{}
			for _, f := range builtinToolConfigSchemas[toolName] {
				written[f.Key] = "value-of-" + f.Key
			}
			applyBuiltin(t, repo, agentID, []string{toolset}, ToolsetConfigBinding{ToolsetName: toolset, ConfigValues: map[string]any{toolName: written}})
			svc := NewAgentService(store.NewAgentRepo(db), nil, nil, repo, nil, nil, slog.New(slog.DiscardHandler))

			single, err := svc.GetByID(t.Context(), agentID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			list, err := svc.ListPaginated(t.Context(), 1, 100, "")
			if err != nil {
				t.Fatalf("ListPaginated: %v", err)
			}
			reads := map[string][]ToolsetConfigBinding{"single": single.ToolsetConfigs}
			for _, a := range list.Data {
				if a.ID == agentID {
					reads["list"] = a.ToolsetConfigs
				}
			}
			if len(reads) != 2 {
				t.Fatalf("agent %s missing from the list read", agentID)
			}
			for path, bindings := range reads {
				if len(bindings) != 1 {
					t.Fatalf("%s read = %+v, want one binding", path, bindings)
				}
				read, _ := bindings[0].ConfigValues[toolName].(map[string]any)
				for _, f := range builtinToolConfigSchemas[toolName] {
					want := written[f.Key]
					if f.Secret {
						want = redactedSentinel
					}
					if read[f.Key] != want {
						t.Errorf("%s read %s = %v, want %v", path, f.Key, read[f.Key], want)
					}
				}
			}
		})
	}
}

func builtinToolsetRegistering(t *testing.T, toolName string) string {
	t.Helper()
	for _, toolset := range slices.Sorted(maps.Keys(builtinToolsetTools)) {
		if slices.Contains(builtinToolsetTools[toolset], toolName) {
			return toolset
		}
	}
	t.Fatalf("no built-in toolset registers %q", toolName)
	return ""
}

func TestBuiltinToolsetSecrets_SentinelPrefersItsOwnToolset(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	agentID := seedAgentBoundTo(t, db, "SearchBothToolsets", researchToolset, webToolset)
	search := func(toolset, key string) ToolsetConfigBinding {
		return ToolsetConfigBinding{
			ToolsetName:  toolset,
			ConfigValues: map[string]any{"web_search_google": map[string]any{"google_search_api_key": key, "google_cse_id": "cse-alice"}},
		}
	}
	both := []string{researchToolset, webToolset}
	applyBuiltin(t, repo, agentID, both, search(researchToolset, "AIza-research"), search(webToolset, "AIza-web"))
	applyBuiltin(t, repo, agentID, both, search(researchToolset, redactedSentinel), search(webToolset, redactedSentinel))

	for toolset, want := range map[string]string{researchToolset: "AIza-research", webToolset: "AIza-web"} {
		stored := storedBuiltinToolValues(t, repo, agentID, toolset, "web_search_google")
		if stored["google_search_api_key"] != want {
			t.Errorf("%s key = %v, want %q kept rather than the other toolset's", toolset, stored["google_search_api_key"], want)
		}
	}
}

func TestBuiltinToolsetSecrets_OverlaySecretsDiff(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	agentID := seedAgentBoundTo(t, db, "DatadogDiff", datadogToolset)
	applyBuiltin(t, repo, agentID, []string{datadogToolset}, datadogBinding("dd-api-alice", "dd-app-alice"))
	svc := NewAgentService(store.NewAgentRepo(db), nil, nil, repo, nil, nil, slog.New(slog.DiscardHandler))

	res, err := svc.GetOverlaySecretsDiff(t.Context(), agentID, AgentOverlayCandidate{
		ToolsetConfigs: []ToolsetConfigBinding{datadogBinding("dd-api-rotated", redactedSentinel)},
	})
	if err != nil {
		t.Fatalf("GetOverlaySecretsDiff: %v", err)
	}
	if got, ok := res[datadogToolset+"/datadog_query_logs.api_key"]; !ok || got.Matches {
		t.Errorf("api_key = %+v (present %v), want a rotation reported", got, ok)
	}
	if got, ok := res[datadogToolset+"/datadog_query_logs.app_key"]; !ok || !got.Matches {
		t.Errorf("app_key = %+v (present %v), want the sentinel to match", got, ok)
	}
	if _, ok := res[datadogToolset+"/datadog_query_logs.site"]; ok {
		t.Errorf("non-secret site reported: %+v", res)
	}
}

func TestBuiltinToolsetSecrets_OverlaySecretsDiffIgnoresAPackageToolNamedLikeABuiltin(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	pkg := &store.ToolPackage{Name: "my-dd-pkg", DiscoveryStatus: "ready", CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(t.Context(), pkg); err != nil {
		t.Fatalf("create package: %v", err)
	}
	schema := `[{"key":"api_key","type":"string","secret":true,"required":true},{"key":"site","type":"string"}]`
	if err := repo.UpsertToolFile(t.Context(), &store.ToolPackageFile{PackageID: pkg.ID, ToolName: "datadog_query_logs", ConfigSchema: &schema}); err != nil {
		t.Fatalf("upsert tool file: %v", err)
	}
	agentID := seedAgentBoundTo(t, db, "PackageNamedLikeBuiltin", pkg.Name, datadogToolset)
	pkgBinding := ToolsetConfigBinding{
		ToolsetName:  pkg.Name,
		ConfigValues: map[string]any{"datadog_query_logs": map[string]any{"api_key": "PKGKEY", "site": "datadoghq.com"}},
	}
	applyBuiltin(t, repo, agentID, []string{pkg.Name, datadogToolset}, pkgBinding, datadogBinding("BUILTINKEY", "BUILTINAPP"))
	svc := NewAgentService(store.NewAgentRepo(db), nil, nil, repo, nil, nil, slog.New(slog.DiscardHandler))

	res, err := svc.GetOverlaySecretsDiff(t.Context(), agentID, AgentOverlayCandidate{ToolsetConfigs: []ToolsetConfigBinding{pkgBinding}})
	if err != nil {
		t.Fatalf("GetOverlaySecretsDiff: %v", err)
	}
	if got := res[pkg.Name+"/datadog_query_logs.api_key"]; !got.Matches {
		t.Errorf("package api_key = %+v, want the unchanged package secret to match, not the built-in row", got)
	}
	if _, phantom := res[pkg.Name+"/datadog_query_logs.app_key"]; phantom {
		t.Errorf("diff = %+v, want no entry for a field the package does not declare", res)
	}
}

func TestBuiltinToolsetSecrets_OverlaySecretsDiffMatchesWhatTheWriteDoes(t *testing.T) {
	t.Parallel()
	const placeholder = "<<__SAM_REQUIRED__>>"
	search := func(toolset string, vals map[string]any) ToolsetConfigBinding {
		return ToolsetConfigBinding{ToolsetName: toolset, ConfigValues: map[string]any{"web_search_google": vals}}
	}
	key := func(toolset string, v any) ToolsetConfigBinding {
		return search(toolset, map[string]any{"google_search_api_key": v, "google_cse_id": "cse"})
	}
	cases := []struct {
		name       string
		storedKey  string
		storedIn   string
		candidate  ToolsetConfigBinding
		wantChange bool
	}{
		{"placeholder, key stored under another toolset", "AIza-alice", researchToolset, key(webToolset, placeholder), true},
		{"sentinel, key stored under another toolset", "AIza-alice", researchToolset, key(webToolset, redactedSentinel), false},
		{"same literal, key stored under another toolset", "AIza-alice", researchToolset, key(webToolset, "AIza-alice"), false},
		{"different literal, key stored under another toolset", "AIza-alice", researchToolset, key(webToolset, "AIza-bob"), true},
		{"empty string, key stored under another toolset", "AIza-alice", researchToolset, key(webToolset, ""), false},
		{"placeholder, same toolset", "AIza-alice", researchToolset, key(researchToolset, placeholder), true},
		{"different literal, same toolset", "AIza-alice", researchToolset, key(researchToolset, "AIza-bob"), true},
		{"empty string, same toolset", "AIza-alice", researchToolset, key(researchToolset, ""), false},
		{"null, same toolset", "AIza-alice", researchToolset, key(researchToolset, nil), false},
		{"omitted, same toolset", "AIza-alice", researchToolset, search(researchToolset, map[string]any{"google_cse_id": "cse"}), false},
		{"sentinel over a stored empty string", "", researchToolset, key(researchToolset, redactedSentinel), false},
		{"empty string over a stored empty string", "", researchToolset, key(researchToolset, ""), false},
		{"literal over a stored empty string", "", researchToolset, key(researchToolset, "AIza-bob"), true},
		{"placeholder over a stored empty string", "", researchToolset, key(researchToolset, placeholder), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := testOpenDB(t)
			repo := store.NewToolPackageRepo(db)
			agentID := seedAgentBoundTo(t, db, "DiffAgreement", researchToolset, webToolset)
			applyBuiltin(t, repo, agentID, []string{tc.storedIn}, key(tc.storedIn, tc.storedKey))
			svc := NewAgentService(store.NewAgentRepo(db), nil, nil, repo, nil, nil, slog.New(slog.DiscardHandler))

			res, err := svc.GetOverlaySecretsDiff(t.Context(), agentID, AgentOverlayCandidate{ToolsetConfigs: []ToolsetConfigBinding{tc.candidate}})
			if err != nil {
				t.Fatalf("GetOverlaySecretsDiff: %v", err)
			}
			got, present := res[tc.candidate.ToolsetName+"/web_search_google.google_search_api_key"]
			if !present {
				t.Fatalf("diff = %+v, want an entry for the key", res)
			}
			if got.Matches == tc.wantChange {
				t.Errorf("matches = %v, want %v", got.Matches, !tc.wantChange)
			}

			applyBuiltin(t, repo, agentID, []string{tc.candidate.ToolsetName}, tc.candidate)
			stored := storedBuiltinToolValues(t, repo, agentID, tc.candidate.ToolsetName, "web_search_google")
			if changed := stored["google_search_api_key"] != tc.storedKey; changed != tc.wantChange {
				t.Errorf("write changed the key = %v (stored %v), but the diff said %v", changed, stored["google_search_api_key"], tc.wantChange)
			}
		})
	}
}

func TestBuiltinToolsetSecrets_OverlaySecretsDiffSkipsAToolWithNothingStored(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	repo := store.NewToolPackageRepo(db)
	agentID := seedAgentBoundTo(t, db, "DiffNothingStored", datadogToolset)
	svc := NewAgentService(store.NewAgentRepo(db), nil, nil, repo, nil, nil, slog.New(slog.DiscardHandler))

	res, err := svc.GetOverlaySecretsDiff(t.Context(), agentID, AgentOverlayCandidate{
		ToolsetConfigs: []ToolsetConfigBinding{datadogBinding("dd-api-alice", "dd-app-alice")},
	})
	if err != nil {
		t.Fatalf("GetOverlaySecretsDiff: %v", err)
	}
	if len(res) != 0 {
		t.Errorf("diff = %+v, want no entries with nothing stored", res)
	}
}

func TestBuiltinToolConfigSchemas_NameToolsAToolsetRegisters(t *testing.T) {
	registered := map[string]bool{}
	for _, tools := range builtinToolsetTools {
		for _, tool := range tools {
			registered[tool] = true
		}
	}
	for tool, fields := range builtinToolConfigSchemas {
		if !registered[tool] {
			t.Errorf("builtinToolConfigSchemas[%q]: no built-in toolset registers this tool, so its secrets can never be bound or redacted", tool)
		}
		seen := map[string]bool{}
		for _, f := range fields {
			if seen[f.Key] {
				t.Errorf("builtinToolConfigSchemas[%q]: key %q declared twice", tool, f.Key)
			}
			seen[f.Key] = true
		}
	}
}
