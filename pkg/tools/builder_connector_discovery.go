// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type DiscoverMCPToolsTool struct {
	artifactToolBase
	tool.SideEffectingTool
}

func (t *DiscoverMCPToolsTool) Name() string { return "discover_mcp_tools" }

func (t *DiscoverMCPToolsTool) Description() string {
	return "Connects to an MCP server and discovers available tools. " +
		"Returns the list of tool names, descriptions, and input schemas, plus an " +
		"output schema for each tool whose server declares one. " +
		"Use this when building an MCP connector to preview what tools will be available. " +
		"Requires the server URL and optionally authentication credentials. " +
		"If the server requires auth that hasn't been provided, skip discovery — " +
		"the UI will handle it after credentials are filled in."
}

func (t *DiscoverMCPToolsTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"server_url": map[string]any{
				"type":        "string",
				"description": "The URL of the MCP server to discover tools from.",
			},
			"connection_type": map[string]any{
				"type":        "string",
				"description": "Connection type: 'streamable-http' (default) or 'sse'.",
				"enum":        []string{"streamable-http", "sse"},
			},
			"auth": map[string]any{
				"type":        "object",
				"description": "Optional authentication. Object with 'type' (none/apikey/http) and type-specific fields.",
			},
		},
		Required: []string{"server_url"},
	}
}

func (t *DiscoverMCPToolsTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "tool_count", typ: "integer", description: "Number of tools the MCP server advertised. Absent when the response could not be parsed.", optional: true},
		resultField{name: "tool_names", typ: "array", description: "Names of the discovered tools. Their full schemas go to the mcp_discovered_tools.json artifact, not here. Absent when the response could not be parsed.", optional: true},
		resultField{name: "raw_response", typ: "string", description: "Unparsed JSON text of the discovery response. Present only when the response was not a JSON array, and then instead of tool_count and tool_names.", optional: true},
	)
}

func (t *DiscoverMCPToolsTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if toolCtx.ControlQuerier == nil {
		return tool.Error("Platform service not available. MCP discovery requires the platform service to be running."), nil
	}

	serverURL, _ := args["server_url"].(string)
	if serverURL == "" {
		return tool.Error("server_url is required"), nil
	}

	payload := map[string]any{
		"serverUrl": serverURL,
	}
	if ct, ok := args["connection_type"].(string); ok && ct != "" {
		payload["connectionType"] = ct
	}
	if auth, ok := args["auth"].(map[string]any); ok {
		payload["auth"] = auth
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return tool.Error("failed to marshal request: " + err.Error()), nil
	}

	raw, err := toolCtx.ControlQuerier.Custom(ctx, "platform", "post", "connectors/mcp/discover", json.RawMessage(body))
	if err != nil {
		return tool.Error("MCP discovery failed: " + err.Error()), nil
	}

	var tools []map[string]any
	if err := json.Unmarshal(raw, &tools); err != nil {
		return tool.OK(
			fmt.Sprintf("Discovered tools from %s", serverURL),
			tool.WithData(map[string]any{"raw_response": string(raw)}),
		), nil
	}

	toolNames := make([]string, 0, len(tools))
	for _, t := range tools {
		if name, ok := t["name"].(string); ok {
			toolNames = append(toolNames, name)
		}
	}

	content, _ := json.MarshalIndent(tools, "", "  ")

	return tool.OK(
		fmt.Sprintf("Discovered %d tools from %s", len(tools), serverURL),
		tool.WithData(map[string]any{
			"tool_count": len(tools),
			"tool_names": toolNames,
		}),
		tool.WithDataObjects(tool.DataObject{
			Name:        "mcp_discovered_tools.json",
			Content:     content,
			MIMEType:    "application/json",
			Disposition: tool.DispositionArtifactWithPreview,
			Preview:     discoveredToolsPreview(tools),
			Description: fmt.Sprintf("MCP tools discovered from %s", serverURL),
		}),
	), nil
}

const previewBudgetChars = 500

const previewHardCapChars = 4000

func discoveredToolsPreview(tools []map[string]any) string {
	tiers := []struct {
		withInputs, withOutputs bool
		elided                  string
	}{
		{true, true, ""},
		{false, true, "input schemas"},
		{false, false, "all field names"},
	}
	var rendered string
	for _, tier := range tiers {
		rendered = renderToolsPreview(tools, tier.withInputs, tier.withOutputs)
		if tier.elided != "" {
			rendered += fmt.Sprintf("load_artifact mcp_discovered_tools.json for %s.\n", tier.elided)
		}
		if len(rendered) <= previewBudgetChars {
			break
		}
	}
	if len(rendered) > previewHardCapChars {
		notice := fmt.Sprintf("(+%d tools not shown) load_artifact mcp_discovered_tools.json for the full list.\n",
			len(tools)-strings.Count(rendered[:previewHardCapChars], "\n"))
		rendered = rendered[:previewHardCapChars] + "\n" + notice
	}
	return rendered
}

func renderToolsPreview(tools []map[string]any, withInputs, withOutputs bool) string {
	var b strings.Builder
	for _, t := range tools {
		name, _ := t["name"].(string)
		if name == "" {
			continue
		}
		b.WriteString(name)
		if withInputs {
			fmt.Fprintf(&b, ": in(%s)", strings.Join(schemaFieldNames(t["inputSchema"]), ", "))
		}
		if withOutputs {
			if out := schemaFieldNames(t["outputSchema"]); len(out) > 0 {
				fmt.Fprintf(&b, " out(mcp_structured_content: %s)", strings.Join(out, ", "))
			} else {
				b.WriteString(" out(none declared)")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

func schemaFieldNames(schema any) []string {
	doc, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	props, ok := doc["properties"].(map[string]any)
	if !ok {
		return nil
	}
	return slices.Sorted(maps.Keys(props))
}

type CheckMCPServerAuthTool struct {
	artifactToolBase
	tool.SideEffectingTool
}

func (t *CheckMCPServerAuthTool) Name() string { return "check_mcp_server_auth" }

func (t *CheckMCPServerAuthTool) Description() string {
	return "Probes an MCP server URL to detect what authentication it requires. " +
		"Returns whether the server is reachable, whether it requires auth, and if so, " +
		"what type (OAuth 2.0 with auto-discovery support, or unknown). " +
		"Use this BEFORE generating an MCP connector config to determine the correct auth_type. " +
		"If OAuth with auto-discovery is detected, set auth_type to 'oauth' and leave the " +
		"OAuth fields empty — SAM handles discovery automatically."
}

func (t *CheckMCPServerAuthTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"server_url": map[string]any{
				"type":        "string",
				"description": "The URL of the MCP server to probe.",
			},
		},
		Required: []string{"server_url"},
	}
}

func (t *CheckMCPServerAuthTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "server_url", typ: "string", description: "The MCP server URL that was probed, echoed back."},
		resultField{name: "reachable", typ: "boolean", description: "Whether the HTTP probe got any response at all. False for an invalid URL or a transport failure."},
		resultField{name: "requires_auth", typ: "boolean", description: "Whether the server answered the probe with 401 or 403."},
		resultField{name: "auth_type", typ: "string", description: "How to authenticate: \"none\" for a server that answered 2xx/3xx, \"oauth\" for a 401/403 whose OAuth metadata was discovered, \"unknown\" otherwise. Branch on this to pick the connector's auth mode."},
		resultField{name: "status_code", typ: "integer", description: "HTTP status the probe received. Absent when the URL was invalid or the server was unreachable.", optional: true},
		resultField{name: "error", typ: "string", description: "Why the probe could not conclude: an invalid URL, a transport failure, or an unexpected status code.", optional: true},
		resultField{name: "www_authenticate", typ: "string", description: "The server's raw WWW-Authenticate header. Present only on a 401/403 that sent one.", optional: true},
		resultField{name: "oauth_discovery", typ: "object", description: "OAuth endpoints read from .well-known: authorization_endpoint, token_endpoint, registration_endpoint, has_dynamic_registration. Present only when auth_type is oauth.", optional: true},
	)
}

func (t *CheckMCPServerAuthTool) Execute(ctx context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
	serverURL, _ := args["server_url"].(string)
	if serverURL == "" {
		return tool.Error("server_url is required"), nil
	}

	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	result := map[string]any{
		"server_url":    serverURL,
		"reachable":     false,
		"requires_auth": false,
		"auth_type":     "unknown",
	}

	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, serverURL, nil)
	if err != nil {
		result["error"] = fmt.Sprintf("invalid server URL: %v", err)
		return tool.OK("Server URL is invalid", tool.WithData(result)), nil
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		result["error"] = fmt.Sprintf("server unreachable: %v", err)
		return tool.OK("Server is unreachable", tool.WithData(result)), nil
	}
	defer resp.Body.Close()        //nolint:errcheck
	io.Copy(io.Discard, resp.Body) //nolint:errcheck

	result["reachable"] = true
	result["status_code"] = resp.StatusCode

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		result["requires_auth"] = true
		if wwwAuth := resp.Header.Get("WWW-Authenticate"); wwwAuth != "" {
			result["www_authenticate"] = wwwAuth
		}

		oauthMeta := discoverOAuthWellKnown(checkCtx, serverURL, client)
		if oauthMeta != nil {
			result["auth_type"] = "oauth"
			result["oauth_discovery"] = oauthMeta
		}
	} else if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		result["requires_auth"] = false
		result["auth_type"] = "none"
	} else {
		result["error"] = fmt.Sprintf("server returned unexpected status %d", resp.StatusCode)
	}

	summary := formatAuthCheckSummary(result)
	return tool.OK(summary, tool.WithData(result)), nil
}

func discoverOAuthWellKnown(ctx context.Context, serverURL string, client *http.Client) map[string]any {
	metadataURL := strings.TrimRight(serverURL, "/") + "/.well-known/oauth-authorization-server"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		return nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil
	}

	var metadata map[string]any
	if err := json.Unmarshal(body, &metadata); err != nil {
		return nil
	}

	authEndpoint, _ := metadata["authorization_endpoint"].(string)
	tokenEndpoint, _ := metadata["token_endpoint"].(string)
	if authEndpoint == "" || tokenEndpoint == "" {
		return nil
	}

	regEndpoint, _ := metadata["registration_endpoint"].(string)

	return map[string]any{
		"authorization_endpoint":   authEndpoint,
		"token_endpoint":           tokenEndpoint,
		"registration_endpoint":    regEndpoint,
		"has_dynamic_registration": regEndpoint != "",
	}
}

func formatAuthCheckSummary(result map[string]any) string {
	reachable, _ := result["reachable"].(bool)
	if !reachable {
		return fmt.Sprintf("Server at %s is not reachable", result["server_url"])
	}

	requiresAuth, _ := result["requires_auth"].(bool)
	if !requiresAuth {
		return fmt.Sprintf("Server at %s does not require authentication (auth_type: none)", result["server_url"])
	}

	authType, _ := result["auth_type"].(string)
	if authType == "oauth" {
		oauthInfo, _ := result["oauth_discovery"].(map[string]any)
		hasDynReg, _ := oauthInfo["has_dynamic_registration"].(bool)
		if hasDynReg {
			return fmt.Sprintf("Server at %s requires OAuth 2.0 authentication with auto-discovery and dynamic client registration supported. Set auth_type to 'oauth' and leave OAuth fields empty — SAM handles this automatically.", result["server_url"])
		}
		return fmt.Sprintf("Server at %s requires OAuth 2.0 authentication (auto-discovery supported but no dynamic client registration — user may need to provide client_id/client_secret).", result["server_url"])
	}

	return fmt.Sprintf("Server at %s requires authentication but the type could not be auto-detected. Ask the user what authentication method the server uses.", result["server_url"])
}

const (
	openAPIParsePath    = "connectors/openapi/parse"
	openAPISpecArtifact = "openapi_spec.json"
)

type ParseOpenAPISpecTool struct {
	artifactToolBase
	tool.SideEffectingTool
}

func (t *ParseOpenAPISpecTool) Name() string { return "parse_openapi_spec" }

func (t *ParseOpenAPISpecTool) Description() string {
	return "Fetches an OpenAPI specification URL and returns what it requires and what it offers: " +
		"the detected security schemes with a suggested auth configuration, and the operations a connector made from it would expose, " +
		"each as toolName, operationId, method, path, summary and parameters (up to 200, sorted by toolName; " +
		"pass operations to read only specific operationIds). " +
		"Use this when building an OpenAPI connector to choose the auth_type and to read the parameter names " +
		"a workflow tool node's input: keys must use (a * marks a required parameter). " +
		"The connector stores this URL in values.specification_url, not spec_url. " +
		"A workflow tool node on a connector created from a spec URL needs the operation's operationId listed in the connector's allow_list; toolName can be a shortened form that does not match."
}

func (t *ParseOpenAPISpecTool) Parameters() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"spec_url": map[string]any{
				"type":        "string",
				"description": "The URL of the OpenAPI specification to analyze.",
			},
			"operations": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional operationIds; when set, only the matching operations are returned.",
			},
		},
		Required: []string{"spec_url"},
	}
}

func (t *ParseOpenAPISpecTool) ResultSchema() *tool.JSONSchema {
	return resultSchema(
		resultField{name: "schemes", typ: "array", description: "Security schemes detected in the spec, each {schemeName, authType, details}. Absent only when the response could not be parsed.", optional: true},
		resultField{name: "suggestedAuth", typ: "object", description: "Recommended connector auth configuration {authType, config}. Omitted when no detected scheme yields a suggestion.", optional: true},
		resultField{name: "operations", typ: "array", description: "The operations a connector made from the spec would expose, sorted by toolName and capped, each {toolName, operationId, method, path, summary}. Each operation's parameters are in the openapi_spec.json artifact, not here.", optional: true},
		resultField{name: "operationsTotal", typ: "integer", description: "Number of operations matching the operations filter (all of them when none was given), before the cap.", optional: true},
		resultField{name: "truncated", typ: "boolean", description: "True when operations holds fewer than operationsTotal.", optional: true},
		resultField{name: "note", typ: "string", description: "What a workflow tool node needs from a connector created from a specification URL: the operation's operationId listed in the connector's allow_list.", optional: true},
		resultField{name: "operationsError", typ: "string", description: "Why operations is empty although the spec was fetched and its auth schemes read: a short fixed reason. Absent when the operations were read.", optional: true},
		resultField{name: "operationsWarning", typ: "string", description: "A short fixed notice that some operations cannot back a tool and are left out of operations: today, operationIds that clash once snake-cased. Absent when there is nothing to warn of.", optional: true},
		resultField{name: "clashingOperationIds", typ: "array", description: "The operationIds that clash once snake-cased among those the operations filter selects, sorted. Absent when none clash.", optional: true},
		resultField{name: "raw_response", typ: "string", description: "Unparsed JSON text of the platform response. Present only when the response was not a JSON object, and then instead of every other field.", optional: true},
	)
}

type openAPIOperationView struct {
	ToolName    string          `json:"toolName"`
	OperationID string          `json:"operationId,omitempty"`
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	Summary     string          `json:"summary,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type openAPIParseView struct {
	Operations           []openAPIOperationView `json:"operations"`
	OperationsTotal      int                    `json:"operationsTotal"`
	Truncated            bool                   `json:"truncated"`
	Note                 string                 `json:"note"`
	OperationsError      string                 `json:"operationsError,omitempty"`
	OperationsWarning    string                 `json:"operationsWarning,omitempty"`
	ClashingOperationIDs []string               `json:"clashingOperationIds,omitempty"`
	filtered             bool
}

const noOperationsMatchedText = "0 operations matching the requested names"

func (v openAPIParseView) matchedNothing() bool {
	return v.filtered && v.OperationsTotal == 0 && v.OperationsError == "" && v.OperationsWarning == ""
}

func (v openAPIParseView) clashLine() string {
	if v.OperationsWarning == "" {
		return ""
	}
	return fmt.Sprintf("clash: %s \u2014 %s\n", strings.Join(v.ClashingOperationIDs, ", "), v.OperationsWarning)
}

func (v openAPIParseView) operationsWithoutParameters() []openAPIOperationView {
	ops := make([]openAPIOperationView, len(v.Operations))
	for i, op := range v.Operations {
		op.Parameters = nil
		ops[i] = op
	}
	return ops
}

func (t *ParseOpenAPISpecTool) Execute(ctx context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
	if toolCtx.ControlQuerier == nil {
		return tool.Error("Platform service not available. OpenAPI analysis requires the platform service to be running."), nil
	}

	specURL, _ := args["spec_url"].(string)
	if specURL == "" {
		return tool.Error("spec_url is required"), nil
	}

	request := map[string]any{"specUrl": specURL}
	if operations := stringSliceArg(args["operations"]); len(operations) > 0 {
		request["operations"] = operations
	}
	body, err := json.Marshal(request)
	if err != nil {
		return tool.Error("failed to marshal request: " + err.Error()), nil
	}

	raw, err := toolCtx.ControlQuerier.Custom(ctx, "platform", "post", openAPIParsePath, json.RawMessage(body))
	if err != nil {
		return tool.Error("OpenAPI spec analysis failed: " + err.Error()), nil
	}

	var view openAPIParseView
	var result map[string]any
	if json.Unmarshal(raw, &view) != nil || json.Unmarshal(raw, &result) != nil {
		return tool.OK(
			fmt.Sprintf("Analyzed OpenAPI spec at %s", specURL),
			tool.WithData(map[string]any{"raw_response": string(raw)}),
		), nil
	}

	content, _ := json.MarshalIndent(result, "", "  ")
	if _, ok := result["operations"]; ok {
		result["operations"] = view.operationsWithoutParameters()
	}
	schemes, _ := result["schemes"].([]any)
	view.filtered = len(stringSliceArg(args["operations"])) > 0

	return tool.OK(
		openAPIParseSummary(specURL, len(schemes), view),
		tool.WithData(result),
		tool.WithDataObjects(tool.DataObject{
			Name:        openAPISpecArtifact,
			Content:     content,
			MIMEType:    "application/json",
			Disposition: tool.DispositionArtifactWithPreview,
			Preview:     openAPIParsePreview(view),
			Description: fmt.Sprintf("Auth schemes and operations read from %s", specURL),
			Tags:        []string{a2a.ArtifactTagWorking},
		}),
	), nil
}

func openAPIParseSummary(specURL string, schemes int, view openAPIParseView) string {
	if view.OperationsError != "" {
		return fmt.Sprintf("Read OpenAPI spec at %s: %d auth scheme(s), operations could not be read", specURL, schemes)
	}
	if view.matchedNothing() {
		return fmt.Sprintf("Read OpenAPI spec at %s: %d auth scheme(s), %s", specURL, schemes, noOperationsMatchedText)
	}
	return fmt.Sprintf("Read OpenAPI spec at %s: %d auth scheme(s), %d operation(s)", specURL, schemes, view.OperationsTotal)
}

func openAPIParsePreview(view openAPIParseView) string {
	var head strings.Builder
	if view.Note != "" {
		head.WriteString("Note: " + view.Note + "\n")
	}
	if view.OperationsError != "" {
		return head.String() + fmt.Sprintf("operations: could not be read (%s)\n", view.OperationsError)
	}
	head.WriteString(view.clashLine())
	if view.Truncated {
		fmt.Fprintf(&head, "Operations: first %d of %d (the list is capped; call parse_openapi_spec again with `operations: [...]` to see specific ones)\n",
			len(view.Operations), view.OperationsTotal)
	} else if view.matchedNothing() {
		fmt.Fprintf(&head, "Operations: %s\n", noOperationsMatchedText)
	} else {
		fmt.Fprintf(&head, "Operations: %d\n", len(view.Operations))
	}

	rendered := head.String() + renderOperations(view.Operations, true)
	if previewChars(rendered) > previewBudgetChars {
		rendered = head.String() + renderOperations(view.Operations, false) +
			fmt.Sprintf("load_artifact %s for parameters.\n", openAPISpecArtifact)
	}
	return capPreview(rendered, openAPISpecArtifact)
}

func renderOperations(ops []openAPIOperationView, withParameters bool) string {
	var b strings.Builder
	for _, op := range ops {
		b.WriteString(op.ToolName)
		if op.OperationID != "" && op.OperationID != op.ToolName {
			fmt.Fprintf(&b, " [operationId: %s]", op.OperationID)
		}
		if withParameters {
			fmt.Fprintf(&b, ": in(%s)", schemaSignature(op.Parameters, true))
		}
		b.WriteString("\n")
	}
	return b.String()
}
