// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	_ "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/mcp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const crossFamilyMCPKey = "mcp-api-key-the-test-supplies"

func crossFamilyAPIKeyMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != crossFamilyMCPKey && r.URL.Query().Get("api_key") != crossFamilyMCPKey {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var rq struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &rq)
		if len(rq.ID) == 0 || string(rq.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any = map[string]any{}
		switch rq.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "apikey-mcp", "version": "1.0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name": "echo", "description": "Echo a message.",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			}}}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": rq.ID, "result": result})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func crossFamilyCreateMCPConnector(t *testing.T, app *apiClient, name string, values map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	return app.call(http.MethodPost, "/connectors", map[string]any{
		"name": name, "description": "An MCP server the agents call.",
		"connectorType": "mcp", "connectorSubtype": "remote", "values": values,
	})
}

func crossFamilyMCPAPIKeyValues(secret any) map[string]any {
	return map[string]any{
		"server_url": "https://mcp.example.com/mcp", "connection_type": "streamable-http",
		"auth_type": "apikey", "auth_apikey_location": "header", "auth_apikey_name": "X-API-Key",
		"auth_apikey_value": secret,
	}
}

func TestCrossFamily_DeploymentBodyNeedsAWellFormedID(t *testing.T) {
	api, _ := newAWEAPI(t)
	for _, tc := range []struct{ collection, idField string }{
		{"/agentDeployments", "agentId"},
		{"/entrypointDeployments", "entrypointId"},
		{"/workflowDeployments", "workflowId"},
	} {
		t.Run(tc.collection, func(t *testing.T) {
			expectDetail(t, api.call(http.MethodPost, tc.collection, map[string]any{tc.idField: "not-a-uuid", "action": "deploy"}),
				http.StatusUnprocessableEntity, tc.idField)
			expectDetail(t, api.call(http.MethodPost, tc.collection, map[string]any{"action": "deploy"}),
				http.StatusUnprocessableEntity, tc.idField)
			listed := decodeList[map[string]any](t, api.call(http.MethodGet, tc.collection, nil))
			kept := slices.DeleteFunc(listed.Data, func(d map[string]any) bool { return d["createdBy"] == "system" })
			if len(kept) != 0 {
				t.Errorf("a refused deploy kept a record: %+v", kept)
			}
		})
	}
}

func TestCrossFamily_AgentCreateRules(t *testing.T) {
	api, _ := newAWEAPI(t)
	agent := func(name string, fields ...any) map[string]any {
		return with(map[string]any{"name": name, "description": "Agent the create rules judge."}, fields...)
	}

	expectDetail(t, api.call(http.MethodPost, "/agents", agent("typed-standard", "agentType", "standard")),
		http.StatusUnprocessableEntity, "systemPrompt")
	expectDetail(t, api.call(http.MethodPost, "/agents", agent("proxy-agent", "agentType", "a2a_proxy", "systemPrompt", "You proxy.")),
		http.StatusUnprocessableEntity, "agentType")

	expectCreated[Agent](t, api.call(http.MethodPost, "/agents", agent("rcat-061-collide", "systemPrompt", "You help.")), "agents")
	expectCreated[Agent](t, api.call(http.MethodPost, "/agents", agent("rcat 061 collide", "systemPrompt", "You help.")), "agents")
}

func TestCrossFamily_AgentInstructionBoundIsTheOperatorsAndIsAdvertised(t *testing.T) {
	const maxChars = 40
	core, _ := newTestCoreWithConfig(t, map[string]any{"agent_instruction_max_chars": maxChars})
	api := &entrypointAPI{apiClient: newAPIClient(t, core)}
	agent := func(name, prompt string) map[string]any {
		return map[string]any{"name": name, "description": "Agent the bound judges.", "systemPrompt": prompt}
	}

	over := api.call(http.MethodPost, "/agents", agent("over-limit", strings.Repeat("a", maxChars+1)))
	expectDetail(t, over, http.StatusUnprocessableEntity, "systemPrompt")
	if detail := strings.Join(decodeError(t, over).ValidationDetails["systemPrompt"], " "); !strings.Contains(detail, "40") || !strings.Contains(detail, "41") {
		t.Errorf("systemPrompt detail %q must name the bound and the length sent", detail)
	}

	terse := expectCreated[Agent](t, api.call(http.MethodPost, "/agents", agent("terse-agent", "You are a SQL helper.")), "agents")
	expectCreated[Agent](t, api.call(http.MethodPost, "/agents", agent("at-limit", strings.Repeat("a", maxChars))), "agents")

	path := "/agents/" + terse.ID
	expectStatus(t, api.call(http.MethodPut, path, agent("terse-agent", strings.Repeat("目", maxChars))), http.StatusOK)
	expectDetail(t, api.call(http.MethodPut, path, agent("terse-agent", strings.Repeat("b", maxChars+1))), http.StatusUnprocessableEntity, "systemPrompt")

	limits := decodeData[AgentPlatformDefaults](t, api.call(http.MethodGet, "/agentPlatformDefaults", nil)).ValidationLimits
	if limits.AgentInstructionMax != maxChars || limits.AgentInstructionMin != 1 {
		t.Errorf("advertised bounds = %d..%d, want the enforced 1..%d", limits.AgentInstructionMin, limits.AgentInstructionMax, maxChars)
	}
}

func TestCrossFamily_DeployRefusesAToolsetStillInDiscovery(t *testing.T) {
	api, _ := newAWEAPI(t)
	repo := store.NewToolPackageRepo(api.core.DB())
	zipKey := "packages/pending-pkg/v1.zip"
	pkg := &store.ToolPackage{Name: "pending-pkg", DiscoveryStatus: "pending", ZipResourceKey: &zipKey, CreatedBy: "u", UpdatedBy: "u"}
	if err := repo.Create(t.Context(), pkg); err != nil {
		t.Fatalf("create tool package: %v", err)
	}
	agent := expectCreated[Agent](t, api.call(http.MethodPost, "/agents", map[string]any{
		"name": "pending-pkg-agent", "description": "Agent on a toolset still in discovery.",
		"systemPrompt": "You wait for your tools.", "toolsets": []string{pkg.Name},
	}), "agents")

	refused := deployAgent(api, agent.ID)
	expectStatus(t, refused, http.StatusUnprocessableEntity)
	if !strings.Contains(refused.Body.String(), "pending") {
		t.Errorf("refusal %s must say the toolset is pending", refused.Body.String())
	}

	if err := repo.UpdateDiscoveryStatus(t.Context(), pkg.ID, "ready", nil); err != nil {
		t.Fatalf("mark the package ready: %v", err)
	}
	expectCreated[AgentDeployment](t, deployAgent(api, agent.ID), "agentDeployments")
}

func TestCrossFamily_ConcurrentUndeploysOfOneAgentAllSucceed(t *testing.T) {
	api, _ := newAWEAPI(t)
	agent := createAgent(api, "concurrent-undeploy")
	expectCreated[AgentDeployment](t, deployAgent(api, agent.ID), "agentDeployments")

	const n = 3
	recs := make([]*httptest.ResponseRecorder, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			recs[i] = api.call(http.MethodPost, "/agentDeployments", map[string]any{"agentId": agent.ID, "action": "undeploy"})
		})
	}
	wg.Wait()
	for i, rec := range recs {
		if rec.Code != http.StatusCreated {
			t.Errorf("undeploy %d = %d, want 201; body %s", i, rec.Code, rec.Body.String())
		}
	}
}

func TestCrossFamily_ConnectorNameFilterIgnoresCase(t *testing.T) {
	app := newConnectorAPI(t)
	createNamedConnector(t, app, "test-connector")

	if got := decodeList[Connector](t, app.call(http.MethodGet, "/connectors?name=TEST", nil)); got.Meta.Pagination.Count != 1 {
		t.Errorf("name=TEST matched %v, want the one connector", connectorNames(got.Data))
	}
	if got := decodeList[Connector](t, app.call(http.MethodGet, "/connectors?name=nonexistent", nil)); got.Meta.Pagination.Count != 0 {
		t.Errorf("name=nonexistent matched %v, want none", connectorNames(got.Data))
	}
}

func TestCrossFamily_ConnectorValuesMustBeAnObject(t *testing.T) {
	app := newConnectorAPI(t)
	for name, values := range map[string]any{"a string": "not an object", "a list": []any{1, 2, 3}, "a number": 42} {
		t.Run(name, func(t *testing.T) {
			expectDetail(t, app.call(http.MethodPost, "/connectors", map[string]any{
				"name": "bad-values", "description": "Connector with values that are not an object.",
				"connectorType": "api", "connectorSubtype": "openapi", "values": values,
			}), http.StatusBadRequest, "values")
		})
	}
}

func TestCrossFamily_MCPConnectorSecretSurvivesANullAndRefusesANonString(t *testing.T) {
	app := newConnectorAPI(t)
	const secret = "PLAINTEXT-SECRET-MUST-NOT-BE-CLEARED"
	rec := crossFamilyCreateMCPConnector(t, app, "secret-handling", crossFamilyMCPAPIKeyValues(secret))
	expectStatus(t, rec, http.StatusCreated)
	id := decodeData[Connector](t, rec).ID

	expectStatus(t, app.call(http.MethodPatch, "/connectors/"+id, map[string]any{"values": crossFamilyMCPAPIKeyValues(nil)}), http.StatusOK)
	diff := app.call(http.MethodPut, "/connectors/"+id+"/secretsDiff", map[string]any{"values": map[string]any{"auth_apikey_value": secret}})
	expectStatus(t, diff, http.StatusOK)
	if !decodeData[SecretsDiffResult](t, diff).Secrets["auth_apikey_value"].Matches {
		t.Errorf("a null secret in a patch cleared the stored one: %s", diff.Body.String())
	}

	for name, value := range map[string]any{"an object": map[string]any{"hidden": "sk-1234"}, "a list": []any{"sk-1", "sk-2"}, "a number": 42} {
		t.Run(name, func(t *testing.T) {
			expectStatus(t, app.call(http.MethodPatch, "/connectors/"+id, map[string]any{"values": crossFamilyMCPAPIKeyValues(value)}),
				http.StatusUnprocessableEntity)
		})
	}
}

func TestCrossFamily_MCPConnectorCreateRefusesUnsafeValues(t *testing.T) {
	app := newConnectorAPI(t)
	const serverURL = "https://mcp.example.com/mcp"
	for name, values := range map[string]map[string]any{
		"a line break in a header value": {"server_url": serverURL, "custom_headers": map[string]any{"X-Bad": "value\r\nX-Injected: pwn"}},
		"a line break in a header name":  {"server_url": serverURL, "custom_headers": map[string]any{"X-Bad\r\nX-Injected": "ok"}},
		"a server that is not http":      {"server_url": "ftp://mcp.example.com/"},
		"a connection type not offered":  {"server_url": serverURL, "connection_type": "websocket"},
	} {
		t.Run(name, func(t *testing.T) {
			expectStatus(t, crossFamilyCreateMCPConnector(t, app, "unsafe-mcp", values), http.StatusUnprocessableEntity)
		})
	}
}

func TestCrossFamily_MCPDiscoverRefusals(t *testing.T) {
	app := newConnectorAPI(t)
	expectDetail(t, app.call(http.MethodPut, "/connectors/mcp/discover", map[string]any{}), http.StatusUnprocessableEntity, "serverUrl")
	expectStatus(t, app.call(http.MethodPut, "/connectors/mcp/discover", map[string]any{"serverUrl": "ftp://example.com/"}),
		http.StatusUnprocessableEntity)
	expectStatus(t, app.call(http.MethodPut, "/connectors/mcp/discover", map[string]any{
		"serverUrl": "http://127.0.0.1:9", "connectionType": "streamable_http",
	}), http.StatusBadGateway)
}

func TestCrossFamily_MCPDiscoverSendsTheAPIKeyWhereItIsAsked(t *testing.T) {
	app := newConnectorAPI(t)
	srv := crossFamilyAPIKeyMCPServer(t)
	discover := func(auth map[string]any) *httptest.ResponseRecorder {
		body := map[string]any{"serverUrl": srv.URL, "connectionType": "streamable_http"}
		if auth != nil {
			body["auth"] = auth
		}
		return app.call(http.MethodPut, "/connectors/mcp/discover", body)
	}

	for in, name := range map[string]string{"header": "X-API-Key", "query": "api_key"} {
		t.Run(in, func(t *testing.T) {
			rec := discover(map[string]any{"type": "apikey", "apiKey": map[string]any{"in": in, "name": name, "value": crossFamilyMCPKey}})
			expectStatus(t, rec, http.StatusOK)
			if tools := decodeList[MCPTool](t, rec).Data; len(tools) != 1 || tools[0].Name != "echo" {
				t.Errorf("tools = %+v, want echo", tools)
			}
		})
	}
	expectStatus(t, discover(nil), http.StatusBadGateway)
}

func TestCrossFamily_MCPOAuthInitiateRefusals(t *testing.T) {
	app := newConnectorAPI(t)
	expectMessage(t, app.call(http.MethodPut, "/connectors/mcp/oauth/initiate", map[string]any{
		"serverUrl": "http://localhost:8000", "mode": "manual",
	}), http.StatusUnprocessableEntity, "authorizationUrl, tokenUrl, and clientId are required for manual mode")
}

func TestCrossFamily_DetectAuthReadsYAMLAndRefusesSwagger2(t *testing.T) {
	app := newConnectorAPI(t)
	yamlSpec := "openapi: \"3.0.0\"\ninfo:\n  title: Test\n  version: \"1.0\"\npaths: {}\ncomponents:\n  securitySchemes:\n    apiKeyHeader:\n      type: apiKey\n      in: header\n      name: X-API-Key\n"

	res := decodeData[DetectAuthResult](t, app.call(http.MethodPut, "/connectors/openapi/detectAuth", map[string]any{"specificationContent": yamlSpec}))
	if len(res.Schemes) != 1 || res.Schemes[0].AuthType != "apikey" {
		t.Errorf("schemes = %+v, want the one apikey scheme", res.Schemes)
	}
	expectStatus(t, app.call(http.MethodPut, "/connectors/openapi/detectAuth", map[string]any{
		"specificationContent": `{"swagger": "2.0", "info": {"title": "Test", "version": "1.0"}, "paths": {}}`,
	}), http.StatusUnprocessableEntity)
}

func TestCrossFamily_EntrypointPublicURLSurvivesAPatchThatLeavesItOut(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	created := createTeams(t, api)
	path := "/entrypoints/" + created.ID
	const publicURL = "https://teams.example.com/api/messages"

	expectStatus(t, api.call(http.MethodPatch, path, map[string]any{"publicUrl": publicURL}), http.StatusOK)
	renamed := api.call(http.MethodPatch, path, map[string]any{"name": "renamed-teams"})
	expectStatus(t, renamed, http.StatusOK)
	if got := decodeData[Entrypoint](t, renamed).PublicURL; got == nil || *got != publicURL {
		t.Errorf("publicUrl after a patch without it = %v, want %q", got, publicURL)
	}
	expectDetail(t, api.call(http.MethodPatch, path, map[string]any{"publicUrl": "http://teams.example.com"}), http.StatusUnprocessableEntity, "publicUrl")
}

func TestCrossFamily_EntrypointCreateJudgesTheTypesSettings(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	create := func(name, entrypointType string, values any) *httptest.ResponseRecorder {
		return api.call(http.MethodPost, "/entrypoints", map[string]any{
			"name": name, "description": "Entrypoint whose settings are judged.", "entrypointType": entrypointType, "values": values,
		})
	}

	both := create("bad-teams", "teams", map[string]any{"microsoft_app_id": "not-a-guid", "microsoft_app_password": ""})
	expectStatus(t, both, http.StatusUnprocessableEntity)
	body := decodeError(t, both)
	if body.Message == "" || body.ErrorID == "" {
		t.Errorf("error envelope lacks message or errorId: %+v", body)
	}
	for _, field := range []string{"values.microsoft_app_id", "values.microsoft_app_password"} {
		if len(body.ValidationDetails[field]) == 0 {
			t.Errorf("validationDetails has nothing for %s: %v", field, body.ValidationDetails)
		}
	}

	expectStatus(t, create("null-teams", "teams", nil), http.StatusUnprocessableEntity)
	expectDetail(t, create("list-teams", "teams", []any{}), http.StatusBadRequest, "values")

	expectStatus(t, create("mcp-auth-off", "mcp", map[string]any{"enableAuth": "false"}), http.StatusCreated)
	reserved := create("mcp-system-identity", "mcp", map[string]any{"enableAuth": "false", "defaultUserIdentity": "system:default"})
	expectStatus(t, reserved, http.StatusUnprocessableEntity)
	if !strings.Contains(reserved.Body.String(), "reserved") {
		t.Errorf("refusal must name the reserved prefix: %s", reserved.Body.String())
	}
}

func TestCrossFamily_EntrypointTestConnectionProbesSavedAndSentSettings(t *testing.T) {
	api := newEntrypointAPI(t, nil)
	unreachable := map[string]any{"broker_url": "tcp://127.0.0.1:1", "broker_vpn": "default", "broker_username": "admin", "broker_password": "admin"}

	expectStatus(t, api.call(http.MethodPut, "/entrypoints/testConnection", map[string]any{"entrypointId": missingID}), http.StatusNotFound)

	sent := api.call(http.MethodPut, "/entrypoints/testConnection", map[string]any{"values": unreachable})
	expectStatus(t, sent, http.StatusOK)
	if decodeData[TestConnectionResult](t, sent).Success {
		t.Error("a probe of an unreachable broker succeeded")
	}

	values := with(unreachable, "broker_connection_type", "custom", "event_rules", []map[string]any{{
		"name": "smoke", "subscriptions": []map[string]any{{"topic": "events/>"}},
		"targetAgent": "agent1", "promptTemplate": "Process {{message}}",
	}})
	saved := expectCreated[Entrypoint](t, api.call(http.MethodPost, "/entrypoints", map[string]any{
		"name": "event-mesh-entrypoint", "description": "Event mesh entrypoint the probe reads.", "entrypointType": "event_mesh", "values": values,
	}), "entrypoints")
	probed := api.call(http.MethodPut, "/entrypoints/testConnection", map[string]any{"entrypointId": saved.ID})
	expectStatus(t, probed, http.StatusOK)
	if res := decodeData[TestConnectionResult](t, probed); res.Success || res.Message == nil || *res.Message == "" {
		t.Errorf("probe of the saved entrypoint = %s, want a failure that says why", probed.Body.String())
	}
}

func TestCrossFamily_RemoteAgentAuthTypeSwitchNeedsTheNewSecret(t *testing.T) {
	api, _ := newAWEAPI(t)
	body := remoteAgentBody("type-switch")
	body["agentCardLocation"] = "custom"
	body["agentCardUrl"] = "https://agents.example.com/card"
	body["agentCardAuthType"] = "bearer"
	body["agentCardAuthConfig"] = map[string]any{"type": "bearer", "token": cardToken}
	created := createRemoteAgent(api, body)
	path := "/remoteAgents/" + created.ID

	body["agentAuthType"] = "apikey"
	body["agentAuthConfig"] = map[string]any{"type": "apikey", "apiKey": ""}
	expectStatus(t, api.call(http.MethodPut, path, body), http.StatusUnprocessableEntity)
	if got := decodeData[RemoteAgent](t, api.call(http.MethodGet, path, nil)).AgentAuthConfig; got == nil || got.Type != "bearer" {
		t.Errorf("task auth after a refused switch = %+v, want bearer kept", got)
	}

	body["agentAuthConfig"] = map[string]any{"type": "apikey", "apiKey": "new-key", "headerName": "X-Key"}
	expectStatus(t, api.call(http.MethodPut, path, body), http.StatusOK)
	got := decodeData[RemoteAgent](t, api.call(http.MethodGet, path, nil))
	if got.AgentAuthConfig == nil || got.AgentAuthConfig.Type != "apikey" {
		t.Errorf("task auth after the switch = %+v, want apikey", got.AgentAuthConfig)
	}
	if got.AgentCardAuthConfig == nil || got.AgentCardAuthConfig.Type != "bearer" {
		t.Errorf("card auth = %+v, want bearer untouched", got.AgentCardAuthConfig)
	}
}

func TestCrossFamily_RemoteAgentCreateRules(t *testing.T) {
	api, _ := newAWEAPI(t)

	ftp := remoteAgentBody("ftp-agent")
	ftp["agentUrl"] = "ftp://not-http.example.com"
	expectDetail(t, api.call(http.MethodPost, "/remoteAgents", ftp), http.StatusUnprocessableEntity, "agentUrl")

	open := remoteAgentBody("open-agent")
	open["agentAuthType"] = "none"
	open["agentAuthConfig"] = map[string]any{"type": "none"}
	open["requestTimeoutSeconds"] = 60
	if got := createRemoteAgent(api, open); got.RequestTimeoutSeconds != 60 {
		t.Errorf("requestTimeoutSeconds = %d, want 60", got.RequestTimeoutSeconds)
	}

	listed := api.call(http.MethodGet, "/remoteAgents", nil, rbac.ScopeAgentBuilderRead)
	expectStatus(t, listed, http.StatusOK)
	if got := decodeList[RemoteAgent](t, listed); got.Meta.Pagination.Count != 1 {
		t.Errorf("read-only list = %d remote agents, want 1", got.Meta.Pagination.Count)
	}
}

func TestCrossFamily_ModelOAuth2SecretIsRedactedAndItsSettingsAreNot(t *testing.T) {
	api, _ := newModelAPI(t)
	created := createModel(api, "oauth-model", map[string]any{
		"type": "oauth2", "client_id": "my-client-id", "client_secret": modelSecret, "token_url": "https://auth.example.com/token",
	})
	for key, want := range map[string]any{"client_id": "my-client-id", "client_secret": redacted, "token_url": "https://auth.example.com/token"} {
		if got := created.AuthConfig[key]; got != want {
			t.Errorf("authConfig.%s = %v, want %v", key, got, want)
		}
	}
}

func TestCrossFamily_ModelAuthPatchWithoutTheKeyKeepsIt(t *testing.T) {
	api, svc := newModelAPI(t)
	created := createModel(api, "merge-test", apiKeyAuth())

	expectStatus(t, api.call(http.MethodPatch, "/models/"+created.ID, map[string]any{
		"authConfig": map[string]any{"type": "apikey", "org_id": "org-123"},
	}), http.StatusOK)

	got := decodeData[Model](t, api.call(http.MethodGet, "/models/"+created.ID, nil))
	if got.AuthConfig["org_id"] != "org-123" {
		t.Errorf("org_id = %v, want org-123", got.AuthConfig["org_id"])
	}
	_, stored, err := svc.GetAuthConfigCleartext(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("read the stored auth config: %v", err)
	}
	if stored["api_key"] != modelSecret {
		t.Errorf("stored api_key = %v, want the one the create stored", stored["api_key"])
	}
}

func TestCrossFamily_SkillPagesEndWithAPartialPage(t *testing.T) {
	core, _ := newTestCore(t)
	app := newAPIClient(t, core)
	repo := store.NewSkillRepo(core.DB())
	for i := range 25 {
		desc, tags := fmt.Sprintf("Skill %d", i), `["test"]`
		if err := repo.UpsertByName(t.Context(), &store.Skill{
			Name: fmt.Sprintf("page-skill-%02d", i), Description: &desc, Tags: &tags,
			Origin: "platform", DiscoveryStatus: "ready", CreatedBy: "system", UpdatedBy: "system",
		}); err != nil {
			t.Fatalf("seed skill %d: %v", i, err)
		}
	}

	first := decodeList[Skill](t, app.call(http.MethodGet, "/skills?pageNumber=1&pageSize=10", nil))
	total := first.Meta.Pagination.Count
	if len(first.Data) != 10 || total < 25 {
		t.Fatalf("page 1 = %d items of %d, want 10 of at least the 25 seeded", len(first.Data), total)
	}
	lastPage := (total + 9) / 10
	last := decodeList[Skill](t, app.call(http.MethodGet, fmt.Sprintf("/skills?pageNumber=%d&pageSize=10", lastPage), nil))
	if want := total - (lastPage-1)*10; len(last.Data) != want || last.Meta.Pagination.Count != total {
		t.Errorf("page %d = %d items of %d, want %d of %d", lastPage, len(last.Data), last.Meta.Pagination.Count, want, total)
	}
}
