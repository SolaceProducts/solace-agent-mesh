// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samcli

import (
	"strings"
	"testing"
)

func TestParseEnvFile_BasicKV(t *testing.T) {
	input := `# comment line
KEY1=value1
KEY2=value with spaces
EMPTY=
export EXPORTED=ok

# blank line above, trailing comment below
ANOTHER=val # not a comment, value includes hash
`
	entries, warnings := parseEnvFile(strings.NewReader(input), "test.env")
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	want := map[string]string{
		"KEY1":     "value1",
		"KEY2":     "value with spaces",
		"EMPTY":    "",
		"EXPORTED": "ok",
		"ANOTHER":  "val # not a comment, value includes hash",
	}
	got := map[string]string{}
	for _, e := range entries {
		got[e.key] = e.value
	}
	if len(got) != len(want) {
		t.Errorf("entry count = %d, want %d (got: %v)", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q = %q, want %q", k, got[k], v)
		}
	}
}

func TestParseEnvFile_MalformedLineWarns(t *testing.T) {
	input := `GOOD=ok
no_equals_sign_here
=missing_key
123STARTS_WITH_DIGIT=bad
`
	entries, warnings := parseEnvFile(strings.NewReader(input), "broken.env")
	if len(entries) != 1 || entries[0].key != "GOOD" {
		t.Errorf("expected one valid entry GOOD, got %v", entries)
	}
	if len(warnings) != 3 {
		t.Errorf("expected 3 warnings (no-equals, missing-key, bad-key), got %d: %v", len(warnings), warnings)
	}
}

func TestValidEnvKey(t *testing.T) {
	cases := map[string]bool{
		"FOO":      true,
		"_FOO":     true,
		"FOO_BAR":  true,
		"FOO123":   true,
		"_":        true,
		"":         false,
		"1FOO":     false,
		"FOO-BAR":  false,
		"FOO BAR":  false,
		"FOO.BAR":  false,
		"foo":      true,
		"FOO\nBAR": false,
	}
	for in, want := range cases {
		if got := validEnvKey(in); got != want {
			t.Errorf("validEnvKey(%q) = %v, want %v", in, got, want)
		}
	}
}
