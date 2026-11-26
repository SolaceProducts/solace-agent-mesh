// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestValidateDeployerAuthBlock_AllowedShape(t *testing.T) {
	val := map[string]any{
		"credential": map[string]any{"client_id": "cid-123"},
		"scheme": map[string]any{
			"audience":                   "api.atlassian.com",
			"refresh_url":                "https://auth.atlassian.com/oauth/token",
			"token_endpoint_auth_method": "none",
		},
	}
	if d := validateDeployerAuthBlock(val); len(d) != 0 {
		t.Fatalf("expected no validation errors, got %v", d)
	}
}

func TestValidateDeployerAuthBlock_NilIsValid(t *testing.T) {
	if d := validateDeployerAuthBlock(nil); len(d) != 0 {
		t.Fatalf("nil should be valid, got %v", d)
	}
}

func TestValidateDeployerAuthBlock_AllowsClientSecret(t *testing.T) {
	val := map[string]any{
		"credential": map[string]any{
			"client_id":     "cid",
			"client_secret": "shhh",
		},
	}
	if d := validateDeployerAuthBlock(val); len(d) != 0 {
		t.Fatalf("client_id + client_secret should be allowed, got %v", d)
	}
}

func TestValidateDeployerAuthBlock_RejectsUnknownCredentialKeys(t *testing.T) {
	val := map[string]any{
		"credential": map[string]any{
			"client_id": "cid",
			"surprise":  "leaked",
		},
	}
	d := validateDeployerAuthBlock(val)
	if _, ok := d["auth.credential.surprise"]; !ok {
		t.Errorf("expected violation at auth.credential.surprise, got %v", d)
	}
	if _, ok := d["auth.credential.client_id"]; ok {
		t.Errorf("client_id should be allowed, got error: %v", d["auth.credential.client_id"])
	}
}

func TestValidateDeployerAuthBlock_AllowsStaticCredentialKeys(t *testing.T) {
	val := map[string]any{
		"credential": map[string]any{
			"username": "svc@example.com",
			"password": "api-token",
			"token":    "bearer-token",
		},
	}
	if d := validateDeployerAuthBlock(val); len(d) != 0 {
		t.Fatalf("username/password/token should be allowed, got %v", d)
	}
}

func TestValidateDeployerAuthBlock_AllowsDisablePKCE(t *testing.T) {
	val := map[string]any{
		"scheme": map[string]any{
			"audience":     "api.atlassian.com",
			"disable_pkce": true,
		},
	}
	if d := validateDeployerAuthBlock(val); len(d) != 0 {
		t.Fatalf("disable_pkce=true should be allowed, got %v", d)
	}
}

func TestValidateDeployerAuthBlock_DisablePKCEMustBeBool(t *testing.T) {
	val := map[string]any{
		"scheme": map[string]any{
			"disable_pkce": "yes",
		},
	}
	d := validateDeployerAuthBlock(val)
	if _, ok := d["auth.scheme.disable_pkce"]; !ok {
		t.Fatalf("expected type-error at auth.scheme.disable_pkce, got %v", d)
	}
}

func TestValidateDeployerAuthBlock_RejectsSDKOwnedSchemeFields(t *testing.T) {
	val := map[string]any{
		"scheme": map[string]any{
			"authorization_url": "https://attacker.example/auth",
			"token_url":         "https://attacker.example/token",
			"scopes":            []any{"narrow"},
			"audience":          "api.atlassian.com",
			"resource":          "https://mcp.example.com/mcp",
		},
	}
	d := validateDeployerAuthBlock(val)
	for _, path := range []string{"auth.scheme.authorization_url", "auth.scheme.token_url", "auth.scheme.scopes"} {
		if _, ok := d[path]; !ok {
			t.Errorf("expected violation at %q, got %v", path, d)
		}
	}
	if _, ok := d["auth.scheme.audience"]; ok {
		t.Errorf("audience should be allowed, got error: %v", d["auth.scheme.audience"])
	}
	if _, ok := d["auth.scheme.resource"]; ok {
		t.Errorf("resource should be allowed (RFC 8707 deployer override), got error: %v", d["auth.scheme.resource"])
	}
}

func TestValidateDeployerAuthBlock_AcceptsValidType(t *testing.T) {
	for _, ty := range []string{"oauth2", "basic", "bearer"} {
		if d := validateDeployerAuthBlock(map[string]any{"type": ty}); len(d) != 0 {
			t.Errorf("type %q should be accepted, got %v", ty, d)
		}
	}
}

func TestValidateDeployerAuthBlock_RejectsUnsupportedType(t *testing.T) {
	d := validateDeployerAuthBlock(map[string]any{"type": "mtls"})
	if _, ok := d["auth.type"]; !ok {
		t.Fatalf("expected violation at auth.type for unsupported type, got %v", d)
	}
}

func TestValidateDeployerAuthBlock_RejectsNonStringType(t *testing.T) {
	d := validateDeployerAuthBlock(map[string]any{"type": 42})
	if _, ok := d["auth.type"]; !ok {
		t.Fatalf("expected type-error at auth.type, got %v", d)
	}
}

func TestValidateDeployerAuthBlock_NilTypeIsValid(t *testing.T) {
	if d := validateDeployerAuthBlock(map[string]any{"type": nil}); len(d) != 0 {
		t.Fatalf("nil type should be tolerated, got %v", d)
	}
}

func TestValidateDeployerAuthBlock_RejectsNonStringValues(t *testing.T) {
	val := map[string]any{
		"credential": map[string]any{"client_id": 42},
		"scheme":     map[string]any{"audience": []any{"x"}},
	}
	d := validateDeployerAuthBlock(val)
	if _, ok := d["auth.credential.client_id"]; !ok {
		t.Errorf("expected type error at auth.credential.client_id, got %v", d)
	}
	if _, ok := d["auth.scheme.audience"]; !ok {
		t.Errorf("expected type error at auth.scheme.audience, got %v", d)
	}
}

func TestRejectReservedConfigKey(t *testing.T) {
	good := marshalJSON(t, []map[string]any{{"key": "base_url", "type": "string"}})
	if msg := rejectReservedConfigKey(&good); msg != "" {
		t.Errorf("good schema should pass, got %q", msg)
	}
	bad := marshalJSON(t, []map[string]any{{"key": "auth", "type": "string"}})
	if msg := rejectReservedConfigKey(&bad); msg == "" {
		t.Errorf("schema declaring auth should be rejected")
	}
	if msg := rejectReservedConfigKey(nil); msg != "" {
		t.Errorf("nil schema should pass, got %q", msg)
	}
	empty := ""
	if msg := rejectReservedConfigKey(&empty); msg != "" {
		t.Errorf("empty schema should pass, got %q", msg)
	}
	garbage := "{not json"
	if msg := rejectReservedConfigKey(&garbage); msg != "" {
		t.Errorf("invalid JSON should fall through (other validators surface that), got %q", msg)
	}
}

func TestBuildToolAuthBlock_DeployerCredentialMerged(t *testing.T) {
	authJSON := marshalJSON(t, remotetool.AuthDeclaration{
		Type: "oauth2",
		Scheme: &remotetool.AuthDeclarationScheme{
			AuthorizationURL: "https://auth.example/authorize",
			TokenURL:         "https://auth.example/token",
			Scopes:           []string{"read"},
		},
	})
	f := store.ToolPackageFile{Auth: &authJSON}
	deployer := map[string]any{
		"credential": map[string]any{"client_id": "cid-xyz"},
		"scheme":     map[string]any{"audience": "api.example.com"},
	}

	block := buildToolAuthBlock(f, deployer)
	if block == nil {
		t.Fatal("expected non-nil auth block")
	}
	cred, _ := block["credential"].(map[string]any)
	if cred["client_id"] != "cid-xyz" {
		t.Errorf("client_id should round-trip from deployer, got %v", cred)
	}
	scheme, _ := block["scheme"].(map[string]any)
	if scheme["audience"] != "api.example.com" {
		t.Errorf("audience missing from merged scheme: %v", scheme)
	}
	if scheme["authorization_url"] != "https://auth.example/authorize" {
		t.Errorf("SDK-declared authorization_url lost: %v", scheme)
	}
}

func TestBuildToolAuthBlock_NoSDKAuthIgnoresDeployer(t *testing.T) {
	f := store.ToolPackageFile{Auth: nil}
	deployer := map[string]any{"credential": map[string]any{"client_id": "cid"}}
	if block := buildToolAuthBlock(f, deployer); block != nil {
		t.Errorf("deployer auth without SDK declaration must not emit a block, got %v", block)
	}
}

func TestBuildToolAuthBlock_PassesDisablePKCEToRuntime(t *testing.T) {
	authJSON := marshalJSON(t, remotetool.AuthDeclaration{Type: "oauth2"})
	f := store.ToolPackageFile{Auth: &authJSON}
	deployer := map[string]any{
		"scheme": map[string]any{
			"audience":     "api.atlassian.com",
			"disable_pkce": true,
		},
	}

	block := buildToolAuthBlock(f, deployer)
	scheme, _ := block["scheme"].(map[string]any)
	if got, ok := scheme["disable_pkce"].(bool); !ok || !got {
		t.Errorf("disable_pkce must reach runtime YAML as bool true, got %T %v", scheme["disable_pkce"], scheme["disable_pkce"])
	}
	if scheme["audience"] != "api.atlassian.com" {
		t.Errorf("audience must still reach runtime YAML, got %v", scheme["audience"])
	}
}

func TestBuildToolAuthBlock_PassesClientSecretToRuntime(t *testing.T) {
	authJSON := marshalJSON(t, remotetool.AuthDeclaration{Type: "oauth2"})
	f := store.ToolPackageFile{Auth: &authJSON}
	deployer := map[string]any{
		"credential": map[string]any{"client_id": "cid", "client_secret": "real-secret"},
		"scheme":     map[string]any{"audience": "good"},
	}

	block := buildToolAuthBlock(f, deployer)
	cred, _ := block["credential"].(map[string]any)
	if cred["client_secret"] != "real-secret" {
		t.Errorf("client_secret must reach runtime YAML, got %v", cred)
	}
	if cred["client_id"] != "cid" {
		t.Errorf("client_id must reach runtime YAML, got %v", cred)
	}
}

func TestBuildToolAuthBlock_DeployerTypeOverridesSDK(t *testing.T) {
	authJSON := marshalJSON(t, remotetool.AuthDeclaration{
		Type: "oauth2",
		Scheme: &remotetool.AuthDeclarationScheme{
			AuthorizationURL: "https://auth.example/authorize",
			TokenURL:         "https://auth.example/token",
		},
	})
	f := store.ToolPackageFile{Auth: &authJSON}
	deployer := map[string]any{
		"type":       "basic",
		"credential": map[string]any{"username": "svc@example.com", "password": "api-token"},
	}

	block := buildToolAuthBlock(f, deployer)
	if block["type"] != "basic" {
		t.Errorf("deployer type must override SDK type, got %v", block["type"])
	}
	if _, has := block["scheme"]; has {
		t.Errorf("stale oauth2 scheme must not be emitted when type is basic, got %v", block["scheme"])
	}
	cred, _ := block["credential"].(map[string]any)
	if cred["username"] != "svc@example.com" || cred["password"] != "api-token" {
		t.Errorf("basic credential must reach runtime YAML, got %v", cred)
	}
}

func TestBuildToolAuthBlock_FiltersDisallowedDeployerFields(t *testing.T) {
	authJSON := marshalJSON(t, remotetool.AuthDeclaration{Type: "oauth2"})
	f := store.ToolPackageFile{Auth: &authJSON}
	deployer := map[string]any{
		"credential": map[string]any{"client_id": "cid", "surprise": "shouldNotLeak"},
		"scheme":     map[string]any{"audience": "good", "authorization_url": "https://evil/auth"},
	}

	block := buildToolAuthBlock(f, deployer)
	cred, _ := block["credential"].(map[string]any)
	if _, has := cred["surprise"]; has {
		t.Errorf("disallowed credential.surprise must be filtered out, got %v", cred)
	}
	scheme, _ := block["scheme"].(map[string]any)
	if scheme["authorization_url"] != nil {
		t.Errorf("deployer authorization_url must not overwrite SDK scheme, got %v", scheme)
	}
	if scheme["audience"] != "good" {
		t.Errorf("allow-listed audience lost: %v", scheme)
	}
}

func TestValidateDeployerAuthBlock_TopLevelTypeMismatch(t *testing.T) {
	d := validateDeployerAuthBlock("not-an-object")
	if _, ok := d["auth"]; !ok {
		t.Fatalf("expected violation at \"auth\", got %v", d)
	}
}

func TestValidateDeployerAuthBlock_RejectsUnknownTopLevelKey(t *testing.T) {
	d := validateDeployerAuthBlock(map[string]any{"surprise": "x"})
	if _, ok := d["auth.surprise"]; !ok {
		t.Fatalf("expected violation at \"auth.surprise\", got %v", d)
	}
}

func TestValidateDeployerAuthBlock_NestedLeafNotAMap(t *testing.T) {
	d := validateDeployerAuthBlock(map[string]any{
		"credential": "string-instead-of-object",
		"scheme":     42,
	})
	if _, ok := d["auth.credential"]; !ok {
		t.Errorf("expected type violation at auth.credential, got %v", d)
	}
	if _, ok := d["auth.scheme"]; !ok {
		t.Errorf("expected type violation at auth.scheme, got %v", d)
	}
}

func TestValidateDeployerAuthBlock_NilNestedValueIsValid(t *testing.T) {
	d := validateDeployerAuthBlock(map[string]any{
		"credential": nil,
		"scheme":     nil,
	})
	if len(d) != 0 {
		t.Errorf("nil leaf maps should be valid, got %v", d)
	}
}

func TestValidateDeployerAuthBlock_NilLeafValueIsValid(t *testing.T) {
	d := validateDeployerAuthBlock(map[string]any{
		"credential": map[string]any{"client_id": nil},
		"scheme":     map[string]any{"audience": nil},
	})
	if len(d) != 0 {
		t.Errorf("nil leaf values should be tolerated, got %v", d)
	}
}

func TestRejectReservedConfigKey_AcceptsMalformedJSON(t *testing.T) {
	garbage := "{not-json"
	if msg := rejectReservedConfigKey(&garbage); msg != "" {
		t.Errorf("malformed JSON should fall through, got %q", msg)
	}
}

func TestSanitizeDeployerAuthValue(t *testing.T) {
	t.Run("strips disallowed fields, redacts secret leaves, keeps allow-listed non-secrets", func(t *testing.T) {
		tainted := map[string]any{
			"credential": map[string]any{
				"client_id":     "cid-ok",
				"client_secret": "real-secret-do-not-leak",
				"token":         "secret-token",
				"surprise":      "leaked",
			},
			"scheme": map[string]any{
				"authorization_url": "https://attacker.example/auth",
				"token_url":         "https://attacker.example/token",
				"audience":          "api.example",
				"refresh_url":       "https://auth.example/refresh",
			},
			"type":         "bearer",
			"unknown_root": "x",
		}
		out := sanitizeDeployerAuthValue(tainted)
		cred, _ := out["credential"].(map[string]any)
		if cred["client_id"] != "cid-ok" {
			t.Errorf("client_id should survive verbatim, got %v", cred)
		}
		if cred["client_secret"] != redactedSentinel {
			t.Errorf("client_secret must surface as %q, got %v", redactedSentinel, cred)
		}
		if cred["token"] != redactedSentinel {
			t.Errorf("secret credential.token must surface as %q, got %v", redactedSentinel, cred)
		}
		if _, has := cred["surprise"]; has {
			t.Errorf("disallowed credential.surprise must be stripped, got %v", cred)
		}
		scheme, _ := out["scheme"].(map[string]any)
		if scheme["audience"] != "api.example" || scheme["refresh_url"] != "https://auth.example/refresh" {
			t.Errorf("allow-listed scheme fields lost, got %v", scheme)
		}
		for _, forbidden := range []string{"authorization_url", "token_url"} {
			if _, has := scheme[forbidden]; has {
				t.Errorf("scheme.%s must be stripped, got %v", forbidden, scheme)
			}
		}
		if out["type"] != "bearer" {
			t.Errorf("a valid deployer-selected type must round-trip, got %v", out["type"])
		}
		if _, has := out["unknown_root"]; has {
			t.Errorf("unknown top-level keys must be stripped, got %v", out)
		}
	})

	t.Run("non-map input yields nil", func(t *testing.T) {
		if got := sanitizeDeployerAuthValue("string"); got != nil {
			t.Errorf("string input should yield nil, got %v", got)
		}
		if got := sanitizeDeployerAuthValue(nil); got != nil {
			t.Errorf("nil input should yield nil, got %v", got)
		}
	})

	t.Run("empty after filtering yields nil", func(t *testing.T) {
		onlyBad := map[string]any{
			"credential": map[string]any{"surprise": "x"},
			"scheme":     map[string]any{"authorization_url": "x"},
		}
		if got := sanitizeDeployerAuthValue(onlyBad); got != nil {
			t.Errorf("auth block with no allow-listed fields should yield nil, got %v", got)
		}
	})
}

func TestMergeDeployerAuthBlock(t *testing.T) {
	t.Run("redacted sentinel preserves existing secret", func(t *testing.T) {
		newAuth := map[string]any{
			"credential": map[string]any{
				"client_id":     "cid-new",
				"client_secret": redactedSentinel,
			},
		}
		existing := map[string]any{
			"credential": map[string]any{
				"client_id":     "cid-old",
				"client_secret": "stored-secret",
			},
		}
		got := mergeDeployerAuthBlock(newAuth, existing)
		cred := got.(map[string]any)["credential"].(map[string]any)
		if cred["client_secret"] != "stored-secret" {
			t.Errorf("expected stored secret preserved, got %v", cred["client_secret"])
		}
		if cred["client_id"] != "cid-new" {
			t.Errorf("non-secret client_id should be overwritten with new value, got %v", cred["client_id"])
		}
	})

	t.Run("null preserves existing secret", func(t *testing.T) {
		newAuth := map[string]any{"credential": map[string]any{"client_secret": nil}}
		existing := map[string]any{"credential": map[string]any{"client_secret": "stored"}}
		got := mergeDeployerAuthBlock(newAuth, existing)
		cred := got.(map[string]any)["credential"].(map[string]any)
		if cred["client_secret"] != "stored" {
			t.Errorf("nil should preserve existing, got %v", cred["client_secret"])
		}
	})

	t.Run("non-sentinel value updates", func(t *testing.T) {
		newAuth := map[string]any{"credential": map[string]any{"client_secret": "rotated"}}
		existing := map[string]any{"credential": map[string]any{"client_secret": "stored"}}
		got := mergeDeployerAuthBlock(newAuth, existing)
		cred := got.(map[string]any)["credential"].(map[string]any)
		if cred["client_secret"] != "rotated" {
			t.Errorf("expected update to new value, got %v", cred["client_secret"])
		}
	})

	t.Run("empty string clears", func(t *testing.T) {
		newAuth := map[string]any{"credential": map[string]any{"client_id": "cid", "client_secret": ""}}
		existing := map[string]any{"credential": map[string]any{"client_secret": "stored"}}
		got := mergeDeployerAuthBlock(newAuth, existing)
		cred := got.(map[string]any)["credential"].(map[string]any)
		if _, has := cred["client_secret"]; has {
			t.Errorf("empty string should clear the secret, got %v", cred)
		}
	})

	t.Run("no existing + redacted sentinel drops the field", func(t *testing.T) {
		newAuth := map[string]any{"credential": map[string]any{"client_secret": redactedSentinel}}
		got := mergeDeployerAuthBlock(newAuth, nil)
		credAny, ok := got.(map[string]any)["credential"]
		if !ok {
			return
		}
		cred, _ := credAny.(map[string]any)
		if _, has := cred["client_secret"]; has {
			t.Errorf("redacted-sentinel against no-existing must not persist sentinel literal, got %v", cred)
		}
	})

	t.Run("password sentinel preserves existing basic secret", func(t *testing.T) {
		newAuth := map[string]any{
			"type": "basic",
			"credential": map[string]any{
				"username": "svc@example.com",
				"password": redactedSentinel,
			},
		}
		existing := map[string]any{
			"type":       "basic",
			"credential": map[string]any{"username": "old@example.com", "password": "stored-pass"},
		}
		got := mergeDeployerAuthBlock(newAuth, existing)
		cred := got.(map[string]any)["credential"].(map[string]any)
		if cred["password"] != "stored-pass" {
			t.Errorf("redacted password should preserve stored secret, got %v", cred["password"])
		}
		if cred["username"] != "svc@example.com" {
			t.Errorf("non-secret username should update, got %v", cred["username"])
		}
	})

	t.Run("token non-sentinel updates bearer secret", func(t *testing.T) {
		newAuth := map[string]any{"type": "bearer", "credential": map[string]any{"token": "rotated"}}
		existing := map[string]any{"type": "bearer", "credential": map[string]any{"token": "stored"}}
		got := mergeDeployerAuthBlock(newAuth, existing)
		cred := got.(map[string]any)["credential"].(map[string]any)
		if cred["token"] != "rotated" {
			t.Errorf("non-sentinel token should update, got %v", cred["token"])
		}
	})

	t.Run("non-map input is returned as-is", func(t *testing.T) {
		if got := mergeDeployerAuthBlock("not-a-map", nil); got != "not-a-map" {
			t.Errorf("non-map input should pass through, got %v", got)
		}
	})
}

func TestValidateToolsetBindingValues_AuthValidatedWhenNoSchemaDeclared(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_no_schema", "do", `[]`)

	err := validateToolsetBindingValues(ctx, "pkg_no_schema", p.ID, map[string]any{
		"auth": map[string]any{
			"credential": map[string]any{"surprise": "shouldNotLeak"},
		},
	}, f.repo)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError even with no declared schema, got %T: %v", err, err)
	}
	if _, ok := verr.Details["pkg_no_schema.auth.credential.surprise"]; !ok {
		t.Errorf("expected violation at pkg_no_schema.auth.credential.surprise, got details=%v", verr.Details)
	}

	if err := validateToolsetBindingValues(ctx, "pkg_no_schema", p.ID, map[string]any{
		"auth": map[string]any{"credential": map[string]any{
			"client_id":     "cid",
			"client_secret": "real-secret",
		}},
		"unknown_field": "tolerated when no schema declared",
	}, f.repo); err != nil {
		t.Errorf("legal auth + no-schema overlay should pass, got %v", err)
	}
}

func TestDeployerAuthFromConfig(t *testing.T) {
	if deployerAuthFromConfig(nil) != nil {
		t.Error("nil config should yield nil")
	}
	if deployerAuthFromConfig(map[string]any{"base_url": "x"}) != nil {
		t.Error("config without auth key should yield nil")
	}
	want := map[string]any{"credential": map[string]any{"client_id": "x"}}
	got := deployerAuthFromConfig(map[string]any{"auth": want})
	if got["credential"].(map[string]any)["client_id"] != "x" {
		t.Errorf("auth value not extracted, got %v", got)
	}
}

func marshalJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
