// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const pkceVerifierBytes = 32

func GeneratePKCE() (verifier, challenge string, err error) {
	v, err := randomBase64URL(pkceVerifierBytes)
	if err != nil {
		return "", "", fmt.Errorf("pkce: generate verifier: %w", err)
	}
	return v, ChallengeFromVerifier(v), nil
}

func ChallengeFromVerifier(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func GenerateState() (string, error) {
	s, err := randomBase64URL(16)
	if err != nil {
		return "", fmt.Errorf("pkce: generate state: %w", err)
	}
	return s, nil
}

func randomBase64URL(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
