// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package builderapply

import (
	"reflect"
	"strings"
	"testing"
)

func TestDecideA2AAuth(t *testing.T) {
	t.Parallel()
	const live = "super-secret-live-token"
	bearer := func(token string) map[string]any { return map[string]any{"type": "bearer", "token": token} }

	cases := []struct {
		name string

		config     any
		authorType string
		stored     string

		wantConfig  any
		wantType    string
		wantEffType string
		wantRotates bool
	}{
		{
			name:        "nothing authored preserves",
			stored:      "bearer",
			wantEffType: "bearer",
		},

		{
			name:   "rotation with the type nested",
			config: bearer("rotated"), stored: "bearer",
			wantConfig: bearer("rotated"), wantEffType: "bearer", wantRotates: true,
		},
		{
			name:   "rotation with the type at the top level",
			config: map[string]any{"token": "rotated"}, authorType: "bearer", stored: "bearer",
			wantConfig: map[string]any{"type": "bearer", "token": "rotated"},
			wantType:   "bearer", wantEffType: "bearer", wantRotates: true,
		},
		{
			name:   "rotation with the type only in stored state",
			config: map[string]any{"token": "rotated"}, stored: "bearer",
			wantConfig:  map[string]any{"type": "bearer", "token": "rotated"},
			wantEffType: "bearer", wantRotates: true,
		},

		{
			name:   "placeholder alone preserves",
			config: bearer("<<__SAM_REQUIRED__>>"), stored: "bearer",
			wantEffType: "bearer",
		},
		{
			name:   "placeholder with an explicit type still preserves",
			config: bearer("<<__SAM_REQUIRED__>>"), authorType: "bearer", stored: "bearer",
			wantEffType: "bearer",
		},
		{
			name:   "redaction sentinel preserves",
			config: bearer("<REDACTED>"), stored: "bearer",
			wantEffType: "bearer",
		},

		{
			name:   "bare no-auth block is boilerplate and preserves",
			config: map[string]any{"type": "none"}, stored: "bearer",
			wantEffType: "bearer",
		},
		{
			name:   "explicit none with a config clears",
			config: map[string]any{"type": "none"}, authorType: "none", stored: "bearer",
			wantConfig: map[string]any{"type": "none"}, wantType: "none", wantEffType: "none",
		},
		{
			name:       "explicit none without a config still clears",
			authorType: "none", stored: "bearer",
			wantConfig: map[string]any{"type": "none"}, wantType: "none", wantEffType: "none",
		},

		{
			name:       "type change without a credential sends a validatable block",
			authorType: "bearer", stored: "none",
			wantConfig: map[string]any{"type": "bearer"}, wantType: "bearer", wantEffType: "bearer",
		},
		{
			name:   "type switch carrying its credential lands",
			config: map[string]any{"type": "basic", "username": "svc", "password": "pw"}, stored: "bearer",
			wantConfig:  map[string]any{"type": "basic", "username": "svc", "password": "pw"},
			wantEffType: "basic", wantRotates: true,
		},

		{
			name:        "non-secret edit lands and is not a rotation",
			config:      map[string]any{"type": "basic", "username": "edited", "password": "<<__SAM_REQUIRED__>>"},
			stored:      "basic",
			wantConfig:  map[string]any{"type": "basic", "username": "edited"},
			wantEffType: "basic",
		},

		{
			name:   "no stored type takes the artifact at its word",
			config: bearer(live), stored: "",
			wantConfig: bearer(live), wantEffType: "bearer", wantRotates: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decideA2AAuth(tc.config, tc.authorType, tc.stored)
			if err != nil {
				t.Fatalf("decideA2AAuth: %v", err)
			}

			if !reflect.DeepEqual(got.Config, tc.wantConfig) {
				t.Errorf("Config = %v, want %v", got.Config, tc.wantConfig)
			}
			if got.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", got.Type, tc.wantType)
			}
			if got.EffectiveType != tc.wantEffType {
				t.Errorf("EffectiveType = %q, want %q", got.EffectiveType, tc.wantEffType)
			}
			if got.Rotates != tc.wantRotates {
				t.Errorf("Rotates = %v, want %v", got.Rotates, tc.wantRotates)
			}
		})
	}
}

func TestDecideA2AAuth_RefusesATypeItCannotResolve(t *testing.T) {
	t.Parallel()
	typos := []string{"apiKey", "Bearer", "oauth", "NONE", "api_key", "basic-auth"}

	for _, typo := range typos {
		t.Run(typo, func(t *testing.T) {
			for _, tc := range []struct {
				where        string
				config       any
				authoredType string
			}{
				{"nested", map[string]any{"type": typo, "token": "x"}, ""},
				{"top level", map[string]any{"token": "x"}, typo},
			} {
				_, err := decideA2AAuth(tc.config, tc.authoredType, "bearer")
				if err == nil {
					t.Errorf("%s: an unresolvable type was accepted; downstream this clears the stored credential", tc.where)
				}
			}
		})
	}

	for _, valid := range []string{"none", "bearer", "apikey", "basic", "oauth2"} {
		if _, err := decideA2AAuth(map[string]any{"type": valid}, "", "bearer"); err != nil {
			t.Errorf("valid type %q was refused: %v", valid, err)
		}
	}
}

func TestDecideA2AAuth_RefusesAContradictoryBlock(t *testing.T) {
	t.Parallel()
	_, err := decideA2AAuth(
		map[string]any{"type": "bearer", "username": "u", "password": "NEW"}, "bearer", "bearer")
	if err == nil {
		t.Fatal("a bearer block carrying a basic-auth password was accepted; the credential is silently discarded")
	}
	if !strings.Contains(err.Error(), "password") {
		t.Errorf("err = %q, want it to name the contradicting key", err)
	}
}

func TestDecideA2AAuth_NeverEmitsAnUnfilledSecret(t *testing.T) {
	t.Parallel()
	unfilled := []string{"<<__SAM_REQUIRED__>>", "<REDACTED>"}
	types := []string{"bearer", "apikey", "basic", "oauth2"}
	fields := map[string]string{"bearer": "token", "apikey": "apiKey", "basic": "password", "oauth2": "clientSecret"}

	for _, authType := range types {
		for _, value := range unfilled {
			for _, explicit := range []string{"", authType} {
				config := map[string]any{"type": authType, fields[authType]: value}
				got, err := decideA2AAuth(config, explicit, authType)
				if err != nil {
					t.Fatalf("decideA2AAuth: %v", err)
				}

				out, _ := got.Config.(map[string]any)
				if v, present := out[fields[authType]]; present {
					t.Errorf("type=%s explicit=%q: emitted %s=%v, want the key dropped so the merge preserves the stored credential",
						authType, explicit, fields[authType], v)
				}
				if got.Rotates {
					t.Errorf("type=%s explicit=%q: reported a rotation for an unfilled placeholder", authType, explicit)
				}
			}
		}
	}
}
