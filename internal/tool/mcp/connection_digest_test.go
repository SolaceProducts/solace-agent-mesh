// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

func mustDigest(t *testing.T, id connectionIdentity) string {
	t.Helper()
	digest, err := id.digest()
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	return digest
}

func TestConnectionIdentityDigestSeparatesValuesThatWouldRunTogether(t *testing.T) {
	cases := []struct {
		name string
		a, b connectionIdentity
	}{
		{"args", connectionIdentity{Args: []string{"a;sb", "c"}}, connectionIdentity{Args: []string{"a", "b;sc"}}},
		{"args split", connectionIdentity{Args: []string{"ab", "c"}}, connectionIdentity{Args: []string{"a", "bc"}}},
		{"env key against value", connectionIdentity{Env: map[string]string{"k": "v"}}, connectionIdentity{Env: map[string]string{"kv": ""}}},
		{"neighbouring strings", connectionIdentity{Command: "ab", URL: ""}, connectionIdentity{Command: "a", URL: "b"}},
		{"headers", connectionIdentity{Headers: map[string]string{"a": "b", "c": "d"}}, connectionIdentity{Headers: map[string]string{"a": "bc", "": "d"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if mustDigest(t, tc.a) == mustDigest(t, tc.b) {
				t.Errorf("distinct identities share a digest, so two different servers could share a client")
			}
		})
	}
}

func TestWriteCanonicalRefusesAKindItCannotEncode(t *testing.T) {
	type unsupported struct{ Ratio float64 }
	err := writeCanonical(io.Discard, reflect.ValueOf(unsupported{Ratio: 1.5}))
	if err == nil || !strings.Contains(err.Error(), "float64") {
		t.Fatalf("writeCanonical = %v, want an error naming the kind", err)
	}
}
