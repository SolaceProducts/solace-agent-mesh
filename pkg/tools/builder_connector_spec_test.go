// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/schema"
)

const (
	needsSpecPhrase     = "needs values.specification_url"
	foreignUploadPhrase = "only works on the connector it was uploaded to"
	lookupFailedPhrase  = "could not check"
	uploadedSpecPath    = "/api/v1/platform/connectors/01a12741-271c-7a25-939e-56a916f4217e/resource"
	uploadedSpecPathV2  = "/api/v2/platform/connectors/01a12741-271c-7a25-939e-56a916f4217e/resource"
	ledgerName          = "LedgerAPI"
)

func openAPIConnectorJSON(t *testing.T, extraValues map[string]any) string {
	t.Helper()
	return connectorJSON(t, "api", "openapi", map[string]any{
		"base_url":             "https://api.example-ledger.test/v1",
		"auth_type":            "apikey",
		"auth_apikey_location": "header",
		"auth_apikey_name":     "X-Api-Key",
		"auth_apikey_value":    "<<__SAM_REQUIRED__>>",
	}, extraValues)
}

func connectorJSON(t *testing.T, connectorType, subtype string, values, extraValues map[string]any) string {
	t.Helper()
	merged := map[string]any{}
	for k, v := range values {
		merged[k] = v
	}
	for k, v := range extraValues {
		merged[k] = v
	}
	body, err := json.Marshal(map[string]any{
		"name":        ledgerName,
		"description": "Ledger REST API for account balance lookups.",
		"type":        connectorType,
		"subtype":     subtype,
		"icon":        "other",
		"values":      merged,
	})
	if err != nil {
		t.Fatalf("marshal connector: %v", err)
	}
	return string(body)
}

type connectorLookupQuerier struct {
	name string
	body string
	err  error
}

func (q connectorLookupQuerier) Custom(_ context.Context, appName, method, path string, body json.RawMessage) (json.RawMessage, error) {
	var req struct {
		Name string `json:"name"`
	}
	if appName != "platform" || method != "post" || path != "connectors/lookup/byName" || json.Unmarshal(body, &req) != nil || req.Name != q.name {
		return nil, fmt.Errorf("unknown resource: %s %s %s %s", appName, method, path, body)
	}
	if q.err != nil {
		return nil, q.err
	}
	return json.RawMessage(q.body), nil
}

func ledgerLookup(exists, hasResource bool) tool.ControlQuerier {
	body, _ := json.Marshal(map[string]bool{"exists": exists, "hasResource": hasResource})
	return connectorLookupQuerier{name: ledgerName, body: string(body)}
}

func validationErrorsWithPath(data map[string]any) []string {
	errs, _ := data["errors"].([]schema.ValidationError)
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.Path + ": " + e.Message
	}
	return out
}

func specErrors(data map[string]any) []string {
	var out []string
	for _, e := range extractValidationErrors(data) {
		if strings.Contains(e, "specification_url") || strings.Contains(e, "spec_url") {
			out = append(out, e)
		}
	}
	return out
}

func TestValidateComponentConfig_OpenAPIConnectorSpecSource(t *testing.T) {
	withSpec := func(specURL string) func(t *testing.T) string {
		return func(t *testing.T) string {
			return openAPIConnectorJSON(t, map[string]any{"specification_url": specURL})
		}
	}
	noSpec := func(t *testing.T) string { return openAPIConnectorJSON(t, nil) }
	for _, tc := range []struct {
		name        string
		config      func(t *testing.T) string
		querier     tool.ControlQuerier
		wantValid   bool
		wantMessage string
		wantPath    string
	}{
		{name: "no spec source, no such connector", config: noSpec, querier: ledgerLookup(false, false), wantMessage: needsSpecPhrase, wantPath: "/values/specification_url"},
		{name: "spec_url typo", config: func(t *testing.T) string {
			return openAPIConnectorJSON(t, map[string]any{"spec_url": "https://api.example-ledger.test/openapi.json"})
		}, wantMessage: "use values.specification_url", wantPath: "/values/spec_url"},
		{name: "placeholder spec url", config: withSpec("<<__SAM_REQUIRED__>>"), wantMessage: needsSpecPhrase},
		{name: "relative spec url", config: withSpec("openapi.json"), wantMessage: needsSpecPhrase},
		{name: "scheme without host", config: withSpec("https://"), wantMessage: needsSpecPhrase},
		{name: "uppercase scheme the platform would not resolve", config: withSpec("HTTPS://api.example-ledger.test/openapi.json"), wantMessage: needsSpecPhrase},
		{name: "https spec url", config: withSpec("https://api.example-ledger.test/openapi.json"), wantValid: true},
		{name: "http spec url", config: withSpec("http://specs.internal.example/ledger.yaml"), wantValid: true},
		{name: "uploaded spec path on a new connector", config: withSpec(uploadedSpecPath), querier: ledgerLookup(false, false), wantMessage: foreignUploadPhrase},
		{name: "uploaded spec path on a connector without an upload", config: withSpec(uploadedSpecPath), querier: ledgerLookup(true, false), wantMessage: foreignUploadPhrase},
		{name: "uploaded spec path on the connector it belongs to", config: withSpec(uploadedSpecPath), querier: ledgerLookup(true, true), wantValid: true},
		{name: "v2 uploaded spec path on the connector it belongs to", config: withSpec(uploadedSpecPathV2), querier: ledgerLookup(true, true), wantValid: true},
		{name: "v2 uploaded spec path on a new connector", config: withSpec(uploadedSpecPathV2), querier: ledgerLookup(false, false), wantMessage: foreignUploadPhrase},
		{name: "edit of a connector with an uploaded spec omits the url", config: noSpec, querier: ledgerLookup(true, true), wantValid: true},
		{name: "edit of a connector without an upload omits the url", config: noSpec, querier: ledgerLookup(true, false), wantMessage: "already exists and has no spec uploaded"},
		{name: "garbage url on a connector with an upload", config: withSpec("openapi.json"), querier: ledgerLookup(true, true), wantMessage: needsSpecPhrase},
		{name: "unnamed connector", config: func(t *testing.T) string {
			var cfg map[string]any
			_ = json.Unmarshal([]byte(openAPIConnectorJSON(t, nil)), &cfg)
			cfg["name"] = "  "
			body, _ := json.Marshal(cfg)
			return string(body)
		}, querier: ledgerLookup(true, true), wantMessage: needsSpecPhrase},
		{name: "lookup error", config: noSpec, querier: connectorLookupQuerier{name: ledgerName, err: errors.New("control request failed")}, wantMessage: lookupFailedPhrase},
		{name: "malformed lookup response", config: noSpec, querier: connectorLookupQuerier{name: ledgerName, body: "not json"}, wantMessage: lookupFailedPhrase},
		{name: "lookup unavailable", config: withSpec(uploadedSpecPath), wantMessage: lookupFailedPhrase},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newMockArtifactService()
			toolCtx := newTestToolCtx(svc)
			toolCtx.ControlQuerier = tc.querier
			saveTestArtifact(t.Context(), svc, toolCtx, manifestFilename, minimalManifestYAML, "application/vnd.sam-build-manifest+yaml")

			res := validateConfigIn(t, toolCtx, tc.config(t), SAMConnectorConfigMIMEType)
			if res.Status == tool.StatusError {
				t.Fatalf("status = error: %s", res.Message)
			}
			valid, _ := res.Data["valid"].(bool)
			if valid != tc.wantValid {
				t.Fatalf("valid = %v, want %v; errors: %v", valid, tc.wantValid, extractValidationErrors(res.Data))
			}
			if tc.wantMessage != "" && !strings.Contains(strings.Join(specErrors(res.Data), "\n"), tc.wantMessage) {
				t.Errorf("errors %v do not mention %q", extractValidationErrors(res.Data), tc.wantMessage)
			}
			if tc.wantPath != "" && !slices.ContainsFunc(validationErrorsWithPath(res.Data), func(e string) bool { return strings.HasPrefix(e, tc.wantPath+": ") }) {
				t.Errorf("errors %v have no entry at %s", validationErrorsWithPath(res.Data), tc.wantPath)
			}
		})
	}
}

func TestValidateComponentConfig_NonOpenAPIConnectorNeedsNoSpec(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(t.Context(), svc, toolCtx, manifestFilename, minimalManifestYAML, "application/vnd.sam-build-manifest+yaml")

	config := connectorJSON(t, "mcp", "remote", map[string]any{
		"server_url":      "https://mcp.example.test/mcp",
		"connection_type": "streamable-http",
		"auth_type":       "none",
	}, nil)
	res := validateConfigIn(t, toolCtx, config, SAMConnectorConfigMIMEType)
	if errs := specErrors(res.Data); len(errs) > 0 {
		t.Errorf("an MCP connector was held to the OpenAPI spec rule: %v", errs)
	}
}

func TestFullValidation_OpenAPIConnectorSpecSource(t *testing.T) {
	for _, tc := range []struct {
		name        string
		origin      string
		config      func(t *testing.T) string
		querier     tool.ControlQuerier
		wantPath    string
		wantMessage string
	}{
		{name: "no spec source", origin: "session", config: func(t *testing.T) string { return openAPIConnectorJSON(t, nil) },
			querier: ledgerLookup(false, false), wantPath: "components[0].values.specification_url", wantMessage: needsSpecPhrase},
		{name: "https spec url", origin: "session", config: func(t *testing.T) string {
			return openAPIConnectorJSON(t, map[string]any{"specification_url": "https://api.example-ledger.test/openapi.json"})
		}},
		{name: "edit of a connector with an uploaded spec", origin: "session", config: func(t *testing.T) string { return openAPIConnectorJSON(t, nil) },
			querier: ledgerLookup(true, true)},
		{name: "platform connector is not ours to check", origin: "platform", config: func(t *testing.T) string { return openAPIConnectorJSON(t, nil) }},
		{name: "config is not a JSON object", origin: "session", config: func(*testing.T) string { return `{"type":"api","subtype":"openapi",}` },
			wantPath: "components[0]", wantMessage: "is not a JSON object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := "\nname: ledger-project\ncomponents:\n  - filename: ledger_api_connector.json\n    type: connector\n    name: LedgerAPI\n    origin: " + tc.origin + "\n"
			svc := newMockArtifactService()
			toolCtx := newTestToolCtx(svc)
			toolCtx.ControlQuerier = tc.querier
			ctx := t.Context()
			saveTestArtifact(ctx, svc, toolCtx, manifestFilename, manifest, "application/vnd.sam-build-manifest+yaml")
			saveTestArtifact(ctx, svc, toolCtx, "ledger_api_connector.json", tc.config(t), SAMConnectorConfigMIMEType)

			result, err := (&ValidateBuildManifestTool{}).Execute(ctx, map[string]any{"manifest_yaml": manifest, "full_validation": true}, toolCtx)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			var matched []string
			for _, e := range validationErrorsWithPath(result.Data) {
				if strings.Contains(e, "LedgerAPI") && (strings.Contains(e, "specification_url") || strings.Contains(e, "JSON object")) {
					matched = append(matched, e)
				}
			}
			if tc.wantMessage == "" {
				if len(matched) > 0 {
					t.Fatalf("unexpected connector errors: %v", matched)
				}
				return
			}
			if len(matched) == 0 || !strings.Contains(matched[0], tc.wantMessage) || !strings.HasPrefix(matched[0], tc.wantPath+":") {
				t.Fatalf("connector errors %v, want one at %s mentioning %q; all errors: %v", matched, tc.wantPath, tc.wantMessage, extractValidationErrors(result.Data))
			}
			if valid, _ := result.Data["valid"].(bool); valid {
				t.Errorf("valid = true, want false")
			}
		})
	}
}
