// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"testing"
)

func TestToolAuthState_IsAliasForMCPAuthState(t *testing.T) {
	var a ToolAuthState
	var b MCPAuthState = a //nolint:staticcheck // intentional identity assertion
	_ = b
	a.ToolType = ToolAuthTypeMCP
	if a.ToolType != ToolAuthTypeMCP {
		t.Errorf("ToolType roundtrip failed: %q", a.ToolType)
	}
}

func TestLooksLikeOpenAPICredentialKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"openapi_oauth2", true},
		{"openapi_oauth2_authorization_code", true},
		{"openapi_oauth2_client_credentials", true},
		{"openapi_serviceaccount", true},
		{"mcp-client-oauth2", false},
		{"test_mcp_oauth", false},
		{"my-custom-key", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := looksLikeOpenAPICredentialKey(tt.key); got != tt.want {
				t.Errorf("looksLikeOpenAPICredentialKey(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}
