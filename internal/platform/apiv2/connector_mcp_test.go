// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool/mcp"
)

func fakeMCPServer(t *testing.T, names []string) *httptest.Server {
	t.Helper()
	tools := make([]map[string]any, len(names))
	for i, name := range names {
		tools[i] = fakeMCPToolListing(name, nil)
	}
	return fakeMCPServerListing(t, tools)
}

func fakeMCPToolListing(name string, annotations map[string]any) map[string]any {
	tool := map[string]any{
		"name":        name,
		"description": "The " + name + " tool.",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"query": map[string]any{"type": "string"}},
		},
	}
	if annotations != nil {
		tool["annotations"] = annotations
	}
	return tool
}

func fakeMCPServerListing(t *testing.T, tools []map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string          `json:"method"`
			ID     json.RawMessage `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "parse json-rpc", http.StatusBadRequest)
			return
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("mcp-session-id", "fake-mcp-session")
			writeJSONRPCResult(t, w, req.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "fakeMCPServer", "version": "1.0.0"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeJSONRPCResult(t, w, req.ID, map[string]any{"tools": tools})
		default:
			http.Error(w, "unexpected JSON-RPC method", http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeJSONRPCResult(t *testing.T, w http.ResponseWriter, id json.RawMessage, result any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result}); err != nil {
		t.Errorf("write JSON-RPC result: %v", err)
	}
}

func failingRegistrationProvider(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 base,
			"authorization_endpoint": base + "/authorize",
			"token_endpoint":         base + "/token",
			"registration_endpoint":  base + "/register",
		}); err != nil {
			t.Errorf("write provider metadata: %v", err)
		}
	})
	mux.HandleFunc("POST /register", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"invalid_client_metadata"}`, http.StatusBadRequest)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func oauthTokenEndpoint(t *testing.T, accessToken string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"access_token": accessToken, "token_type": "Bearer", "expires_in": 3600,
		}); err != nil {
			t.Errorf("write token response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMCPDiscover_UsesTheListEnvelope(t *testing.T) {
	app := newConnectorAPI(t)
	server := fakeMCPServer(t, []string{"search_orders", "create_order"})

	rec := app.call(http.MethodPut, "/connectors/mcp/discover", map[string]any{
		"serverUrl": server.URL, "connectionType": "streamable_http",
	})
	expectStatus(t, rec, http.StatusOK)
	for _, want := range []string{`"data":[`, `"pagination"`} {
		if !bytes.Contains(rec.Body.Bytes(), []byte(want)) {
			t.Errorf("want the list envelope key %s: %s", want, rec.Body.String())
		}
	}

	tools := decodeList[MCPTool](t, rec).Data
	if len(tools) != 2 {
		t.Fatalf("got %d tools, want 2: %s", len(tools), rec.Body.String())
	}
	if tools[0].Name != "search_orders" || tools[0].InputSchema == nil {
		t.Errorf("a discovered tool must carry its name and input schema: %+v", tools[0])
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"outputSchema":null`)) {
		t.Errorf("outputSchema must be sent as null when the server declares none: %s", rec.Body.String())
	}
}

func TestMCPDiscover_AToolsHintsSurviveIntoTheManifestTheRuntimeParses(t *testing.T) {
	app := newConnectorAPI(t)
	server := fakeMCPServerListing(t, []map[string]any{
		fakeMCPToolListing("lookup_order", map[string]any{"readOnlyHint": true, "openWorldHint": false}),
		fakeMCPToolListing("create_order", nil),
	})

	rec := app.call(http.MethodPut, "/connectors/mcp/discover", map[string]any{
		"serverUrl": server.URL, "connectionType": "streamable_http",
	})
	expectStatus(t, rec, http.StatusOK)

	var discovered struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &discovered); err != nil {
		t.Fatalf("decode discovery response %s: %v", rec.Body.String(), err)
	}
	manifest := make([]any, len(discovered.Data))
	for i, tool := range discovered.Data {
		manifest[i] = tool
	}
	cfg, err := mcp.ParseToolsetConfig(map[string]any{
		"connection_params":     map[string]any{"type": "streamable-http", "url": "https://mcp.example.com/mcp"},
		mcp.TrustAnnotationsKey: true,
		"manifest":              manifest,
	})
	if err != nil {
		t.Fatalf("the runtime refused the manifest built from the discovery response: %v", err)
	}
	if len(cfg.Manifest) != 2 {
		t.Fatalf("manifest has %d tools, want 2", len(cfg.Manifest))
	}
	hints := cfg.Manifest[0].Annotations
	if hints.ReadOnlyHint == nil || !*hints.ReadOnlyHint || hints.OpenWorldHint == nil || *hints.OpenWorldHint || hints.DestructiveHint != nil {
		t.Errorf("lookup_order hints = %+v, want readOnlyHint true, openWorldHint false and destructiveHint unset", hints)
	}
	if got := cfg.Manifest[1].Annotations; got.ReadOnlyHint != nil || got.DestructiveHint != nil || got.OpenWorldHint != nil {
		t.Errorf("create_order hints = %+v, want none, because its server declared none", got)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"annotations":null`)) {
		t.Errorf("annotations must be sent as null when the server declares none: %s", rec.Body.String())
	}
}

func TestMCPAnnotationsDocs_NoConnectorSettingIsNamedBecauseNoConnectorHasOne(t *testing.T) {
	t.Parallel()
	texts := []string{MCPTool{}.APIDoc().Fields["Annotations"].Doc, MCPToolAnnotations{}.APIDoc().Type}
	for field, doc := range (MCPToolAnnotations{}).APIDoc().Fields {
		texts = append(texts, doc.Doc+" "+field)
	}
	for _, text := range texts {
		if strings.Contains(text, "connector sets trust_annotations") || strings.Contains(text, "connector's trust_annotations") {
			t.Errorf("published text %q names a connector trust_annotations setting; the key exists only on an agent's inline MCP entry", text)
		}
	}
	if !strings.Contains(MCPToolAnnotations{}.APIDoc().Type, "inline MCP tool entry") {
		t.Errorf("MCPToolAnnotations type doc %q must say where trust_annotations is set", MCPToolAnnotations{}.APIDoc().Type)
	}
}

func TestMCPDiscover_SendsThePlatformsSentence(t *testing.T) {
	logs := captureLogs(t)
	app := newConnectorAPI(t)
	t.Setenv("SAM_PLATFORM_ALLOW_PRIVATE_MCP", "")
	server := fakeMCPServer(t, []string{"search_orders"})

	rec := app.call(http.MethodPut, "/connectors/mcp/discover", map[string]any{
		"serverUrl": server.URL + "/mcp?token=" + discoverySecret, "connectionType": "streamable_http",
	})

	expectRefusedDiscovery(t, logs, rec, discoverySecret)
}

func TestMCPOAuthDiscoverWithToken_SendsThePlatformsSentence(t *testing.T) {
	logs := captureLogs(t)
	app := newConnectorAPI(t)
	mcpServer := fakeMCPServer(t, []string{"search_orders"})
	const accessToken = "ACCESS-TOKEN-MUST-NOT-APPEAR"
	tokens := oauthTokenEndpoint(t, accessToken)
	initiated := decodeData[MCPOAuthInitiateResult](t, app.call(http.MethodPut, "/connectors/mcp/oauth/initiate", map[string]any{
		"serverUrl": mcpServer.URL, "connectionType": "streamable_http", "mode": "manual",
		"authorizationUrl": "https://auth.example.com/authorize",
		"tokenUrl":         tokens.URL,
		"clientId":         "sam-mcp-discovery",
	}))
	expectStatus(t, app.anonymous().call(http.MethodGet, "/connectors/mcp/oauth/callback?code=auth-code&state="+initiated.State, nil), http.StatusOK)
	t.Setenv("SAM_PLATFORM_ALLOW_PRIVATE_MCP", "")

	rec := app.call(http.MethodPut, "/connectors/mcp/oauth/discoverWithToken", map[string]any{
		"sessionId": initiated.SessionID, "serverUrl": mcpServer.URL,
	})

	expectRefusedDiscovery(t, logs, rec, accessToken)
}

func TestMCPDiscover_BothRoutesHandSSLConfigToDiscovery_SoAHalfKeypairIsRefused(t *testing.T) {
	app := newConnectorAPI(t)
	mcpServer := fakeMCPServer(t, []string{"search_orders"})
	halfKeypair := map[string]any{"clientCert": "/etc/sam/mtls/client.crt"}

	expectDetail(t, app.call(http.MethodPut, "/connectors/mcp/discover", map[string]any{
		"serverUrl": mcpServer.URL, "connectionType": "streamable_http", "sslConfig": halfKeypair,
	}), http.StatusUnprocessableEntity, "sslConfig.clientKey")

	initiated := decodeData[MCPOAuthInitiateResult](t, app.call(http.MethodPut, "/connectors/mcp/oauth/initiate", map[string]any{
		"serverUrl": mcpServer.URL, "connectionType": "streamable_http", "mode": "manual",
		"authorizationUrl": "https://auth.example.com/authorize",
		"tokenUrl":         oauthTokenEndpoint(t, "access-token").URL,
		"clientId":         "sam-mcp-discovery",
	}))
	expectStatus(t, app.anonymous().call(http.MethodGet, "/connectors/mcp/oauth/callback?code=auth-code&state="+initiated.State, nil), http.StatusOK)
	expectDetail(t, app.call(http.MethodPut, "/connectors/mcp/oauth/discoverWithToken", map[string]any{
		"sessionId": initiated.SessionID, "serverUrl": mcpServer.URL, "sslConfig": halfKeypair,
	}), http.StatusUnprocessableEntity, "sslConfig.clientKey")
	expectStatus(t, app.call(http.MethodPut, "/connectors/mcp/oauth/discoverWithToken", map[string]any{
		"sessionId": initiated.SessionID, "serverUrl": mcpServer.URL,
	}), http.StatusOK)
}

const discoverySecret = "QUERY-SECRET-MUST-NOT-APPEAR"

func expectRefusedDiscovery(t *testing.T, logs *lockedBuffer, rec *httptest.ResponseRecorder, secret string) {
	t.Helper()
	expectStatus(t, rec, http.StatusBadGateway)
	answer := decodeError(t, rec)
	for _, want := range []string{"The MCP server URL resolves to a loopback or private network address", "an administrator can allow it"} {
		if !strings.Contains(answer.Message, want) {
			t.Errorf("message = %q, want it to contain %q", answer.Message, want)
		}
	}
	var lines []map[string]any
	for line := range strings.Lines(logs.String()) {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) == nil && entry["errorId"] == answer.ErrorID {
			lines = append(lines, entry)
		}
	}
	if len(lines) != 1 || lines[0]["level"] != "WARN" {
		t.Fatalf("log lines for errorId %s = %v, want one WARN", answer.ErrorID, lines)
	}
	if cause, _ := lines[0]["error"].(string); !strings.Contains(cause, "initialize: dial") || !strings.Contains(cause, "address rejected by SSRF guard") {
		t.Errorf("logged error = %q, want the step and the redacted guard cause", cause)
	}
	if strings.Contains(logs.String(), secret) || strings.Contains(rec.Body.String(), secret) {
		t.Errorf("%q reached a log or the response", secret)
	}
}

func TestMCPDiscover_RejectsTheKebabCaseConnectionType(t *testing.T) {
	app := newConnectorAPI(t)
	expectDetail(t, app.call(http.MethodPut, "/connectors/mcp/discover", map[string]any{
		"serverUrl": "https://mcp.example.com/mcp", "connectionType": "streamable-http",
	}), http.StatusUnprocessableEntity, "connectionType")
}

func TestMCPDiscover_RejectionsNameTheField(t *testing.T) {
	app := newConnectorAPI(t)
	expectDetailSaying(t, app.call(http.MethodPut, "/connectors/mcp/discover", map[string]any{
		"serverUrl": "ftp://mcp.example.com/mcp", "connectionType": "streamable_http",
	}), "serverUrl", "use http or https")
	expectDetailSaying(t, app.call(http.MethodPut, "/connectors/mcp/discover", map[string]any{
		"serverUrl": "https://mcp.example.com/mcp", "connectionType": "streamable_http",
		"customHeaders": map[string]string{"X-Tenant": "acme\r\nX-Injected: yes"},
	}), "customHeaders", "contains CR/LF")
}

func refusingTokenEndpoint(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMCPOAuthCallback_AFailedTokenExchangeStillRendersThePage(t *testing.T) {
	app := newConnectorAPI(t)
	mcpServer := fakeMCPServer(t, []string{"search_orders"})
	tokens := refusingTokenEndpoint(t)

	initiated := decodeData[MCPOAuthInitiateResult](t, app.call(http.MethodPut, "/connectors/mcp/oauth/initiate", map[string]any{
		"serverUrl": mcpServer.URL, "mode": "manual",
		"authorizationUrl": "https://auth.example.com/authorize",
		"tokenUrl":         tokens.URL,
		"clientId":         "sam-mcp-discovery",
	}))

	rec := app.anonymous().call(http.MethodGet, "/connectors/mcp/oauth/callback?code=auth-code&state="+initiated.State, nil)

	expectStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q, want text/html: the browser is the caller here, not an API client", got)
	}
	if body := rec.Body.String(); !strings.Contains(body, "mcp-oauth-error") {
		t.Errorf("body does not post the failure back to the opener: %s", body)
	}
	if body := rec.Body.String(); !strings.Contains(body, "postMessage") {
		t.Errorf("body carries no postMessage, so the opening window would never learn the flow ended: %s", body)
	}
}

func TestMCPOAuthCallback_IsReachableWithoutAnIdentity(t *testing.T) {
	app := newConnectorAPI(t)
	mcpServer := fakeMCPServer(t, []string{"search_orders"})
	tokens := oauthTokenEndpoint(t, "placeholder-access-token")

	t.Run("a caller with no connector scope reaches it", func(t *testing.T) {
		rec := app.as(rbac.ScopeEntrypointRead).call(http.MethodGet, "/connectors/mcp/oauth/callback?code=x&state=unknown", nil)
		if rec.Code == http.StatusForbidden || rec.Code == http.StatusNotFound {
			t.Errorf("status %d: the callback must not be scope-gated, the identity provider redirects a browser here", rec.Code)
		}
	})

	t.Run("and so does a caller with no identity at all", func(t *testing.T) {
		initiated := decodeData[MCPOAuthInitiateResult](t, app.call(http.MethodPut, "/connectors/mcp/oauth/initiate", map[string]any{
			"serverUrl": mcpServer.URL, "mode": "manual",
			"authorizationUrl": "https://auth.example.com/authorize",
			"tokenUrl":         tokens.URL,
			"clientId":         "sam-mcp-discovery",
		}))

		rec := app.anonymous().call(http.MethodGet, "/connectors/mcp/oauth/callback?code=auth-code&state="+initiated.State, nil)
		expectStatus(t, rec, http.StatusOK)
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Errorf("Content-Type = %q, want the HTML page the browser lands on", got)
		}
		if body := rec.Body.String(); !strings.Contains(body, initiated.SessionID) {
			t.Errorf("the page must hand the session back to the opener: %s", body)
		}
	})
}

func TestMCPOAuthInitiate_RegistrationFailureHidesUpstreamText(t *testing.T) {
	app := newConnectorAPI(t)
	provider := failingRegistrationProvider(t)

	rec := app.call(http.MethodPut, "/connectors/mcp/oauth/initiate", map[string]any{
		"serverUrl": provider.URL, "mode": "discovery",
	})
	expectStatus(t, rec, http.StatusBadGateway)
	raw := rec.Body.Bytes()
	for _, leak := range []string{"dynamic client registration", "registration endpoint returned", provider.URL} {
		if bytes.Contains(raw, []byte(leak)) {
			t.Errorf("upstream text %q reached the wire: %s", leak, raw)
		}
	}
	if got := decodeError(t, rec).Message; got != msgMCPRegistrationRefused {
		t.Errorf("message = %q, want the fixed sentence %q", got, msgMCPRegistrationRefused)
	}
}

func TestMCPOAuthInitiate_SendsTheProviderThisVersionsCallback(t *testing.T) {
	app := newConnectorAPI(t)

	rec := app.call(http.MethodPut, "/connectors/mcp/oauth/initiate", map[string]any{
		"serverUrl": "https://mcp.example.com/mcp", "mode": "manual",
		"authorizationUrl": "https://auth.example.com/authorize",
		"tokenUrl":         "https://auth.example.com/token",
		"clientId":         "sam-mcp-discovery",
	})
	expectStatus(t, rec, http.StatusOK)
	result := decodeData[MCPOAuthInitiateResult](t, rec)

	authURL, err := url.Parse(result.AuthorizationURL)
	if err != nil {
		t.Fatalf("authorizationUrl is not a URL: %v", err)
	}
	const want = "http://example.com/api/v2/platform/connectors/mcp/oauth/callback"
	if got := authURL.Query().Get("redirect_uri"); got != want {
		t.Errorf("redirect_uri = %q, want %q: the provider must return the browser to the version that began the flow", got, want)
	}
	if result.State == "" || result.SessionID == "" {
		t.Errorf("initiate must name the state and session: %+v", result)
	}
}

func TestMCPOAuth_FlowCompletesAcrossTheV2Routes(t *testing.T) {
	app := newConnectorAPI(t)
	mcpServer := fakeMCPServer(t, []string{"search_orders"})
	tokens := oauthTokenEndpoint(t, "placeholder-access-token")

	initiated := decodeData[MCPOAuthInitiateResult](t, app.call(http.MethodPut, "/connectors/mcp/oauth/initiate", map[string]any{
		"serverUrl": mcpServer.URL, "connectionType": "streamable_http", "mode": "manual",
		"authorizationUrl": "https://auth.example.com/authorize",
		"tokenUrl":         tokens.URL,
		"clientId":         "sam-mcp-discovery",
	}))

	statusOf := func() string {
		t.Helper()
		rec := app.call(http.MethodGet, "/connectors/mcp/oauth/status?sessionId="+initiated.SessionID, nil)
		expectStatus(t, rec, http.StatusOK)
		return decodeData[MCPOAuthStatusResult](t, rec).Status
	}
	if got := statusOf(); got != "pending" {
		t.Errorf("status before the callback = %q, want pending", got)
	}

	callback := app.anonymous().call(http.MethodGet, "/connectors/mcp/oauth/callback?code=auth-code&state="+initiated.State, nil)
	expectStatus(t, callback, http.StatusOK)
	if got := callback.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("Content-Type = %q, want an HTML page for the browser", got)
	}
	if body := callback.Body.String(); !strings.Contains(body, initiated.SessionID) {
		t.Errorf("the page must hand the session back to the opener: %s", body)
	}
	if got := statusOf(); got != "completed" {
		t.Errorf("status after the callback = %q, want completed", got)
	}

	rec := app.call(http.MethodPut, "/connectors/mcp/oauth/discoverWithToken", map[string]any{
		"sessionId": initiated.SessionID, "serverUrl": mcpServer.URL,
	})
	expectStatus(t, rec, http.StatusOK)
	tools := decodeList[MCPTool](t, rec).Data
	if len(tools) != 1 || tools[0].Name != "search_orders" {
		t.Fatalf("discoverWithToken = %+v, want the one tool the server publishes", tools)
	}
	if got := statusOf(); got != "expired" {
		t.Errorf("status after the session is spent = %q, want expired", got)
	}
}

func TestMCPOAuthStatus_NeedsASession(t *testing.T) {
	app := newConnectorAPI(t)
	expectDetail(t, app.call(http.MethodGet, "/connectors/mcp/oauth/status", nil), http.StatusUnprocessableEntity, "sessionId")
}

func TestMCPOAuthDiscoverWithToken_UnknownSessionIsRefused(t *testing.T) {
	app := newConnectorAPI(t)
	rec := app.call(http.MethodPut, "/connectors/mcp/oauth/discoverWithToken", map[string]any{
		"sessionId": "0123456789abcdef0123456789abcdef",
	})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
}

func TestMCPRoutes_PathsExist(t *testing.T) {
	app := newConnectorAPI(t)
	server := fakeMCPServer(t, []string{"search_orders"})

	for _, rt := range []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodPut, "/connectors/mcp/discover", map[string]any{"serverUrl": server.URL, "connectionType": "streamable_http"}},
		{http.MethodPut, "/connectors/mcp/oauth/initiate", map[string]any{"serverUrl": server.URL, "mode": "manual"}},
		{http.MethodGet, "/connectors/mcp/oauth/callback?code=x&state=unknown", nil},
		{http.MethodGet, "/connectors/mcp/oauth/status?sessionId=unknown", nil},
		{http.MethodPut, "/connectors/mcp/oauth/discoverWithToken", map[string]any{"sessionId": "unknown"}},
	} {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			if rec := app.call(rt.method, rt.path, rt.body); rec.Code == http.StatusNotFound {
				t.Errorf("the route is not mounted: %s", rec.Body.String())
			}
		})
	}
}
