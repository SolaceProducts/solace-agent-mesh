// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import "testing"

func TestSDKDeclaredAuth_CarriesTLSKnobs(t *testing.T) {
	cfg, err := ParseToolConfig(map[string]any{
		"tool_type": "builtin",
		"tool_name": "probe_alpha",
		"auth": map[string]any{
			"type":                 "oauth2",
			"insecure_skip_verify": true,
			"ca_cert_path":         "/tmp/keycloak-ca.pem",
			"credential": map[string]any{
				"client_id":     "test-client",
				"client_secret": "test-secret",
			},
		},
	})
	if err != nil {
		t.Fatalf("ParseToolConfig: %v", err)
	}

	rt := NewTool(cfg, nil)
	rt.initMu.Lock()
	rt.applyInitAuthLocked(sdkWireAuth())
	rt.initMu.Unlock()

	ah := rt.AuthHandler()
	if ah == nil {
		t.Fatal("no auth handler after init")
	}
	got := ah.Config()
	if !got.InsecureSkipVerify {
		t.Error("insecure_skip_verify dropped by the SDK-declared merge: the handler would verify the self-signed IdP cert and the token exchange fails x509")
	}
	if got.CACertPath != "/tmp/keycloak-ca.pem" {
		t.Errorf("ca_cert_path dropped by the SDK-declared merge: got %q, want %q", got.CACertPath, "/tmp/keycloak-ca.pem")
	}
}
