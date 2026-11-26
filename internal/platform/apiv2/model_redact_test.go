// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"reflect"
	"testing"

	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
)

func TestRedactModelAuthConfig(t *testing.T) {
	tests := []struct {
		name   string
		stored map[string]any
		want   map[string]any
	}{
		{
			name:   "known type redacts its secret and keeps the rest",
			stored: map[string]any{"type": "apikey", "api_key": "sk-real", "api_base": "https://x"},
			want:   map[string]any{"type": "apikey", "api_key": redactedSentinel, "api_base": "https://x"},
		},
		{
			name:   "known type with several secrets redacts each",
			stored: map[string]any{"type": "aws_iam", "aws_secret_access_key": "k", "aws_session_token": "t", "region": "eu"},
			want:   map[string]any{"type": "aws_iam", "aws_secret_access_key": redactedSentinel, "aws_session_token": redactedSentinel, "region": "eu"},
		},
		{
			name:   "unknown type redacts every value but keeps type",
			stored: map[string]any{"type": "quantum_handshake", "secret_token": "s3cret", "region": "eu"},
			want:   map[string]any{"type": "quantum_handshake", "secret_token": redactedSentinel, "region": redactedSentinel},
		},
		{
			name:   "missing type redacts everything",
			stored: map[string]any{"api_key": "sk-real"},
			want:   map[string]any{"api_key": redactedSentinel},
		},
		{
			name:   "empty string type redacts everything",
			stored: map[string]any{"type": "", "api_key": "sk-real"},
			want:   map[string]any{"type": "", "api_key": redactedSentinel},
		},
		{
			name:   "non-string type is redacted with everything else",
			stored: map[string]any{"type": 7, "api_key": "sk-real"},
			want:   map[string]any{"type": redactedSentinel, "api_key": redactedSentinel},
		},
		{
			name:   "known type carrying another type's secret redacts it",
			stored: map[string]any{"type": "apikey", "api_key": "sk-real", "password": "left-behind"},
			want:   map[string]any{"type": "apikey", "api_key": redactedSentinel, "password": redactedSentinel},
		},
		{
			name:   "number under a known type is kept",
			stored: map[string]any{"type": "oauth2", "client_secret": "s", "token_refresh_buffer_seconds": 300, "max_retries": float64(3)},
			want:   map[string]any{"type": "oauth2", "client_secret": redactedSentinel, "token_refresh_buffer_seconds": 300, "max_retries": float64(3)},
		},
		{
			name:   "a cleared setting reads back as null, under any type",
			stored: map[string]any{"type": "apikey", "api_key": "sk-real", "client_id": nil, "client_secret": nil},
			want:   map[string]any{"type": "apikey", "api_key": redactedSentinel, "client_id": nil, "client_secret": nil},
		},
		{
			name:   "a null under an unknown type reads back as null",
			stored: map[string]any{"type": "quantum_handshake", "region": nil},
			want:   map[string]any{"type": "quantum_handshake", "region": nil},
		},
		{
			name:   "the runtime's oauth_client_secret spelling is redacted",
			stored: map[string]any{"type": "oauth2", "oauth_client_secret": "s-real", "oauth_client_id": "client-1"},
			want:   map[string]any{"type": "oauth2", "oauth_client_secret": redactedSentinel, "oauth_client_id": "client-1"},
		},
		{
			name:   "bool under a known type is kept",
			stored: map[string]any{"type": "apikey", "api_key": "sk-real", "verify_ssl": true},
			want:   map[string]any{"type": "apikey", "api_key": redactedSentinel, "verify_ssl": true},
		},
		{
			name:   "number under an unknown type is redacted",
			stored: map[string]any{"type": "quantum_handshake", "max_retries": 3},
			want:   map[string]any{"type": "quantum_handshake", "max_retries": redactedSentinel},
		},
		{
			name:   "number under a secret name is redacted",
			stored: map[string]any{"type": "apikey", "api_key": 12345},
			want:   map[string]any{"type": "apikey", "api_key": redactedSentinel},
		},
		{
			name:   "nested map under a known type is redacted wholesale",
			stored: map[string]any{"type": "apikey", "options": map[string]any{"api_key": "sk-real"}},
			want:   map[string]any{"type": "apikey", "options": redactedSentinel},
		},
		{
			name:   "slice under a known type is redacted wholesale",
			stored: map[string]any{"type": "apikey", "extras": []any{"sk-real"}},
			want:   map[string]any{"type": "apikey", "extras": redactedSentinel},
		},
		{
			name:   "nil type is redacted with everything else",
			stored: map[string]any{"type": nil, "api_key": "sk-real"},
			want:   map[string]any{"type": redactedSentinel, "api_key": redactedSentinel},
		},
		{
			name:   "map-valued type is redacted with everything else",
			stored: map[string]any{"type": map[string]any{"api_key": "sk-nested"}, "api_key": "sk-real"},
			want:   map[string]any{"type": redactedSentinel, "api_key": redactedSentinel},
		},
		{
			name:   "case-variant type is unknown and redacts everything",
			stored: map[string]any{"type": "APIKEY", "api_key": "sk-real", "api_base": "https://x"},
			want:   map[string]any{"type": "APIKEY", "api_key": redactedSentinel, "api_base": redactedSentinel},
		},
		{
			name:   "nil config yields an empty map",
			stored: nil,
			want:   map[string]any{},
		},
		{
			name:   "empty config stays empty",
			stored: map[string]any{},
			want:   map[string]any{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var before map[string]any
			if tt.stored != nil {
				before = make(map[string]any, len(tt.stored))
				for k, v := range tt.stored {
					before[k] = v
				}
			}

			got := redactModelAuthConfig(tt.stored)

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
			if tt.stored != nil && !reflect.DeepEqual(tt.stored, before) {
				t.Errorf("redaction mutated its input: got %v, want %v", tt.stored, before)
			}
		})
	}
}

func TestRedactModelAuthConfigReturnsACopy(t *testing.T) {
	stored := map[string]any{"type": "apikey", "api_key": "sk-real"}

	got := redactModelAuthConfig(stored)
	got["api_key"] = "changed"

	if stored["api_key"] != "sk-real" {
		t.Errorf("writing to the result changed the stored map: %v", stored)
	}
}

func TestRedactModelAuthConfigDoesNotAliasNestedValues(t *testing.T) {
	nested := map[string]any{"x": "original"}
	stored := map[string]any{"type": "apikey", "options": nested}

	got := redactModelAuthConfig(stored)
	if inner, ok := got["options"].(map[string]any); ok {
		inner["x"] = "changed"
	}

	if nested["x"] != "original" {
		t.Errorf("mutating the result changed the stored nested map: %v", nested)
	}
}

func TestModelAuthSecretKeysCoverSupportedAuthTypes(t *testing.T) {
	for _, authType := range platpkg.SupportedModelAuthTypes {
		if _, ok := modelAuthSecretKeys[authType]; !ok {
			t.Errorf("auth type %q has no entry in modelAuthSecretKeys; add it (an empty list if it has no secrets)", authType)
		}
	}
}
