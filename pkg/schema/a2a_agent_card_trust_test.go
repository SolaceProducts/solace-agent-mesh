// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package schema

import (
	"strings"
	"testing"
)

func TestValidateA2AAgentConfig_PinAndCardTrust(t *testing.T) {
	const base = `"name": "SignedProxy", "description": "d", "agentUrl": "https://a.example.com", "agentAuthConfig": { "type": "none" }`
	tests := []struct {
		name  string
		extra string
		valid bool
	}{
		{"pinned with trusted keys and require", `"agentCardLocation": "well_known", "protocolVersion": "v0_3", "trustedCardJwksUrls": ["https://a.example.com/jwks.json"], "requireSignedCard": true`, true},
		{"loopback http key set", `"agentCardLocation": "well_known", "trustedCardJwksUrls": ["http://localhost:8080/jwks.json"]`, true},
		{"require off without keys", `"agentCardLocation": "well_known", "requireSignedCard": false`, true},
		{"unknown pin", `"agentCardLocation": "well_known", "protocolVersion": "1.0"`, false},
		{"plain http key set", `"agentCardLocation": "well_known", "trustedCardJwksUrls": ["http://a.example.com/jwks.json"]`, false},
		{"require without keys", `"agentCardLocation": "well_known", "requireSignedCard": true`, false},
		{"require with an empty key list", `"agentCardLocation": "well_known", "requireSignedCard": true, "trustedCardJwksUrls": []`, false},
		{"card trust without a fetched card", `"agentCardLocation": "none", "skills": [{"name": "s", "description": "d"}], "trustedCardJwksUrls": ["https://a.example.com/jwks.json"]`, false},
	}
	for _, tt := range tests {
		r, err := ValidateA2AAgentConfig([]byte("{" + base + ", " + tt.extra + "}"))
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if r.Valid != tt.valid {
			t.Errorf("%s: valid = %v, want %v (errors %s)", tt.name, r.Valid, tt.valid, strings.Join(errorMessages(r), "; "))
		}
	}
}

func errorMessages(r *ValidationResult) []string {
	msgs := make([]string, 0, len(r.Errors))
	for _, e := range r.Errors {
		msgs = append(msgs, e.Message)
	}
	return msgs
}
