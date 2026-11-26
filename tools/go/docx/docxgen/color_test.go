// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package docxgen

import (
	"strings"
	"testing"
)

func TestNormalizeColorHex_AcceptsAndNormalizes(t *testing.T) {
	cases := map[string]string{
		"":         "",
		"C0392B":   "C0392B",
		"c0392b":   "C0392B",
		"#C0392B":  "C0392B",
		"  #abc  ": "AABBCC",
	}
	for in, want := range cases {
		got, err := NormalizeColorHex(in)
		if err != nil {
			t.Errorf("NormalizeColorHex(%q) errored: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("NormalizeColorHex(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeColorHex_ReportsTheCharacterTheCallerWrote(t *testing.T) {
	_, err := NormalizeColorHex("café1")
	if err == nil {
		t.Fatal("expected an error for a non-hex character")
	}
	if !strings.Contains(err.Error(), "é") {
		t.Errorf("message should name the offending character 'é', got: %v", err)
	}
	if strings.Contains(err.Error(), "Ã") {
		t.Errorf("message names a character the caller never wrote: %v", err)
	}
}

func TestNormalizeColorHex_Rejects(t *testing.T) {
	for _, in := range []string{"red", "C0392BAA", "12345", "#xyzxyz"} {
		if _, err := NormalizeColorHex(in); err == nil {
			t.Errorf("NormalizeColorHex(%q) should have been rejected", in)
		}
	}
}
