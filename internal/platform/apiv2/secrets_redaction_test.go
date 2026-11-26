// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func expectNoneOf(t *testing.T, what, body string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(body, secret) {
			t.Errorf("%s carries %q: %s", what, secret, body)
		}
	}
}

func TestSecrets_V2RedactsAgentOverlay(t *testing.T) {
	const toolset, toolsetSecret, skillSecret = "overlay-toolset", "sk-overlay-toolset-secret", "sk-overlay-skill-secret"
	api, _ := newAWEAPI(t)
	seedSecretToolset(t, api, toolset)
	skillSchema := `[{"tool_name":"render","config_schema":[{"key":"api_key","type":"string","secret":true},{"key":"model_id","type":"string"}]}]`
	skill := &store.Skill{Name: "overlay_skill", Origin: "platform", DiscoveryStatus: "ready", BundledTools: &skillSchema, CreatedBy: "u", UpdatedBy: "u"}
	if err := store.NewSkillRepo(api.core.DB()).Create(t.Context(), skill); err != nil {
		t.Fatalf("create skill: %v", err)
	}

	rec := api.call(http.MethodPost, "/agents", map[string]any{
		"name": "overlay-agent", "description": "Agent with overlay secrets.", "systemPrompt": "You fetch things.",
		"toolsets":       []string{toolset},
		"toolsetConfigs": []map[string]any{{"toolsetName": toolset, "configValues": map[string]any{"fetch": map[string]any{"api_key": toolsetSecret, "region": "eu"}}}},
		"skillIds":       []string{skill.ID},
		"skillConfigs":   []map[string]any{{"skillId": skill.ID, "configValues": map[string]any{"render": map[string]any{"api_key": skillSecret, "model_id": "m1"}}}},
	})
	id := expectCreated[Agent](t, rec, "agents").ID
	expectNoneOf(t, "create", rec.Body.String(), toolsetSecret, skillSecret)

	got := api.call(http.MethodGet, "/agents/"+id, nil)
	expectStatus(t, got, http.StatusOK)
	expectNoneOf(t, "get", got.Body.String(), toolsetSecret, skillSecret)
	if n := strings.Count(got.Body.String(), "REDACTED"); n != 2 {
		t.Errorf("get has %d REDACTED placeholders, want one per overlay secret: %s", n, got.Body.String())
	}
	list := api.call(http.MethodGet, "/agents", nil)
	expectStatus(t, list, http.StatusOK)
	expectNoneOf(t, "list", list.Body.String(), toolsetSecret, skillSecret)
}

func TestSecrets_V2RedactsModelAndConnector(t *testing.T) {
	const connectorSecret = "connector-password-the-test-supplies"
	api, _ := newModelAPI(t)
	model := createModel(api, "secret-model", apiKeyAuth())

	mongo := databaseConnectorCases[0]
	values := map[string]any{}
	for k, v := range mongo.values {
		values[k] = v
	}
	values["password"] = connectorSecret
	rec := api.call(http.MethodPost, "/connectors", map[string]any{
		"name": "secret-connector", "description": "Connector with a secret.",
		"connectorType": mongo.connType, "connectorSubtype": mongo.subtype, "values": values,
	})
	expectStatus(t, rec, http.StatusCreated)
	connector := decodeData[Connector](t, rec)

	for what, rec := range map[string]*httptest.ResponseRecorder{
		"model get":      api.call(http.MethodGet, "/models/"+model.ID, nil),
		"model list":     api.call(http.MethodGet, "/models", nil),
		"connector get":  api.call(http.MethodGet, "/connectors/"+connector.ID, nil),
		"connector list": api.call(http.MethodGet, "/connectors", nil),
	} {
		expectStatus(t, rec, http.StatusOK)
		expectNoneOf(t, what, rec.Body.String(), modelSecret, connectorSecret)
		if !strings.Contains(rec.Body.String(), "REDACTED") {
			t.Errorf("%s has no %s placeholder: %s", what, redacted, rec.Body.String())
		}
	}
}

func TestSecrets_V2RedactsToolsetAndSkill(t *testing.T) {
	const searchKey = "AIza" + "FAKEKEY0000000000000000000000000000"
	api := newSkillAPI(t)

	toolset := api.toolsetAPI.createReady("secret_tools")
	expectStatus(t, api.call(http.MethodPatch, "/toolsets/"+toolset.ID+"/config", map[string]any{
		"values": map[string]any{"lookup": map[string]any{"api_key": toolsetAPIKeySecret}},
	}), http.StatusOK)

	skill := api.createReady("secret-skill")
	expectStatus(t, api.call(http.MethodPatch, "/skills/"+skill.ID+"/config", map[string]any{
		"values": map[string]any{"find_order": map[string]any{"api_key": skillAPIKeySecret}},
	}), http.StatusOK)

	manifest := zipOf(t, map[string]string{
		"SKILL.md": "---\nname: web-search\ndescription: Search the web.\ntools:\n" +
			"  - tool_type: builtin\n    tool_name: web_search_google\n    tool_config:\n" +
			"      google_search_api_key: " + searchKey + "\n---\n# web-search\n",
	})
	created := uploadForm(api.entrypointAPI, http.MethodPost, "/skills", "web-search.zip", manifest, nil)
	expectStatus(t, created, http.StatusCreated)
	bundled := decodeData[Skill](t, created)

	secrets := []string{toolsetAPIKeySecret, skillAPIKeySecret, searchKey}
	for what, path := range map[string]string{
		"toolset config": "/toolsets/" + toolset.ID + "/config",
		"toolset get":    "/toolsets/" + toolset.ID,
		"toolset list":   "/toolsets",
		"skill config":   "/skills/" + skill.ID + "/config",
		"skill get":      "/skills/" + skill.ID,
		"skill list":     "/skills",
		"skill SKILL.md": "/skills/" + bundled.ID + "/fileContent?path=SKILL.md",
	} {
		rec := api.call(http.MethodGet, path, nil)
		expectStatus(t, rec, http.StatusOK)
		expectNoneOf(t, what, rec.Body.String(), secrets...)
	}
	for what, path := range map[string]string{"toolset config": "/toolsets/" + toolset.ID + "/config", "skill config": "/skills/" + skill.ID + "/config", "skill SKILL.md": "/skills/" + bundled.ID + "/fileContent?path=SKILL.md"} {
		if body := api.call(http.MethodGet, path, nil).Body.String(); !strings.Contains(body, "REDACTED") {
			t.Errorf("%s has no placeholder: %s", what, body)
		}
	}
}
