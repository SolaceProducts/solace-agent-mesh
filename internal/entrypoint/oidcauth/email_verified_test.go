// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import "testing"

func TestEmailVerified_StrictString(t *testing.T) {
	cases := map[string]bool{
		"true":  true,
		"True":  true,
		"TRUE":  true,
		"false": false,
		"False": false,
		"0":     false,
		"no":    false,
		"":      false,
	}
	for v, want := range cases {
		if got := emailVerified(map[string]any{"email_verified": v}); got != want {
			t.Errorf("emailVerified(%q) = %v, want %v", v, got, want)
		}
	}
	if !emailVerified(map[string]any{"email_verified": " true "}) {
		t.Error("surrounding whitespace should be trimmed before comparison")
	}
	if !emailVerified(map[string]any{"email_verified": true}) {
		t.Error("bool true should be verified")
	}
	if emailVerified(map[string]any{"email_verified": false}) {
		t.Error("bool false should not be verified")
	}
	if !emailVerified(map[string]any{}) {
		t.Error("absent claim should fail open (verified) — many IdPs omit it")
	}
}

func TestEmailVerified_PresentNonBoolNotVerified(t *testing.T) {
	cases := map[string]any{
		"numeric zero": float64(0),
		"numeric one":  float64(1),
		"json null":    nil,
		"object":       map[string]any{},
		"string array": []any{"true"},
	}
	for name, v := range cases {
		if emailVerified(map[string]any{"email_verified": v}) {
			t.Errorf("%s: present non-bool/string claim must NOT be verified", name)
		}
	}
}
