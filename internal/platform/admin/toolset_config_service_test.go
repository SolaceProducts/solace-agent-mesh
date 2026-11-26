// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

const (
	cfgSchemaSecretRequired = `[{"key":"api_key","type":"string","description":"the key","secret":true},{"key":"model_id","type":"string","description":"model id","default":"gpt-4"}]`
	cfgSchemaWithBoolInt    = `[{"key":"verbose","type":"boolean","default":false},{"key":"timeout","type":"integer","default":30}]`
)

func seedReadyToolWithSchema(ctx context.Context, t *testing.T, f *toolsetFixture, pkgName, toolName, schemaJSON string) *ToolsetResponse {
	t.Helper()
	p := f.createPackage(ctx, t, pkgName)
	if err := f.repo.UpdateZipInfo(ctx, p.ID, "python", "s3://fake", "pkg.zip", 1, "tester"); err != nil {
		t.Fatalf("UpdateZipInfo: %v", err)
	}
	params := `{"type":"object"}`
	f.svc.HandleToolInit(ctx, pkgName+"__"+toolName, "desc", ToolInitMeta{Params: &params, ConfigSchema: &schemaJSON})
	pkg, err := f.repo.GetByID(ctx, p.ID)
	if err != nil || pkg == nil {
		t.Fatalf("re-read package: %v", err)
	}
	if pkg.DiscoveryStatus != "ready" {
		t.Fatalf("expected discovery_status=ready, got %q", pkg.DiscoveryStatus)
	}
	return p
}

func TestToolsetService_GetConfig_NotReady(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := f.createPackage(ctx, t, "notready_pkg")

	_, err := f.svc.GetConfig(ctx, p.ID)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestToolsetService_GetConfig_UnknownPackage(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()

	_, err := f.svc.GetConfig(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	var nfErr *NotFoundError
	if !errors.As(err, &nfErr) {
		t.Fatalf("expected NotFoundError, got %T: %v", err, err)
	}
}

func TestToolsetService_GetConfig_EmptyWhenUnset(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_empty", "do", cfgSchemaSecretRequired)

	resp, err := f.svc.GetConfig(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if len(resp.Values) != 0 {
		t.Errorf("expected empty values, got %v", resp.Values)
	}
	if resp.UpdatedBy != "" {
		t.Errorf("expected empty updatedBy, got %q", resp.UpdatedBy)
	}
}

func TestToolsetService_PatchConfig_RedactsSecretsOnRead(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_redact", "do", cfgSchemaSecretRequired)

	_, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{
			"do": map[string]any{"api_key": "sk-secret", "model_id": "gpt-5"},
		},
	}, "alice")
	if err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	resp, err := f.svc.GetConfig(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	doVals, _ := resp.Values["do"].(map[string]any)
	if got := doVals["api_key"]; got != redactedSentinel {
		t.Errorf("api_key = %v, want %q (secret should be redacted)", got, redactedSentinel)
	}
	if got := doVals["model_id"]; got != "gpt-5" {
		t.Errorf("model_id = %v, want %q (non-secret should pass through)", got, "gpt-5")
	}
	if resp.UpdatedBy != "alice" {
		t.Errorf("updatedBy = %q, want alice", resp.UpdatedBy)
	}
}

func TestToolsetService_GetSecretsDiff_PerToolCandidate(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_diff", "do", cfgSchemaSecretRequired)
	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"api_key": "sk-old"}},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	cases := []struct {
		name      string
		candidate map[string]any
		want      bool
	}{
		{"flat same", map[string]any{"api_key": "sk-old"}, true},
		{"flat rotated", map[string]any{"api_key": "sk-new"}, false},
		{"per-tool same", map[string]any{"do": map[string]any{"api_key": "sk-old"}}, true},
		{"per-tool rotated", map[string]any{"do": map[string]any{"api_key": "sk-new"}}, false},
		{"per-tool wins over flat", map[string]any{"api_key": "sk-old", "do": map[string]any{"api_key": "sk-new"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := f.svc.GetSecretsDiff(ctx, p.ID, tc.candidate)
			if err != nil {
				t.Fatalf("GetSecretsDiff: %v", err)
			}
			if got := res["api_key"].Matches; got != tc.want {
				t.Errorf("api_key matches = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestToolsetService_PatchConfig_RejectsUnknownKeys(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_unk", "do", cfgSchemaSecretRequired)

	_, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{
			"do": map[string]any{"api_key": "sk-good", "not_a_field": "oops"},
		},
	}, "alice")
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if _, ok := verr.Details["do.not_a_field"]; !ok {
		t.Errorf("expected validation detail for do.not_a_field, got %v", verr.Details)
	}
	if _, ok := verr.Details["do.api_key"]; ok {
		t.Errorf("do.api_key should not be in error details, got %v", verr.Details)
	}
}

func TestIsReservedToolsetConfigKey_MatchesPatchValidator(t *testing.T) {
	t.Parallel()
	for _, key := range []string{reservedAuthConfigKey, reservedHILConfigKey, reservedRequiredScopesConfigKey, "ledger_api_key"} {
		err := validatePatchConfigValues(map[string]any{key: map[string]any{}}, nil, nil)
		var verr *ValidationError
		readAsTool := errors.As(err, &verr) && slices.Contains(verr.Details[key], "unknown tool name for this toolset")
		if IsReservedToolsetConfigKey(key) == readAsTool {
			t.Errorf("key %q: IsReservedToolsetConfigKey=%v but validator reads it as a tool name=%v", key, IsReservedToolsetConfigKey(key), readAsTool)
		}
	}
}

func TestToolsetService_PatchConfig_RejectsWrongTypes(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_types", "do", cfgSchemaWithBoolInt)

	_, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{
			"do": map[string]any{"verbose": "not_a_bool", "timeout": 3.5},
		},
	}, "alice")
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if len(verr.Details) != 2 {
		t.Errorf("expected 2 validation details, got %d: %v", len(verr.Details), verr.Details)
	}
}

func TestToolsetService_PatchConfig_RedactedRoundTrip(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_rtrip", "do", cfgSchemaSecretRequired)

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"api_key": "sk-real", "model_id": "gpt-4"}},
	}, "alice"); err != nil {
		t.Fatalf("first PatchConfig: %v", err)
	}

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{
			"do": map[string]any{"api_key": redactedSentinel, "model_id": "gpt-5"},
		},
	}, "bob"); err != nil {
		t.Fatalf("second PatchConfig: %v", err)
	}

	row, err := f.repo.GetToolsetConfig(ctx, p.ID)
	if err != nil || row == nil {
		t.Fatalf("read raw row: %v", err)
	}
	if row.ConfigValues == nil {
		t.Fatal("config_values nil")
	}
	if !strings.Contains(*row.ConfigValues, `"api_key":"sk-real"`) {
		t.Errorf("stored secret = %s, want preserved original (round-trip guard failed)", *row.ConfigValues)
	}
	if !strings.Contains(*row.ConfigValues, `"model_id":"gpt-5"`) {
		t.Errorf("stored model_id = %s, want gpt-5", *row.ConfigValues)
	}
}

func TestToolsetService_PatchConfig_OmittedKeysPreserved(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_omit", "do", cfgSchemaSecretRequired)

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"api_key": "sk-1", "model_id": "gpt-4"}},
	}, "alice"); err != nil {
		t.Fatalf("first PatchConfig: %v", err)
	}

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"model_id": "gpt-5"}},
	}, "bob"); err != nil {
		t.Fatalf("partial PatchConfig: %v", err)
	}

	resp, err := f.svc.GetConfig(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	doVals, _ := resp.Values["do"].(map[string]any)
	if got := doVals["api_key"]; got != redactedSentinel {
		t.Errorf("api_key gone or wrong = %v, want preserved+redacted", got)
	}
	if got := doVals["model_id"]; got != "gpt-5" {
		t.Errorf("model_id = %v, want gpt-5", got)
	}
}

func TestToolsetService_PatchConfig_NullClearsKey(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_null", "do", cfgSchemaSecretRequired)

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"model_id": "gpt-4"}},
	}, "alice"); err != nil {
		t.Fatalf("first PatchConfig: %v", err)
	}

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"model_id": nil}},
	}, "bob"); err != nil {
		t.Fatalf("clear PatchConfig: %v", err)
	}

	resp, err := f.svc.GetConfig(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	doVals, _ := resp.Values["do"].(map[string]any)
	if _, ok := doVals["model_id"]; ok {
		t.Errorf("model_id should be absent after null clear, got %v", doVals)
	}
}

func TestToolsetService_PatchConfig_NotReady(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := f.createPackage(ctx, t, "patch_notready")

	_, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"foo": "bar"},
	}, "alice")
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
}

func TestToolsetService_PatchConfig_RecoversFromCorruptStoredJSON(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_corrupt", "do", cfgSchemaSecretRequired)

	corrupt := `{not valid json`
	if err := f.repo.SetToolsetConfig(ctx, p.ID, &corrupt, "system"); err != nil {
		t.Fatalf("seed corrupt row: %v", err)
	}

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"api_key": "sk-recovered"}},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig after corrupt seed: %v", err)
	}

	resp, err := f.svc.GetConfig(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	doVals, _ := resp.Values["do"].(map[string]any)
	if v, _ := doVals["api_key"].(string); v != redactedSentinel {
		t.Errorf("api_key should be redacted on read, got %v", doVals["api_key"])
	}
}

func TestToolsetService_PatchConfig_AuthExemptFromDriftCleanup(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_auth_drift", "do", cfgSchemaSecretRequired)

	seeded := `{"do":{"api_key":"sk-x","legacy_field":"orphan"},"auth":{"credential":{"client_id":"cid-1"},"scheme":{"audience":"api.example"}}}`
	if err := f.repo.SetToolsetConfig(ctx, p.ID, &seeded, "system"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"model_id": "gpt-5"}},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	resp, err := f.svc.GetConfig(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	doVals, _ := resp.Values["do"].(map[string]any)
	if _, has := doVals["legacy_field"]; has {
		t.Errorf("legacy_field should have been dropped as orphan, got do values: %v", doVals)
	}
	authVal, ok := resp.Values["auth"].(map[string]any)
	if !ok {
		t.Fatalf("auth key should survive drift cleanup, got values: %v", resp.Values)
	}
	cred, _ := authVal["credential"].(map[string]any)
	if cred["client_id"] != "cid-1" {
		t.Errorf("auth.credential.client_id should be preserved, got %v", cred)
	}
}

func TestToolsetService_PatchConfig_HILExemptFromDriftCleanup(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_hil_drift", "do", cfgSchemaSecretRequired)

	seeded := `{"do":{"api_key":"sk-x","legacy_field":"orphan"},"hil":{"do":{"require_approval":true,"approval_message":"old"}}}`
	if err := f.repo.SetToolsetConfig(ctx, p.ID, &seeded, "system"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"model_id": "gpt-5"}},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	resp, err := f.svc.GetConfig(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	if doVals, _ := resp.Values["do"].(map[string]any); doVals["legacy_field"] != nil {
		t.Errorf("legacy_field should have been dropped as orphan, got: %v", doVals)
	}
	hilVal, ok := resp.Values["hil"].(map[string]any)
	if !ok {
		t.Fatalf("hil key should survive drift cleanup, got values: %v", resp.Values)
	}
	doHIL, _ := hilVal["do"].(map[string]any)
	if doHIL["approval_message"] != "old" {
		t.Errorf("stored hil block should be preserved verbatim, got %v", doHIL)
	}

	if _, perr := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{
			"hil": map[string]any{"do": map[string]any{"require_approval": true, "approval_message": "new"}},
		},
	}, "alice"); perr != nil {
		t.Fatalf("PatchConfig (replace hil): %v", perr)
	}
	resp, err = f.svc.GetConfig(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	hilVal, _ = resp.Values["hil"].(map[string]any)
	doHIL, _ = hilVal["do"].(map[string]any)
	if doHIL["approval_message"] != "new" {
		t.Errorf("included hil block should replace the stored one, got %v", doHIL)
	}
}

func TestToolsetService_PatchConfig_RejectsDisallowedAuthFields(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_auth_bad", "do", cfgSchemaSecretRequired)

	_, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{
			"auth": map[string]any{
				"credential": map[string]any{
					"client_id":     "cid-good",
					"client_secret": "real-secret",
					"surprise":      "shouldNotLeak",
				},
				"scheme": map[string]any{
					"authorization_url": "https://attacker.example/auth",
					"audience":          "api.example",
				},
			},
		},
	}, "alice")

	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	for _, path := range []string{"auth.credential.surprise", "auth.scheme.authorization_url"} {
		if _, ok := verr.Details[path]; !ok {
			t.Errorf("expected violation at %q, got details=%v", path, verr.Details)
		}
	}
	for _, path := range []string{"auth.credential.client_id", "auth.credential.client_secret", "auth.scheme.audience"} {
		if _, ok := verr.Details[path]; ok {
			t.Errorf("%s is allow-listed; should not be flagged, got %v", path, verr.Details[path])
		}
	}
}

func TestToolsetService_GetConfig_SanitizesAuthOnRead(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_auth_sanitize", "do", cfgSchemaSecretRequired)

	tainted := `{"do":{},"auth":{"credential":{"client_id":"cid-ok","client_secret":"real-secret","surprise":"shouldNotLeak"},"scheme":{"audience":"api.example","authorization_url":"https://attacker.example/auth"}}}`
	if err := f.repo.SetToolsetConfig(ctx, p.ID, &tainted, "system"); err != nil {
		t.Fatalf("seed tainted row: %v", err)
	}

	resp, err := f.svc.GetConfig(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	authVal, _ := resp.Values["auth"].(map[string]any)
	cred, _ := authVal["credential"].(map[string]any)
	if cred["client_secret"] != redactedSentinel {
		t.Errorf("client_secret must surface as %q on read, got %v", redactedSentinel, cred)
	}
	if _, has := cred["surprise"]; has {
		t.Errorf("disallowed credential.surprise must be stripped on read, got %v", cred)
	}
	if cred["client_id"] != "cid-ok" {
		t.Errorf("client_id should survive sanitization, got %v", cred)
	}
	scheme, _ := authVal["scheme"].(map[string]any)
	if _, has := scheme["authorization_url"]; has {
		t.Errorf("authorization_url must be sanitized out on read, got %v", scheme)
	}
	if scheme["audience"] != "api.example" {
		t.Errorf("audience should survive sanitization, got %v", scheme)
	}
}

func TestToolsetService_PatchConfig_AuthSecretRoundTrip(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_auth_roundtrip", "do", cfgSchemaSecretRequired)

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{
			"auth": map[string]any{
				"credential": map[string]any{
					"client_id":     "cid-1",
					"client_secret": "original-secret",
				},
			},
		},
	}, "alice"); err != nil {
		t.Fatalf("initial PatchConfig: %v", err)
	}

	resp, err := f.svc.GetConfig(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	auth, _ := resp.Values["auth"].(map[string]any)
	cred, _ := auth["credential"].(map[string]any)
	if cred["client_secret"] != redactedSentinel {
		t.Fatalf("GET should return %q, got %v", redactedSentinel, cred["client_secret"])
	}

	if _, patchErr := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{
			"auth": map[string]any{
				"credential": map[string]any{
					"client_id":     "cid-2",
					"client_secret": redactedSentinel,
				},
			},
		},
	}, "alice"); patchErr != nil {
		t.Fatalf("round-trip PatchConfig: %v", patchErr)
	}

	row, err := f.repo.GetToolsetConfig(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetToolsetConfig: %v", err)
	}
	if row == nil || row.ConfigValues == nil {
		t.Fatal("expected stored config row")
	}
	if !strings.Contains(*row.ConfigValues, `"client_secret":"original-secret"`) {
		t.Errorf("stored client_secret should still be the original after <REDACTED> round-trip, got %s", *row.ConfigValues)
	}
	if !strings.Contains(*row.ConfigValues, `"client_id":"cid-2"`) {
		t.Errorf("client_id should have updated to cid-2, got %s", *row.ConfigValues)
	}

	if _, patchErr := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{
			"auth": map[string]any{
				"credential": map[string]any{"client_secret": "rotated-secret"},
			},
		},
	}, "alice"); patchErr != nil {
		t.Fatalf("rotate PatchConfig: %v", patchErr)
	}
	row, err = f.repo.GetToolsetConfig(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetToolsetConfig: %v", err)
	}
	if !strings.Contains(*row.ConfigValues, `"client_secret":"rotated-secret"`) {
		t.Errorf("client_secret should be the rotated value, got %s", *row.ConfigValues)
	}
}

func TestToolsetService_PatchConfig_DropsOrphanedKeys(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_orphan", "do", cfgSchemaSecretRequired)

	seeded := `{"do":{"api_key":"sk-from-old","legacy_field":"orphan-value"}}`
	if err := f.repo.SetToolsetConfig(ctx, p.ID, &seeded, "system"); err != nil {
		t.Fatalf("seed pre-drift row: %v", err)
	}

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"model_id": "gpt-5"}},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	resp, err := f.svc.GetConfig(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	doVals, _ := resp.Values["do"].(map[string]any)
	if _, has := doVals["legacy_field"]; has {
		t.Errorf("legacy_field should be dropped as orphan, got %v", doVals)
	}
	if doVals["model_id"] != "gpt-5" {
		t.Errorf("model_id = %v, want gpt-5", doVals["model_id"])
	}
	if _, has := doVals["api_key"]; !has {
		t.Errorf("api_key should still be present after orphan-drop, got %v", doVals)
	}
}

func TestToolsetService_PatchConfig_EmptyValuesNoOp(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_empty_patch", "do", cfgSchemaSecretRequired)

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"api_key": "sk-original"}},
	}, "alice"); err != nil {
		t.Fatalf("seed PatchConfig: %v", err)
	}

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{},
	}, "bob"); err != nil {
		t.Fatalf("empty PatchConfig: %v", err)
	}

	resp, err := f.svc.GetConfig(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	doVals, _ := resp.Values["do"].(map[string]any)
	if v, _ := doVals["api_key"].(string); v != redactedSentinel {
		t.Errorf("api_key should still be present (redacted) after empty patch, got %v", doVals["api_key"])
	}
}

func TestToolsetService_BuildConfigSummary(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_summary", "do", cfgSchemaSecretRequired)

	t.Run("uncovered_when_no_defaults_set", func(t *testing.T) {
		sum, err := f.svc.BuildConfigSummary(ctx, p.ID, "ready")
		if err != nil {
			t.Fatalf("BuildConfigSummary: %v", err)
		}
		if sum == nil {
			t.Fatal("expected non-nil summary for ready package")
		}
		if len(sum.RequiredFields) != 1 || sum.RequiredFields[0] != "do/api_key" {
			t.Errorf("RequiredFields = %v, want [do/api_key]", sum.RequiredFields)
		}
		if len(sum.RequiredCoveredByDefaults) != 0 {
			t.Errorf("expected 0 covered, got %v", sum.RequiredCoveredByDefaults)
		}
		if !sum.RequiresAgentConfig {
			t.Error("RequiresAgentConfig should be true when api_key is uncovered")
		}
	})

	t.Run("covered_after_patch", func(t *testing.T) {
		if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
			Values: map[string]any{"do": map[string]any{"api_key": "sk-set"}},
		}, "alice"); err != nil {
			t.Fatalf("PatchConfig: %v", err)
		}
		sum, err := f.svc.BuildConfigSummary(ctx, p.ID, "ready")
		if err != nil {
			t.Fatalf("BuildConfigSummary: %v", err)
		}
		if len(sum.RequiredCoveredByDefaults) != 1 || sum.RequiredCoveredByDefaults[0] != "do/api_key" {
			t.Errorf("RequiredCoveredByDefaults = %v, want [do/api_key]", sum.RequiredCoveredByDefaults)
		}
		if sum.RequiresAgentConfig {
			t.Error("RequiresAgentConfig should be false when api_key is covered")
		}
	})

	t.Run("nil_when_not_ready", func(t *testing.T) {
		sum, err := f.svc.BuildConfigSummary(ctx, p.ID, "pending")
		if err != nil {
			t.Fatalf("BuildConfigSummary: %v", err)
		}
		if sum != nil {
			t.Errorf("expected nil summary for non-ready, got %+v", sum)
		}
	})
}

func TestToolsetService_GetSecretsDiff(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_diff", "do", cfgSchemaSecretRequired)

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{"do": map[string]any{"api_key": "sk-stored", "model_id": "gpt-5"}},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	cases := []struct {
		name      string
		candidate map[string]any
		want      bool
	}{
		{"same value matches", map[string]any{"api_key": "sk-stored"}, true},
		{"rotated value mismatches", map[string]any{"api_key": "sk-rotated"}, false},
		{"redacted sentinel preserves", map[string]any{"api_key": redactedSentinel}, true},
		{"omitted preserves", map[string]any{"model_id": "gpt-5"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := f.svc.GetSecretsDiff(ctx, p.ID, tc.candidate)
			if err != nil {
				t.Fatalf("GetSecretsDiff: %v", err)
			}
			if _, ok := res["model_id"]; ok {
				t.Errorf("non-secret model_id leaked into diff result: %+v", res)
			}
			got, ok := res["api_key"]
			if !ok {
				t.Fatalf("api_key missing from diff result: %+v", res)
			}
			if got.Matches != tc.want {
				t.Errorf("api_key matches = %v; want %v", got.Matches, tc.want)
			}
		})
	}
}

func TestToolsetService_GetSecretsDiff_AuthCredentialSecrets(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_diff_auth", "do", cfgSchemaSecretRequired)

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{
			"auth": map[string]any{"credential": map[string]any{
				"client_id":     "cid",
				"client_secret": "stored-secret",
				"password":      "stored-password",
			}},
		},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	authCandidate := func(cred map[string]any) map[string]any {
		return map[string]any{"auth": map[string]any{"credential": cred}}
	}
	const secretKey = "auth.credential.client_secret"
	const passwordKey = "auth.credential.password"
	cases := []struct {
		name         string
		candidate    map[string]any
		wantSecret   bool
		wantPassword bool
	}{
		{"same values match", authCandidate(map[string]any{"client_secret": "stored-secret", "password": "stored-password"}), true, true},
		{"rotated client_secret mismatches", authCandidate(map[string]any{"client_secret": "rotated"}), false, true},
		{"rotated password mismatches", authCandidate(map[string]any{"password": "rotated"}), true, false},
		{"redacted sentinel preserves", authCandidate(map[string]any{"client_secret": redactedSentinel}), true, true},
		{"null preserves", authCandidate(map[string]any{"client_secret": nil}), true, true},
		{"omitted credential preserves", authCandidate(map[string]any{"client_id": "cid"}), true, true},
		{"no auth block preserves", map[string]any{"model_id": "gpt-5"}, true, true},
		{"rotation beside schema secret", map[string]any{"api_key": "x", "auth": map[string]any{"credential": map[string]any{"client_secret": "rotated"}}}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := f.svc.GetSecretsDiff(ctx, p.ID, tc.candidate)
			if err != nil {
				t.Fatalf("GetSecretsDiff: %v", err)
			}
			if got := res[secretKey]; got.Matches != tc.wantSecret {
				t.Errorf("%s matches = %v; want %v", secretKey, got.Matches, tc.wantSecret)
			}
			if got := res[passwordKey]; got.Matches != tc.wantPassword {
				t.Errorf("%s matches = %v; want %v", passwordKey, got.Matches, tc.wantPassword)
			}
			if _, leaked := res["client_id"]; leaked {
				t.Errorf("non-secret client_id leaked into diff result: %+v", res)
			}
			if _, leaked := res["auth.credential.client_id"]; leaked {
				t.Errorf("non-secret client_id leaked into diff result: %+v", res)
			}
		})
	}
}

func TestToolsetService_GetSecretsDiff_AuthCredentialNoStoredAuth(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_diff_auth_none", "do", cfgSchemaSecretRequired)

	res, err := f.svc.GetSecretsDiff(ctx, p.ID, map[string]any{
		"auth": map[string]any{"credential": map[string]any{"client_secret": "brand-new"}},
	})
	if err != nil {
		t.Fatalf("GetSecretsDiff: %v", err)
	}
	if got := res["auth.credential.client_secret"]; got.Matches {
		t.Errorf("a secret with nothing stored must report matches=false, got %+v", res)
	}
}

func TestToolsetService_GetSecretsDiff_AuthCredentialWithoutSchemaSecrets(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := seedReadyToolWithSchema(ctx, t, f, "pkg_diff_auth_only", "do", cfgSchemaWithBoolInt)

	if _, err := f.svc.PatchConfig(ctx, p.ID, ToolsetConfigPatchRequest{
		Values: map[string]any{
			"auth": map[string]any{"credential": map[string]any{"client_id": "cid", "client_secret": "stored-secret"}},
		},
	}, "alice"); err != nil {
		t.Fatalf("PatchConfig: %v", err)
	}

	res, err := f.svc.GetSecretsDiff(ctx, p.ID, map[string]any{
		"auth": map[string]any{"credential": map[string]any{"client_secret": "rotated"}},
	})
	if err != nil {
		t.Fatalf("GetSecretsDiff: %v", err)
	}
	if got, ok := res["auth.credential.client_secret"]; !ok || got.Matches {
		t.Errorf("rotated auth secret on a toolset with no schema secrets must report matches=false, got %+v", res)
	}
}

func TestToolsetService_GetSecretsDiff_NotReady(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()
	p := f.createPackage(ctx, t, "diff_notready")

	_, err := f.svc.GetSecretsDiff(ctx, p.ID, map[string]any{"api_key": "x"})
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("expected ValidationError for not-ready toolset, got %T: %v", err, err)
	}
}

func TestToolsetService_GetSecretsDiff_UnknownPackage(t *testing.T) {
	t.Parallel()
	f := newToolsetFixture(t)
	ctx := t.Context()

	_, err := f.svc.GetSecretsDiff(ctx, "ffffffff-ffff-ffff-ffff-ffffffffffff", map[string]any{"api_key": "x"})
	var nfErr *NotFoundError
	if !errors.As(err, &nfErr) {
		t.Fatalf("expected NotFoundError, got %T: %v", err, err)
	}
}
