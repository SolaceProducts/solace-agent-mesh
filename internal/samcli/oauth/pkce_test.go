// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"testing"
)

var pkceVerifierAlphabet = regexp.MustCompile(`^[A-Za-z0-9_\-]+$`)

func TestGeneratePKCE_ChallengeMatchesSHA256OfVerifier(t *testing.T) {
	v, c, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE: %v", err)
	}
	if !pkceVerifierAlphabet.MatchString(v) {
		t.Errorf("verifier %q contains characters outside the RFC 7636 unreserved set", v)
	}
	if len(v) != 43 {
		t.Errorf("verifier length = %d; want 43 (32 bytes -> 43 chars rawurl)", len(v))
	}
	want := sha256.Sum256([]byte(v))
	if got := base64.RawURLEncoding.EncodeToString(want[:]); got != c {
		t.Errorf("challenge = %q; want %q (sha256(verifier) base64url)", c, got)
	}
}

func TestGeneratePKCE_VerifiersAreUnique(t *testing.T) {
	v1, _, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE: %v", err)
	}
	v2, _, err := GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE: %v", err)
	}
	if v1 == v2 {
		t.Fatalf("verifiers collide between consecutive calls; randomness is broken")
	}
}

func TestChallengeFromVerifier_KnownVector(t *testing.T) {
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	want := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := ChallengeFromVerifier(verifier); got != want {
		t.Errorf("ChallengeFromVerifier(rfc-vector) = %q; want %q", got, want)
	}
}

func TestGenerateState(t *testing.T) {
	s, err := GenerateState()
	if err != nil {
		t.Fatalf("GenerateState: %v", err)
	}
	if !pkceVerifierAlphabet.MatchString(s) {
		t.Errorf("state %q contains characters outside base64url alphabet", s)
	}
	if len(s) != 22 {
		t.Errorf("state length = %d; want 22 (16 bytes rawurl)", len(s))
	}
}
