// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import "testing"

func TestSameClaimKeyFoldsLikeThePolicy(t *testing.T) {
	t.Parallel()

	same := []string{"phone_number", "Phone_Number", "PHONE_NUMBER", "  phone_number  ", "phone_numbeR"}
	for _, s := range same {
		if !SameClaimKey(s, "phone_number") {
			t.Errorf("SameClaimKey(%q, phone_number) = false; the policy would select the phone canonicalizer for it", s)
		}
	}

	different := []string{"phone", "phone_number_verified", "phonenumber", "phone-number", ""}
	for _, s := range different {
		if SameClaimKey(s, "phone_number") {
			t.Errorf("SameClaimKey(%q, phone_number) = true; these are distinct claims", s)
		}
	}
}

func TestCanonicalTextUsesFullCaseFolding(t *testing.T) {
	t.Parallel()
	if got, want := CanonicalText("Straße"), "strasse"; got != want {
		t.Errorf("CanonicalText(Straße) = %q, want %q", got, want)
	}
	composed, decomposed := "José", "José"
	if composed == decomposed {
		t.Fatal("fixture: the two spellings must differ before folding")
	}
	if CanonicalText(composed) != CanonicalText(decomposed) {
		t.Errorf("composed %q and decomposed %q do not converge", composed, decomposed)
	}
}
