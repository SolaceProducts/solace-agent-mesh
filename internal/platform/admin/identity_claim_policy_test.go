// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
)

func TestCanonicalFormIsPinned(t *testing.T) {
	t.Run("phone", func(t *testing.T) {
		cases := []struct{ name, in, want string }{
			{"e164 claim", "+15551234567", "15551234567"},
			{"wa_id form", "15551234567", "15551234567"},
			{"spaces and dashes", "+1 555-123-4567", "15551234567"},
			{"parenthesised", "+1 (555) 123-4567", "15551234567"},
			{"padded", "  +15551234567  ", "15551234567"},
			{"rfc3966 extension is cut, not folded in", "+15551234567;ext=5678", "15551234567"},
			{"free-text extension is cut", "+1 (555) 010-1234 ext. 9", "15550101234"},
			{"x extension is cut", "+15550101234 x9", "15550101234"},
			{"hash extension is cut", "+15550101234#9", "15550101234"},
			{"idd prefix is dropped", "0044 20 7946 0958", "442079460958"},
			{"arabic-indic digits fold to ascii", "٩٦٥١٢٣٤٥٦٧", "9651234567"},
			{"devanagari digits fold to ascii", "१५५५१२३४५६७", "15551234567"},
			{"national form stays distinct from international", "555-123-4567", "5551234567"},
			{"no digits", "not a phone", ""},
			{"empty", "", ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := canonicalPhoneValue(tc.in); got != tc.want {
					t.Errorf("canonicalPhoneValue(%q) = %q, want %q", tc.in, got, tc.want)
				}
			})
		}
	})

	t.Run("claim and wa_id spellings converge", func(t *testing.T) {
		if got, want := canonicalPhoneValue("+15551234567"), canonicalPhoneValue("15551234567"); got != want {
			t.Fatalf("E.164 claim canonicalized to %q but the wa_id form to %q", got, want)
		}
	})

	t.Run("provider-divergent spellings are projected alongside the claim", func(t *testing.T) {
		for _, tc := range []struct{ name, in, want string }{
			{"mexico keeps its legacy post-+52 one", "5215551234567", "525551234567"},
			{"mexico gains it", "525551234567", "5215551234567"},
			{"brazil loses its ninth digit", "5511987654321", "551187654321"},
			{"brazil gains it", "551187654321", "5511987654321"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				alts := alternatePhoneForms(tc.in)
				if len(alts) != 1 || alts[0] != tc.want {
					t.Errorf("alternatePhoneForms(%q) = %v, want [%q]", tc.in, alts, tc.want)
				}
			})
		}
		if alts := alternatePhoneForms("15551234567"); len(alts) != 0 {
			t.Errorf("a NANP number got alternates %v; only MX and BR diverge", alts)
		}
		if alts := alternatePhoneForms("551132654321"); len(alts) != 0 {
			t.Errorf("a Brazilian landline got alternates %v; only mobiles diverge", alts)
		}
		if alts := alternatePhoneForms("5511932654321"); len(alts) != 0 {
			t.Errorf("a 13-digit number whose subscriber part is not a mobile got alternates %v", alts)
		}
	})

	t.Run("implausible values are recognized", func(t *testing.T) {
		for _, tc := range []struct {
			in   string
			want bool
		}{
			{"15551234567", true},
			{"442079460958", true},
			{"123456", false},
			{"1234567890123456", false},
		} {
			if got := plausiblePhoneValue(tc.in); got != tc.want {
				t.Errorf("plausiblePhoneValue(%q) = %v, want %v", tc.in, got, tc.want)
			}
		}
	})

	t.Run("email", func(t *testing.T) {
		cases := []struct{ name, in, want string }{
			{"lowercased", "Person@Example.COM", "person@example.com"},
			{"padded", "  person@example.com ", "person@example.com"},
			{"already canonical", "person@example.com", "person@example.com"},
			{"empty", "", ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := canonicalEmailValue(tc.in); got != tc.want {
					t.Errorf("canonicalEmailValue(%q) = %q, want %q", tc.in, got, tc.want)
				}
			})
		}
	})

	t.Run("text", func(t *testing.T) {
		cases := []struct{ name, in, want string }{
			{"case folded", "AliceSmith", "alicesmith"},
			{"mixed case", "aLiCe.sMiTh", "alice.smith"},
			{"padded", "  alice  ", "alice"},
			{"sharp s folds to ss", "Straße", "strasse"},
			{"empty", "", ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				if got := canonicalTextValue(tc.in); got != tc.want {
					t.Errorf("canonicalTextValue(%q) = %q, want %q", tc.in, got, tc.want)
				}
			})
		}
	})

	t.Run("composed and decomposed spellings converge", func(t *testing.T) {
		const composed = "René"
		const decomposed = "René"
		if composed == decomposed {
			t.Fatal("the two spellings are byte-identical; an editor normalized this file and the test now proves nothing")
		}
		if got, want := canonicalTextValue(composed), canonicalTextValue(decomposed); got != want {
			t.Fatalf("composed canonicalized to %q but decomposed to %q", got, want)
		}
	})
}

func TestGuardAndPolicySelectOnTheSameFold(t *testing.T) {
	t.Parallel()
	for _, spelling := range []string{"phone_number", "Phone_Number", "PHONE_NUMBER", " phone_number ", "phone_numbers", "phonenumber", "email"} {
		p, ok := policyForClaimKey(spelling)
		if !ok {
			t.Fatalf("%q is not admissible", spelling)
		}
		policySaysPhone := p.canonical("+1 (555) 123-4567") == "15551234567"
		guardSaysPhone := identity.SameClaimKey(spelling, ClaimKeyPhoneNumber)
		if policySaysPhone != guardSaysPhone {
			t.Errorf("%q: policy selects phone canonicalizer = %v, guard treats as phone_number = %v — the two must agree", spelling, policySaysPhone, guardSaysPhone)
		}
	}
}

func TestPolicyForClaimKey(t *testing.T) {
	t.Run("phone_number does not require its verification claim to be present", func(t *testing.T) {
		p, ok := policyForClaimKey(ClaimKeyPhoneNumber)
		if !ok {
			t.Fatal("phone_number is not admissible; it is a built-in key")
		}
		if !p.absentIsVerified {
			t.Error("an absent phone_number_verified is refused; a provider that never sends one has every user turned away")
		}
		if p.verificationClaim != "phone_number_verified" {
			t.Errorf("verificationClaim = %q, want phone_number_verified", p.verificationClaim)
		}
	})

	t.Run("email treats an absent verification claim as verified", func(t *testing.T) {
		p, ok := policyForClaimKey(ClaimKeyEmail)
		if !ok {
			t.Fatal("email is not admissible; it is a built-in key")
		}
		if !p.absentIsVerified {
			t.Error("an absent email_verified is refused; oidcauth.emailVerified accepts it, and the two must not drift")
		}
	})

	t.Run("an empty key is refused", func(t *testing.T) {
		if _, ok := policyForClaimKey(""); ok {
			t.Error("an empty claim key resolved to a policy")
		}
	})

	t.Run("a claim key with no built-in meaning gets the text form", func(t *testing.T) {
		const key = "extension_deadbeef_waUsername"
		p, ok := policyForClaimKey(key)
		if !ok {
			t.Fatal("a named custom key was refused")
		}
		if got := p.canonical("AliceSmith"); got != "alicesmith" {
			t.Errorf("custom key canonical(%q) = %q, want the case-folded form", "AliceSmith", got)
		}
		if p.verificationClaim != key+"_verified" {
			t.Errorf("verificationClaim = %q, want the conventional companion", p.verificationClaim)
		}
	})

	t.Run("a phone number reached through a custom key cannot match a wa_id", func(t *testing.T) {
		const idpHolds = "+1 555-123-4567"
		const waID = "15551234567"

		phone, ok := policyForClaimKey(ClaimKeyPhoneNumber)
		if !ok {
			t.Fatal("phone_number is not admissible; it is a built-in key")
		}
		if phone.canonical(idpHolds) != phone.canonical(waID) {
			t.Fatalf("phone_number canonicalized %q and %q apart (%q vs %q); the phone lookup is broken outright",
				idpHolds, waID, phone.canonical(idpHolds), phone.canonical(waID))
		}

		const custom = "extension_deadbeef_waPhone"
		text, ok := policyForClaimKey(custom)
		if !ok {
			t.Fatal("a named custom key was refused")
		}
		if text.canonical(idpHolds) == text.canonical(waID) {
			t.Fatal("a custom key now canonicalizes phone numbers; the phone-lookup pairing refused in parseIdentityLookup and validateWhatsAppValues may no longer need to be")
		}
	})

	t.Run("a custom key does not consult the phone verification claim", func(t *testing.T) {
		const custom = "extension_deadbeef_waPhone"
		p, ok := policyForClaimKey(custom)
		if !ok {
			t.Fatal("an allowlisted key was refused")
		}
		if p.verificationClaim == ClaimKeyPhoneNumber+"_verified" {
			t.Fatal("a custom key now consults phone_number_verified; the verification half of the pairing refusal is moot")
		}
		denied := map[string]any{
			custom:                  "+15551234567",
			ClaimKeyPhoneNumber:     "+15551234567",
			"phone_number_verified": false,
		}
		if !claimVerified(denied, p).verified() {
			t.Error("a custom key read phone_number_verified; it has no companion claim of its own in this set")
		}
		phone, _ := policyForClaimKey(ClaimKeyPhoneNumber)
		if claimVerified(denied, phone).verified() {
			t.Error("phone_number ignored an explicit phone_number_verified: false")
		}
	})

	t.Run("an empty key is never admissible", func(t *testing.T) {
		if _, ok := policyForClaimKey(""); ok {
			t.Error("the empty claim key resolved to a policy")
		}
	})
}
