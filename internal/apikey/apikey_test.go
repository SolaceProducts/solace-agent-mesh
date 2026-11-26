// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apikey

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateProducesAWellFormedKey(t *testing.T) {
	t.Parallel()

	key, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	secret, ok := strings.CutPrefix(key, Marker)
	if !ok {
		t.Fatalf("key %q does not carry the %q marker", key, Marker)
	}
	raw, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil {
		t.Fatalf("secret portion is not base64url: %v", err)
	}
	if len(raw) != secretBytes {
		t.Errorf("secret carries %d bytes, want %d", len(raw), secretBytes)
	}
}

func TestGenerateDoesNotRepeat(t *testing.T) {
	t.Parallel()

	const runs = 500
	seen := make(map[string]struct{}, runs)
	for range runs {
		key, err := Generate()
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if _, dup := seen[key]; dup {
			t.Fatalf("Generate returned a duplicate key within %d calls", runs)
		}
		seen[key] = struct{}{}
	}
}

func TestHashCoversTheMarker(t *testing.T) {
	t.Parallel()

	key, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	withMarker := Hash(key)
	withoutMarker := Hash(strings.TrimPrefix(key, Marker))
	if withMarker == withoutMarker {
		t.Error("Hash ignores the marker; it must digest the complete plaintext")
	}
}

func TestHashIsDeterministicAndUnsalted(t *testing.T) {
	t.Parallel()

	first, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	second, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	digest := Hash(first)
	if recomputed := Hash(first); recomputed != digest {
		t.Errorf("Hash is not deterministic: %q then %q", digest, recomputed)
	}
	if Hash(first) == Hash(second) {
		t.Error("distinct keys hashed to the same digest")
	}
	if len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" {
		t.Errorf("digest %q is not 64 lowercase hex characters", digest)
	}
}

func TestDisplayPrefix(t *testing.T) {
	t.Parallel()

	generated, err := Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	wellFormed := strings.Repeat("A", 42) + "Q"
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"a generated key", generated, generated[len(Marker) : len(Marker)+DisplayPrefixLen]},
		{"canonical encoding of 32 bytes", Marker + wellFormed, "AAAAAAAA"},
		{"one character short", Marker + wellFormed[:42], ""},
		{"one character long", Marker + wellFormed + "A", ""},
		{"right length, not base64url", Marker + strings.Repeat("!", 43), ""},
		{"right length, padded standard alphabet", Marker + strings.Repeat("+", 42) + "=", ""},
		{"right length, non-canonical trailing bits", Marker + strings.Repeat("A", 42) + "B", ""},
		{"secret shorter than the prefix", Marker + "abc", ""},
		{"no marker", "abcdefghijklmnop", ""},
		{"empty", "", ""},
		{"marker only", Marker, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := DisplayPrefix(tc.input); got != tc.want {
				t.Errorf("DisplayPrefix(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestDisplayPrefixLeaksNothingFromAMalformedCredential(t *testing.T) {
	t.Parallel()

	const planted = "not-a-sam-key-but-secret"
	if got := DisplayPrefix(planted); got != "" {
		t.Errorf("DisplayPrefix returned %q for a marker-less credential; want \"\"", got)
	}
}

func TestHasMarker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  bool
	}{
		{Marker + "abcdefgh", true},
		{Marker, true},
		{"samk", false},
		{"", false},
		{"eyJhbGciOiJFUzI1NiJ9.e30.sig", false},
		{" " + Marker + "abcdefgh", false},
	}
	for _, tc := range tests {
		if got := HasMarker(tc.input); got != tc.want {
			t.Errorf("HasMarker(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestBearerCredential(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		header string
		want   string
		wantOK bool
	}{
		"plain":                  {"Bearer samk_abc", "samk_abc", true},
		"two spaces":             {"Bearer  samk_abc", "samk_abc", true},
		"a tab":                  {"Bearer \tsamk_abc", "samk_abc", true},
		"trailing blanks":        {"Bearer samk_abc \t", "samk_abc", true},
		"empty credential":       {"Bearer ", "", true},
		"another scheme":         {"Basic dXNlcjpwYXNz", "", false},
		"no scheme":              {"samk_abc", "", false},
		"lower-case scheme":      {"bearer samk_abc", "", false},
		"scheme without a blank": {"Bearer", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got, ok := BearerCredential(tc.header)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("BearerCredential(%q) = %q, %v; want %q, %v", tc.header, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}
