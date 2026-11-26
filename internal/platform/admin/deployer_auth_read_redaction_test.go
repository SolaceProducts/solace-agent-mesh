// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"
)

func TestRedactNestedSecrets_SanitizesAuthBlock(t *testing.T) {
	t.Parallel()
	vals := map[string]any{
		"auth": map[string]any{
			"credential": map[string]any{
				"client_id":     "cid-ok",
				"client_secret": "real-secret",
				"token":         "shouldNotLeak",
				"surprise":      "not-allow-listed",
			},
			"scheme": map[string]any{
				"audience":          "api.example",
				"authorization_url": "https://attacker.example/auth",
			},
		},
	}

	out := redactNestedSecrets(vals, nil)

	auth, ok := out["auth"].(map[string]any)
	if !ok {
		t.Fatalf("auth block missing after redaction: %v", out)
	}
	cred, _ := auth["credential"].(map[string]any)
	if cred["client_secret"] != redactedSentinel {
		t.Errorf("client_secret must surface as %q, got %v", redactedSentinel, cred["client_secret"])
	}
	if cred["token"] != redactedSentinel {
		t.Errorf("secret credential.token must surface as %q, got %v", redactedSentinel, cred["token"])
	}
	if _, has := cred["surprise"]; has {
		t.Errorf("non-allow-listed credential.surprise must be stripped, got %v", cred)
	}
	if cred["client_id"] != "cid-ok" {
		t.Errorf("client_id should survive, got %v", cred["client_id"])
	}
	scheme, _ := auth["scheme"].(map[string]any)
	if _, has := scheme["authorization_url"]; has {
		t.Errorf("non-allow-listed scheme.authorization_url must be stripped, got %v", scheme)
	}
	if scheme["audience"] != "api.example" {
		t.Errorf("audience should survive, got %v", scheme["audience"])
	}
}
