// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

const builtinResearchToolset = "builtin_research_tools"

type builtinSecretsAPI struct {
	*entrypointAPI
	awe      *fakeAWE
	platform *entplatform.ManageablePlatform
}

func newBuiltinSecretsAPI(t *testing.T) *builtinSecretsAPI {
	t.Helper()
	core, deps := newTestCore(t)
	api := &entrypointAPI{apiClient: newAPIClient(t, core)}
	awe := newFakeAWE(api)
	startControlApp(t, core, "awe", awe.handle)
	return &builtinSecretsAPI{
		entrypointAPI: api,
		awe:           awe,
		platform:      entplatform.NewManageablePlatform(core, deps, slog.Default()),
	}
}

func (a *builtinSecretsAPI) callControl(path ...string) string {
	a.t.Helper()
	resp, err := a.platform.HandleManagement(a.t.Context(), "get", path, nil, nil)
	if err != nil {
		a.t.Fatalf("control get %v: %v", path, err)
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		a.t.Fatalf("marshal control response: %v", err)
	}
	return string(raw)
}

func researchAgentBody(searchConfig map[string]any) map[string]any {
	return map[string]any{
		"name":         "research-agent",
		"description":  "Agent with a configured Google search.",
		"systemPrompt": "You research things.",
		"toolsets":     []string{builtinResearchToolset},
		"toolsetConfigs": []map[string]any{{
			"toolsetName":  builtinResearchToolset,
			"configValues": map[string]any{"web_search_google": searchConfig},
		}},
	}
}

func TestAgents_BuiltinToolsetSecretsAreRedactedOnEveryReadAndKeptOnWrite(t *testing.T) {
	const apiKey = "AIza-alice-search-key"
	api := newBuiltinSecretsAPI(t)

	rec := api.call(http.MethodPost, "/agents", researchAgentBody(map[string]any{
		"google_search_api_key": apiKey, "google_cse_id": "cse-alice", "api_base": "https://search.example.com",
	}))
	created := expectCreated[Agent](t, rec, "agents")
	if strings.Contains(rec.Body.String(), apiKey) {
		t.Errorf("create returned the secret: %s", rec.Body.String())
	}

	reads := map[string]string{
		"v2 GET":                api.call(http.MethodGet, "/agents/"+created.ID, nil).Body.String(),
		"v2 list":               api.call(http.MethodGet, "/agents", nil).Body.String(),
		"control-protocol GET":  api.callControl("agents", created.ID),
		"control-protocol list": api.callControl("agents"),
	}
	for path, body := range reads {
		if strings.Contains(body, apiKey) {
			t.Errorf("%s returned the secret: %s", path, body)
		}
		if !strings.Contains(body, "REDACTED") || !strings.Contains(body, "cse-alice") {
			t.Errorf("%s = %s, want the key redacted and the CSE id kept", path, body)
		}
	}

	read := decodeData[Agent](t, api.call(http.MethodGet, "/agents/"+created.ID, nil))
	search, _ := read.ToolsetConfigs[0].ConfigValues["web_search_google"].(map[string]any)
	search["google_cse_id"] = "cse-bob"
	expectStatus(t, api.call(http.MethodPut, "/agents/"+created.ID, researchAgentBody(search)), http.StatusOK)

	expectCreated[AgentDeployment](t, deployAgent(api.entrypointAPI, created.ID), "agentDeployments")
	if sent := api.awe.lastAddBody(); !strings.Contains(sent, apiKey) || !strings.Contains(sent, "cse-bob") {
		t.Errorf("deploy after a PUT of the redacted value sent %s, want the stored key and the edited CSE id", sent)
	}

	diffPath := "/agents/" + created.ID + "/secretsDiff"
	diffFor := func(key string) bool {
		t.Helper()
		values := researchAgentBody(map[string]any{"google_search_api_key": key})["toolsetConfigs"]
		diff := api.call(http.MethodPut, diffPath, map[string]any{"values": map[string]any{"toolsetConfigs": values}})
		expectStatus(t, diff, http.StatusOK)
		if strings.Contains(diff.Body.String(), apiKey) {
			t.Error("secretsDiff returned the secret")
		}
		result, ok := decodeData[SecretsDiffResult](t, diff).Secrets[builtinResearchToolset+"/web_search_google.google_search_api_key"]
		if !ok {
			t.Fatalf("secretsDiff = %s, want the built-in key reported", diff.Body.String())
		}
		return result.Matches
	}
	if !diffFor(apiKey) {
		t.Error("secretsDiff reports the stored key as changed")
	}
	if diffFor("AIza-rotated-key") {
		t.Error("secretsDiff misses a rotated built-in key")
	}

	expectStatus(t, api.call(http.MethodPut, "/agents/"+created.ID, researchAgentBody(map[string]any{
		"google_search_api_key": "", "google_cse_id": "cse-bob",
	})), http.StatusOK)
	expectCreated[AgentDeployment](t, deployAgent(api.entrypointAPI, created.ID), "agentDeployments")
	if sent := api.awe.lastAddBody(); !strings.Contains(sent, apiKey) {
		t.Errorf("deploy after a PUT of an empty key sent %s, want the stored key kept, as for a package secret with no default", sent)
	}
	if body := api.call(http.MethodGet, "/agents/"+created.ID, nil).Body.String(); strings.Contains(body, apiKey) || !strings.Contains(body, "REDACTED") {
		t.Errorf("GET after a PUT of an empty key = %s, want the key still redacted", body)
	}
}
