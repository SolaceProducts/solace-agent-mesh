// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"maps"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func mcpFormDefaults(t *testing.T) map[string]any {
	t.Helper()
	svc := newEventMeshGatewayService(t)
	for _, s := range svc.GetSchemas() {
		if s.Type != "mcp" {
			continue
		}
		fields, ok := s.Schema.(map[string]any)
		if !ok {
			t.Fatalf("mcp schema not a map: %T", s.Schema)
		}
		values := map[string]any{}
		for name, raw := range fields {
			field, ok := raw.(map[string]any)
			if !ok {
				t.Fatalf("mcp field %s not a map: %T", name, raw)
			}
			if def, ok := field["default"]; ok {
				values[name] = def
			}
		}
		return values
	}
	t.Fatal("mcp schema not found")
	return nil
}

func TestMCPFormDefaults_SaveWithoutWebUIBaseURL(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "")
	values := mcpFormDefaults(t)

	if values["enableAuth"] != "true" {
		t.Fatalf("enableAuth default = %v, want \"true\"", values["enableAuth"])
	}
	if err := validateMCPGatewayValues(values); err != nil {
		t.Fatalf("the form's defaults must save when no dispatcher URL is derivable: %v", err)
	}
	if values["stateRedirectionEnabled"] != "false" {
		t.Errorf("stateRedirectionEnabled default = %v, want \"false\" without a dispatcher URL", values["stateRedirectionEnabled"])
	}
}

func servedValues(t *testing.T, gatewayType, stored string) map[string]any {
	t.Helper()
	gw := &store.Gateway{ID: "gw-1", Name: "gw", Type: gatewayType, Values: &stored, CreatedBy: "u", UpdatedBy: "u"}
	resp := (&GatewayService{}).buildResponse(gw, DisplayStatusNotDeployed, nil)
	served, ok := resp.Values.(map[string]any)
	if !ok {
		t.Fatalf("Values = %T, want map[string]any", resp.Values)
	}
	return served
}

func storedMCPValuesAsEditFormSees(t *testing.T, stored string) map[string]any {
	t.Helper()
	formValues := map[string]any{}
	maps.Copy(formValues, mcpFormDefaults(t))
	maps.Copy(formValues, servedValues(t, "mcp", stored))
	return formValues
}

func TestMCPEditRoundTrip_OwnDispatcherURLKeepsStateRedirectionOn(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "")
	form := storedMCPValuesAsEditFormSees(t, `{"enableAuth":"true","oauthDispatcherUrl":"https://x/api/v1/auth/callback"}`)

	if got, _ := mcpBoolValue(form, "stateRedirectionEnabled"); !got {
		t.Fatalf("edit form would post stateRedirectionEnabled=%v, want it to stay on for an entrypoint with its own dispatcher URL", form["stateRedirectionEnabled"])
	}
	if err := validateMCPGatewayValues(form); err != nil {
		t.Fatalf("edit round-trip must validate: %v", err)
	}
}

func TestMCPEditRoundTrip_NoDispatcherURLStaysOff(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "")
	form := storedMCPValuesAsEditFormSees(t, `{"enableAuth":"true"}`)

	if got, _ := mcpBoolValue(form, "stateRedirectionEnabled"); got {
		t.Fatalf("stateRedirectionEnabled = %v, want off with no dispatcher URL", form["stateRedirectionEnabled"])
	}
}

func TestMCPEditRoundTrip_ExplicitValueAndOtherShapesUntouched(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "")
	explicit := storedMCPValuesAsEditFormSees(t, `{"enableAuth":"true","oauthDispatcherUrl":"https://x/cb","stateRedirectionEnabled":"false"}`)
	if explicit["stateRedirectionEnabled"] != "false" {
		t.Errorf("explicit false = %v, want it preserved", explicit["stateRedirectionEnabled"])
	}
	authOff := storedMCPValuesAsEditFormSees(t, `{"enableAuth":"false","oauthDispatcherUrl":"https://x/cb"}`)
	if authOff["stateRedirectionEnabled"] != "false" {
		t.Errorf("auth-off entrypoint = %v, want the relaxed default", authOff["stateRedirectionEnabled"])
	}

	if _, present := servedValues(t, "webhook", `{"enableAuth":"true","oauthDispatcherUrl":"https://x/cb"}`)["stateRedirectionEnabled"]; present {
		t.Error("a non-mcp type must not be stamped")
	}
}

func TestMCPEditRoundTrip_BaseURLSetNeedsNoStamp(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	if _, present := servedValues(t, "mcp", `{"enableAuth":"true","oauthDispatcherUrl":"https://x/cb"}`)["stateRedirectionEnabled"]; present {
		t.Error("with a base URL the schema default already applies; nothing to stamp")
	}
}

func TestStripDerivedGatewayValues_DropsTheStampedMCPStateRedirection(t *testing.T) {
	cases := []struct {
		name     string
		gwType   string
		values   map[string]any
		wantKept bool
	}{
		{"stamp beside its own dispatcher URL", "mcp", map[string]any{"enableAuth": "true", "oauthDispatcherUrl": "https://x/cb", "stateRedirectionEnabled": "true"}, false},
		{"native bool beside its own dispatcher URL", "mcp", map[string]any{"enableAuth": true, "oauthDispatcherUrl": "https://x/cb", "stateRedirectionEnabled": true}, false},
		{"explicit false is authored", "mcp", map[string]any{"enableAuth": "true", "oauthDispatcherUrl": "https://x/cb", "stateRedirectionEnabled": "false"}, true},
		{"true without a dispatcher URL is authored", "mcp", map[string]any{"enableAuth": "true", "stateRedirectionEnabled": "true"}, true},
		{"auth off is left alone", "mcp", map[string]any{"enableAuth": "false", "oauthDispatcherUrl": "https://x/cb", "stateRedirectionEnabled": "true"}, true},
		{"other types are left alone", "webhook", map[string]any{"enableAuth": "true", "oauthDispatcherUrl": "https://x/cb", "stateRedirectionEnabled": "true"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			StripDerivedGatewayValues(tc.gwType, tc.values)
			if _, kept := tc.values["stateRedirectionEnabled"]; kept != tc.wantKept {
				t.Errorf("stateRedirectionEnabled kept = %v, want %v (values %v)", kept, tc.wantKept, tc.values)
			}
		})
	}
}

func TestMCPFormDefaults_StateRedirectionStaysOnWithWebUIBaseURL(t *testing.T) {
	t.Setenv("WEBUI_FRONTEND_SERVER_URL", "https://sam.example.com")
	values := mcpFormDefaults(t)

	if values["stateRedirectionEnabled"] != "true" {
		t.Errorf("stateRedirectionEnabled default = %v, want \"true\" when the dispatcher URL is derivable", values["stateRedirectionEnabled"])
	}
	if err := validateMCPGatewayValues(values); err != nil {
		t.Errorf("the form's defaults must save when a dispatcher URL is derivable: %v", err)
	}
}
