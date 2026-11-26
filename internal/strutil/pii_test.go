// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package strutil

import "testing"

func TestPIIFingerprint_IsStableAndCaseInsensitive(t *testing.T) {
	const lower = "alice@example.com"

	got := PIIFingerprint(lower)
	if got == "" {
		t.Fatal("fingerprint of a non-empty value is empty")
	}
	if got == lower {
		t.Fatal("fingerprint returned the raw value")
	}
	if again := PIIFingerprint(lower); again != got {
		t.Errorf("fingerprint is not stable: %q then %q", got, again)
	}
	for _, variant := range []string{"Alice@Example.com", "ALICE@EXAMPLE.COM"} {
		if PIIFingerprint(variant) != got {
			t.Errorf("PIIFingerprint(%q) = %q, want %q — case must not change the digest",
				variant, PIIFingerprint(variant), got)
		}
	}
}

func TestPIIFingerprint_EmptyValueHasNoDigest(t *testing.T) {
	if got := PIIFingerprint(""); got != "" {
		t.Errorf("PIIFingerprint(\"\") = %q, want \"\"", got)
	}
}

func TestPIIFingerprint_DistinguishesValues(t *testing.T) {
	if PIIFingerprint("alice@example.com") == PIIFingerprint("bob@example.com") {
		t.Error("two different addresses produced the same fingerprint")
	}
}
