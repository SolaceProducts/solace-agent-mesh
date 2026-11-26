// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestEffectiveAuthType(t *testing.T) {
	if got := effectiveAuthType("bearer", map[string]any{"type": "apikey"}); got != "bearer" {
		t.Errorf("explicit top-level should win, got %q", got)
	}
	if got := effectiveAuthType("", map[string]any{"type": "bearer", "token": "x"}); got != "bearer" {
		t.Errorf("nested type should be picked up, got %q", got)
	}
	if got := effectiveAuthType("", map[string]any{}); got != "" {
		t.Errorf("missing type should return empty, got %q", got)
	}
	if got := effectiveAuthType("", "bogus"); got != "" {
		t.Errorf("non-map raw should return empty, got %q", got)
	}
	if got := effectiveAuthType("", nil); got != "" {
		t.Errorf("nil raw should return empty, got %q", got)
	}
}

func TestMarshalStoredAuthConfig_FallsBackToNestedType(t *testing.T) {
	raw := map[string]any{"type": "bearer", "token": "bt_demo_value_123"}
	got := marshalStoredAuthConfig("", raw)
	if got == nil {
		t.Fatal("expected non-nil stored config when type is nested")
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(*got), &stored); err != nil {
		t.Fatalf("unmarshal stored: %v", err)
	}
	if stored["type"] != "bearer" {
		t.Errorf("stored type = %v, want bearer", stored["type"])
	}
	if stored["token"] != "bt_demo_value_123" {
		t.Errorf("stored token not preserved: %v", stored["token"])
	}
}

func TestStoredAuthConfigFromWire_AllTypes(t *testing.T) {
	tests := []struct {
		name  string
		at    string
		wire  *CardAuthConfig
		wantT string
		want  map[string]string
	}{
		{
			name:  "bearer",
			at:    "bearer",
			wire:  &CardAuthConfig{BearerToken: "tok-1"},
			wantT: "bearer",
			want:  map[string]string{"token": "tok-1"},
		},
		{
			name:  "apikey default header",
			at:    "apikey",
			wire:  &CardAuthConfig{ApikeyValue: "k-1"},
			wantT: "apikey",
			want:  map[string]string{"api_key": "k-1", "header_name": "X-API-Key"},
		},
		{
			name:  "apikey custom header",
			at:    "apikey",
			wire:  &CardAuthConfig{ApikeyValue: "k-1", ApikeyHeaderName: "X-Y"},
			wantT: "apikey",
			want:  map[string]string{"api_key": "k-1", "header_name": "X-Y"},
		},
		{
			name:  "oauth2",
			at:    "oauth2",
			wire:  &CardAuthConfig{OAuth2ClientID: "cid", OAuth2ClientSecret: "csec", OAuth2TokenURL: "url", OAuth2Scope: "r w"},
			wantT: "oauth2",
			want:  map[string]string{"client_id": "cid", "client_secret": "csec", "token_endpoint": "url"},
		},
		{
			name:  "basic",
			at:    "basic",
			wire:  &CardAuthConfig{BasicUsername: "u", BasicPassword: "p"},
			wantT: "basic",
			want:  map[string]string{"username": "u", "password": "p"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := storedAuthConfigFromWire(tt.at, tt.wire)
			if got == nil {
				t.Fatal("got nil")
			}
			if typ, _ := got["type"].(string); typ != tt.wantT {
				t.Errorf("type = %q, want %q", typ, tt.wantT)
			}
			for k, v := range tt.want {
				if s, _ := got[k].(string); s != v {
					t.Errorf("%s = %q, want %q", k, s, v)
				}
			}
		})
	}
}

func TestStoredAuthConfigFromWire_OAuth2_ScopesSplit(t *testing.T) {
	stored := storedAuthConfigFromWire("oauth2", &CardAuthConfig{
		OAuth2ClientID:     "cid",
		OAuth2ClientSecret: "csec",
		OAuth2TokenURL:     "url",
		OAuth2Scope:        "read write admin",
	})
	scopes, ok := stored["scopes"].([]string)
	if !ok {
		t.Fatalf("scopes type = %T, want []string", stored["scopes"])
	}
	want := []string{"read", "write", "admin"}
	if !reflect.DeepEqual(scopes, want) {
		t.Errorf("scopes = %v, want %v", scopes, want)
	}
}

func TestStoredAuthConfigFromWire_None_ReturnsNil(t *testing.T) {
	if got := storedAuthConfigFromWire("none", nil); got != nil {
		t.Errorf("want nil, got %v", got)
	}
	if got := storedAuthConfigFromWire("bearer", nil); got != nil {
		t.Errorf("nil wire should return nil, got %v", got)
	}
}

func TestRedactStoredAuthConfig_RedactsTypeSpecificFields(t *testing.T) {
	stored := `{"type":"bearer","token":"plaintext"}`
	got := redactStoredAuthConfig(&stored)
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("got type %T, want map", got)
	}
	if m["token"] != redactedSentinel {
		t.Errorf("token = %v, want redacted", m["token"])
	}
	if m["type"] != "bearer" {
		t.Errorf("type was mutated: %v", m["type"])
	}
}

func TestRedactStoredAuthConfig_EmitsCamelCaseWireKeys(t *testing.T) {
	t.Run("apikey", func(t *testing.T) {
		stored := `{"type":"apikey","api_key":"plaintext","header_name":"X-Custom"}`
		got := redactStoredAuthConfig(&stored).(map[string]any)
		if got["apiKey"] != redactedSentinel {
			t.Errorf("apiKey = %v, want redacted", got["apiKey"])
		}
		if got["headerName"] != "X-Custom" {
			t.Errorf("headerName = %v, want X-Custom", got["headerName"])
		}
		for _, leaked := range []string{"api_key", "header_name"} {
			if _, ok := got[leaked]; ok {
				t.Errorf("snake_case %q leaked onto the wire", leaked)
			}
		}
	})

	t.Run("oauth2", func(t *testing.T) {
		stored := `{"type":"oauth2","client_id":"cid","client_secret":"csecret","token_endpoint":"https://a/token","scopes":["read","write"]}`
		got := redactStoredAuthConfig(&stored).(map[string]any)
		if got["clientSecret"] != redactedSentinel {
			t.Errorf("clientSecret = %v, want redacted", got["clientSecret"])
		}
		if got["clientId"] != "cid" {
			t.Errorf("clientId = %v, want cid", got["clientId"])
		}
		if got["tokenEndpoint"] != "https://a/token" {
			t.Errorf("tokenEndpoint = %v, want the stored endpoint", got["tokenEndpoint"])
		}
		for _, leaked := range []string{"client_id", "client_secret", "token_endpoint"} {
			if _, ok := got[leaked]; ok {
				t.Errorf("snake_case %q leaked onto the wire", leaked)
			}
		}
	})

	t.Run("bearer", func(t *testing.T) {
		stored := `{"type":"bearer","token":"plaintext"}`
		got := redactStoredAuthConfig(&stored).(map[string]any)
		if got["token"] != redactedSentinel {
			t.Errorf("token = %v, want redacted", got["token"])
		}
		if got["type"] != "bearer" {
			t.Errorf("type = %v, want bearer", got["type"])
		}
	})

	t.Run("basic", func(t *testing.T) {
		stored := `{"type":"basic","username":"u","password":"plaintext"}`
		got := redactStoredAuthConfig(&stored).(map[string]any)
		if got["password"] != redactedSentinel {
			t.Errorf("password = %v, want redacted", got["password"])
		}
		if got["username"] != "u" {
			t.Errorf("username = %v, want u", got["username"])
		}
	})

	t.Run("deleted secret is omitted, not redacted", func(t *testing.T) {
		stored := `{"type":"apikey","api_key":null,"header_name":"X"}`
		got := redactStoredAuthConfig(&stored).(map[string]any)
		if _, ok := got["apiKey"]; ok {
			t.Error("null (deleted) secret should be omitted so the client shows an empty field")
		}
	})
}

func TestRedactStoredAuthConfig_UnknownTypeScrubsSecrets(t *testing.T) {
	stored := `{"type":"future_kind","api_key":"leak-me","access_token":"leak-too","note":"safe","type_hint":"safe"}`
	got, ok := redactStoredAuthConfig(&stored).(map[string]any)
	if !ok {
		t.Fatalf("got type %T, want map", got)
	}
	for _, secret := range []string{"api_key", "access_token"} {
		if got[secret] != redactedSentinel {
			t.Errorf("%s = %v, want redacted", secret, got[secret])
		}
	}
	if got["note"] != "safe" {
		t.Errorf("note = %v, want preserved", got["note"])
	}
	if got["type"] != "future_kind" {
		t.Errorf("type = %v, want future_kind", got["type"])
	}
}

func TestRedactStoredAuthConfig_EmptyConfigUnchanged(t *testing.T) {
	for _, stored := range []string{`{}`, `{"type":"none"}`} {
		got, ok := redactStoredAuthConfig(&stored).(map[string]any)
		if !ok {
			t.Fatalf("%s: got type %T, want map", stored, got)
		}
		for _, v := range got {
			if s, isStr := v.(string); isStr && s == redactedSentinel {
				t.Errorf("%s: unexpectedly redacted a value in a secret-free config", stored)
			}
		}
	}
}

func TestRedactStoredAuthConfig_OutputReReadableAsCamelCaseWire(t *testing.T) {
	stored := `{"type":"oauth2","client_id":"cid","client_secret":"csecret","token_endpoint":"https://idp/token","scopes":["read","write"]}`
	wire := coerceWireAuthConfig(redactStoredAuthConfig(&stored))
	if wire == nil {
		t.Fatal("coerceWireAuthConfig returned nil — redacted output is not camelCase wire")
	}
	if wire.OAuth2ClientID != "cid" {
		t.Errorf("OAuth2ClientID = %q, want cid", wire.OAuth2ClientID)
	}
	if wire.OAuth2TokenURL != "https://idp/token" {
		t.Errorf("OAuth2TokenURL = %q, want the token endpoint", wire.OAuth2TokenURL)
	}
	if wire.OAuth2ClientSecret != redactedSentinel {
		t.Errorf("OAuth2ClientSecret = %q, want the redacted sentinel", wire.OAuth2ClientSecret)
	}
}

func TestRedactStoredAuthConfig_NilAndBlank(t *testing.T) {
	if redactStoredAuthConfig(nil) != nil {
		t.Error("nil input should return nil")
	}
	bad := `not json`
	if redactStoredAuthConfig(&bad) != nil {
		t.Error("malformed input should return nil")
	}
}

type secretShape int

const (
	shapeOmitted secretShape = iota
	shapeEmpty
	shapeSentinel
	shapeRealValue
)

type authTestCase struct {
	authType   string
	wireSecret string
	storedKey  string
	existing   string
	origSecret string
}

func authTestCases() []authTestCase {
	return []authTestCase{
		{
			authType:   authTypeBearer,
			wireSecret: "token",
			storedKey:  "token",
			existing:   `{"type":"bearer","token":"orig-bearer"}`,
			origSecret: "orig-bearer",
		},
		{
			authType:   authTypeAPIKey,
			wireSecret: "apiKey",
			storedKey:  "api_key",
			existing:   `{"type":"apikey","api_key":"orig-key","header_name":"X-Custom"}`,
			origSecret: "orig-key",
		},
		{
			authType:   authTypeOAuth2,
			wireSecret: "clientSecret",
			storedKey:  "client_secret",
			existing:   `{"type":"oauth2","client_id":"cid","client_secret":"orig-csec","token_endpoint":"https://t","scopes":["r"]}`,
			origSecret: "orig-csec",
		},
		{
			authType:   authTypeBasic,
			wireSecret: "password",
			storedKey:  "password",
			existing:   `{"type":"basic","username":"u","password":"orig-pw"}`,
			origSecret: "orig-pw",
		},
	}
}

func buildIncoming(tc authTestCase, shape secretShape) map[string]any {
	m := map[string]any{}
	switch shape {
	case shapeOmitted:
	case shapeEmpty:
		m[tc.wireSecret] = ""
	case shapeSentinel:
		m[tc.wireSecret] = redactedSentinel
	case shapeRealValue:
		m[tc.wireSecret] = "new-value"
	}
	return m
}

func TestMergeAuthConfigUpdate_TruthTable(t *testing.T) {
	for _, tc := range authTestCases() {
		for _, isRequired := range []bool{true, false} {
			for _, shape := range []struct {
				name          string
				shape         secretShape
				wantValue     any
				wantPreserved bool
			}{
				{"omitted", shapeOmitted, nil, true},
				{"sentinel", shapeSentinel, nil, true},
				{"empty-isRequired", shapeEmpty, nil, true},
				{"empty-isOptional", shapeEmpty, nil, false},
				{"realValue", shapeRealValue, "new-value", false},
			} {
				if shape.name == "empty-isRequired" && !isRequired {
					continue
				}
				if shape.name == "empty-isOptional" && isRequired {
					continue
				}
				tName := tc.authType + "/" + shape.name
				if isRequired {
					tName += "/required"
				} else {
					tName += "/optional"
				}
				t.Run(tName, func(t *testing.T) {
					existing := tc.existing
					incoming := buildIncoming(tc, shape.shape)
					got := mergeAuthConfigUpdate(tc.authType, incoming, &existing, isRequired)
					if got == nil {
						t.Fatal("got nil")
					}
					var parsed map[string]any
					if err := json.Unmarshal([]byte(*got), &parsed); err != nil {
						t.Fatalf("unmarshal: %v", err)
					}
					switch shape.name {
					case "realValue":
						if parsed[tc.storedKey] != "new-value" {
							t.Errorf("%s = %v, want new-value", tc.storedKey, parsed[tc.storedKey])
						}
					case "empty-isOptional":
						if v, present := parsed[tc.storedKey]; !present {
							t.Errorf("stored key absent; want explicit null")
						} else if v != nil {
							t.Errorf("%s = %v, want null", tc.storedKey, v)
						}
					default:
						if parsed[tc.storedKey] != tc.origSecret {
							t.Errorf("%s = %v, want preserved %q", tc.storedKey, parsed[tc.storedKey], tc.origSecret)
						}
					}
					for _, v := range parsed {
						if s, ok := v.(string); ok && s == redactedSentinel {
							t.Errorf("stored config leaked %q sentinel: %v", redactedSentinel, parsed)
						}
					}
				})
			}
		}
	}
}

func TestMergeAuthConfigUpdate_MixedPayload_NoSentinelLeak(t *testing.T) {
	existing := `{"type":"apikey","api_key":"orig","header_name":"X-Custom"}`
	incoming := map[string]any{
		"apiKey":            "rotated-key",
		"headerName":        "X-Custom",
		"someOtherSentinel": redactedSentinel,
	}
	got := mergeAuthConfigUpdate(authTypeAPIKey, incoming, &existing, false)
	if got == nil {
		t.Fatal("got nil")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(*got), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed["api_key"] != "rotated-key" {
		t.Errorf("api_key = %v, want rotated-key", parsed["api_key"])
	}
	if parsed["header_name"] != "X-Custom" {
		t.Errorf("header_name = %v, want X-Custom", parsed["header_name"])
	}
	for k, v := range parsed {
		if s, ok := v.(string); ok && s == redactedSentinel {
			t.Errorf("field %q leaked sentinel literal: %v", k, parsed)
		}
	}
}

func TestMergeAuthConfigUpdate_NonSecretPreservation(t *testing.T) {
	existing := `{"type":"apikey","api_key":"orig","header_name":"X-Custom"}`
	incoming := map[string]any{"apiKey": "rotated"}
	got := mergeAuthConfigUpdate(authTypeAPIKey, incoming, &existing, false)
	if got == nil {
		t.Fatal("got nil")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(*got), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed["header_name"] != "X-Custom" {
		t.Errorf("header_name = %v, want preserved X-Custom", parsed["header_name"])
	}
	if parsed["api_key"] != "rotated" {
		t.Errorf("api_key = %v, want rotated", parsed["api_key"])
	}
}

func TestMergeAuthConfigUpdate_GetPutRoundTrip(t *testing.T) {
	for _, tc := range authTestCases() {
		t.Run(tc.authType, func(t *testing.T) {
			existing := tc.existing
			incoming := map[string]any{tc.wireSecret: redactedSentinel}
			existingParsed, _ := unmarshalAuthConfigTest(existing)
			for wire, stored := range authNonSecretWireMap(tc.authType) {
				if v, ok := existingParsed[stored]; ok {
					incoming[wire] = v
				}
			}
			for _, isRequired := range []bool{true, false} {
				got := mergeAuthConfigUpdate(tc.authType, incoming, &existing, isRequired)
				if got == nil {
					t.Fatal("got nil")
				}
				var parsed map[string]any
				if err := json.Unmarshal([]byte(*got), &parsed); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if parsed[tc.storedKey] != tc.origSecret {
					t.Errorf("isRequired=%v: %s = %v, want preserved %q", isRequired, tc.storedKey, parsed[tc.storedKey], tc.origSecret)
				}
				for k, v := range parsed {
					if s, ok := v.(string); ok && s == redactedSentinel {
						t.Errorf("isRequired=%v: field %q leaked sentinel literal", isRequired, k)
					}
				}
			}
		})
	}
}

func TestMergeAuthConfigUpdate_NoneClearsToEmptyObject(t *testing.T) {
	existing := `{"type":"bearer","token":"x"}`
	got := mergeAuthConfigUpdate("none", map[string]any{}, &existing, false)
	if got == nil || *got != "{}" {
		t.Errorf("got %v, want '{}'", got)
	}
}

func TestMergeAuthConfigUpdate_NilExisting(t *testing.T) {
	incoming := map[string]any{"token": redactedSentinel}
	got := mergeAuthConfigUpdate(authTypeBearer, incoming, nil, true)
	if got == nil {
		t.Fatal("got nil")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(*got), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := parsed["token"]; present {
		t.Errorf("expected token key to be absent when no existing and sentinel incoming, got %v", parsed)
	}
	if parsed["type"] != "bearer" {
		t.Errorf("type = %v, want bearer", parsed["type"])
	}
}

func TestMergeAuthConfigUpdate_NilExisting_RealValue(t *testing.T) {
	incoming := map[string]any{"token": "fresh-secret"}
	got := mergeAuthConfigUpdate(authTypeBearer, incoming, nil, true)
	if got == nil {
		t.Fatal("got nil")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(*got), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed["token"] != "fresh-secret" {
		t.Errorf("token = %v, want fresh-secret", parsed["token"])
	}
}

func TestMergeAuthConfigUpdate_NonMapIncoming_PreservesExisting(t *testing.T) {
	existing := `{"type":"bearer","token":"must-not-be-lost"}`
	cases := []any{
		"garbage",
		42,
		[]any{"not", "a", "map"},
	}
	for _, incoming := range cases {
		t.Run("", func(t *testing.T) {
			got := mergeAuthConfigUpdate(authTypeBearer, incoming, &existing, true)
			if got == nil {
				t.Fatal("got nil")
			}
			if *got != existing {
				t.Errorf("incoming=%v clobbered stored config: got %q, want %q", incoming, *got, existing)
			}
		})
	}
}

func TestMergeAuthConfigUpdate_NilIncoming_PreservesExisting(t *testing.T) {
	existing := `{"type":"bearer","token":"must-not-be-lost"}`
	got := mergeAuthConfigUpdate(authTypeBearer, nil, &existing, true)
	if got == nil {
		t.Fatal("got nil")
	}
	if *got != existing {
		t.Errorf("nil incoming clobbered stored config: got %q, want %q", *got, existing)
	}
}

func TestMergeAuthConfigUpdate_TypeChange(t *testing.T) {
	existing := `{"type":"bearer","token":"old-bearer-secret"}`
	incoming := map[string]any{
		"apiKey":     "new-apikey",
		"headerName": "X-Key",
	}
	got := mergeAuthConfigUpdate(authTypeAPIKey, incoming, &existing, true)
	if got == nil {
		t.Fatal("got nil")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(*got), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed["type"] != "apikey" {
		t.Errorf("type = %v, want apikey", parsed["type"])
	}
	if parsed["api_key"] != "new-apikey" {
		t.Errorf("api_key = %v, want new-apikey", parsed["api_key"])
	}
	if parsed["header_name"] != "X-Key" {
		t.Errorf("header_name = %v, want X-Key", parsed["header_name"])
	}
	if _, hasToken := parsed["token"]; hasToken {
		t.Errorf("stale bearer token leaked into apikey result: %v", parsed)
	}
	if s, _ := parsed["api_key"].(string); s == "old-bearer-secret" {
		t.Errorf("stale bearer secret written to api_key slot: %v", parsed)
	}
}

func unmarshalAuthConfigTest(raw string) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, err
	}
	return m, nil
}
