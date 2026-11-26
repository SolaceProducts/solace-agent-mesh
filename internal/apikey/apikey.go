// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apikey

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	Marker = "samk_"

	secretBytes = 32

	DisplayPrefixLen = 8
)

func Generate() (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("apikey: read random bytes: %w", err)
	}
	return Marker + base64.RawURLEncoding.EncodeToString(buf), nil
}

func Hash(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

func DisplayPrefix(plaintext string) string {
	secret, ok := strings.CutPrefix(plaintext, Marker)
	if !ok || !isWellFormedSecret(secret) {
		return ""
	}
	return secret[:DisplayPrefixLen]
}

func isWellFormedSecret(s string) bool {
	if len(s) != base64.RawURLEncoding.EncodedLen(secretBytes) {
		return false
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(s)
	return err == nil && len(decoded) == secretBytes
}

func IsWellFormed(candidate string) bool {
	secret, ok := strings.CutPrefix(candidate, Marker)
	return ok && isWellFormedSecret(secret)
}

func BearerCredential(header string) (string, bool) {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return "", false
	}
	return strings.TrimSpace(token), true
}

func HasMarker(candidate string) bool {
	return strings.HasPrefix(candidate, Marker)
}
