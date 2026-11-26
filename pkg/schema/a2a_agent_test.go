// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import "testing"

func TestValidateA2AAgentConfig_ValidWellKnown(t *testing.T) {
	json := `{
		"name": "WeatherProxy",
		"description": "Proxies an external weather agent.",
		"agentUrl": "http://ec2-1-2-3-4.compute.amazonaws.com:9000",
		"agentCardLocation": "well_known",
		"agentAuthConfig": { "type": "bearer", "token": "<<__SAM_REQUIRED__>>" }
	}`
	r, err := ValidateA2AAgentConfig([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Valid {
		t.Errorf("expected valid, got errors: %v", r.Errors)
	}
	if r.ComponentType != "a2a_agent" {
		t.Errorf("expected component type a2a_agent, got %q", r.ComponentType)
	}
}

func TestValidateA2AAgentConfig_CustomRequiresCardURL(t *testing.T) {
	missing := `{
		"name": "ForecastProxy",
		"agentUrl": "https://forecast.example.com",
		"agentCardLocation": "custom",
		"agentAuthConfig": { "type": "none" }
	}`
	r, err := ValidateA2AAgentConfig([]byte(missing))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Error("expected invalid: custom card location without agentCardUrl")
	}

	ok := `{
		"name": "ForecastProxy",
		"agentUrl": "https://forecast.example.com",
		"agentCardLocation": "custom",
		"agentCardUrl": "https://forecast.example.com/a2a/card",
		"agentAuthConfig": { "type": "none" }
	}`
	r2, err := ValidateA2AAgentConfig([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Valid {
		t.Errorf("expected valid, got errors: %v", r2.Errors)
	}
}

func TestValidateA2AAgentConfig_NoneRequiresSkills(t *testing.T) {
	missing := `{
		"name": "LegacyProxy",
		"agentUrl": "http://legacy.internal:9000",
		"agentCardLocation": "none",
		"agentAuthConfig": { "type": "none" }
	}`
	r, err := ValidateA2AAgentConfig([]byte(missing))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Error("expected invalid: card location 'none' without inline skills")
	}

	ok := `{
		"name": "LegacyProxy",
		"agentUrl": "http://legacy.internal:9000",
		"agentCardLocation": "none",
		"agentAuthConfig": { "type": "none" },
		"skills": [ { "name": "Summarize", "description": "Summarizes documents." } ]
	}`
	r2, err := ValidateA2AAgentConfig([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Valid {
		t.Errorf("expected valid, got errors: %v", r2.Errors)
	}
}

func TestValidateA2AAgentConfig_MissingRequiredFields(t *testing.T) {
	json := `{ "agentCardLocation": "well_known" }`
	r, err := ValidateA2AAgentConfig([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Error("expected invalid for missing required fields")
	}
	if len(r.Errors) == 0 {
		t.Error("expected validation errors")
	}
}

func TestValidateA2AAgentConfig_InvalidAuthType(t *testing.T) {
	json := `{
		"name": "BadAuthProxy",
		"agentUrl": "https://example.com",
		"agentCardLocation": "well_known",
		"agentAuthConfig": { "type": "kerberos" }
	}`
	r, err := ValidateA2AAgentConfig([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Error("expected invalid auth type to fail")
	}
}

func TestValidateA2AAgentConfig_AuthTypeRequiresSecret(t *testing.T) {
	bad := `{
		"name": "NoTokenProxy",
		"agentUrl": "https://example.com",
		"agentCardLocation": "well_known",
		"agentAuthConfig": { "type": "bearer" }
	}`
	r, err := ValidateA2AAgentConfig([]byte(bad))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Error("expected invalid: bearer auth without a token")
	}

	ok := `{
		"name": "TokenProxy",
		"agentUrl": "https://example.com",
		"agentCardLocation": "well_known",
		"agentAuthConfig": { "type": "bearer", "token": "<<__SAM_REQUIRED__>>" }
	}`
	r2, err := ValidateA2AAgentConfig([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Valid {
		t.Errorf("expected valid with placeholder token, got errors: %v", r2.Errors)
	}
}

func TestValidateA2AAgentConfig_BadURLScheme(t *testing.T) {
	json := `{
		"name": "NoSchemeProxy",
		"agentUrl": "example.com:9000",
		"agentCardLocation": "well_known",
		"agentAuthConfig": { "type": "none" }
	}`
	r, err := ValidateA2AAgentConfig([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Error("expected invalid: agentUrl missing http(s) scheme")
	}
}

func TestValidateA2AAgentConfig_RejectsUnknownField(t *testing.T) {
	json := `{
		"name": "ExtraProxy",
		"agentUrl": "https://example.com",
		"agentCardLocation": "well_known",
		"agentAuthConfig": { "type": "none" },
		"retryPolicy": "aggressive"
	}`
	r, err := ValidateA2AAgentConfig([]byte(json))
	if err != nil {
		t.Fatal(err)
	}
	if r.Valid {
		t.Error("expected invalid: unknown top-level field rejected by additionalProperties:false")
	}
}
