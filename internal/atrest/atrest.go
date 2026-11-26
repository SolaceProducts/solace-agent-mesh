// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package atrest

import (
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"

	identity "github.com/SolaceProducts/solace-agent-mesh/internal/agentidentity"
)

type Box struct {
	key []byte
}

func New(secret, purpose string) (*Box, error) {
	if purpose == "" {
		return nil, errors.New("atrest: purpose is required")
	}
	if secret == "" {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("atrest: random key: %w", err)
		}
		return &Box{key: key}, nil
	}
	key, err := hkdf.Key(sha256.New, []byte(secret), nil, "sam-atrest-v1|"+purpose, 32)
	if err != nil {
		return nil, fmt.Errorf("atrest: derive key: %w", err)
	}
	return &Box{key: key}, nil
}

func SessionSecret(configured string) string {
	if configured != "" {
		return configured
	}
	return os.Getenv("SESSION_SECRET_KEY")
}

const NoSecretWarning = "No session secret configured: login records use a key only this process knows, " +
	"so a restart, or a login step that reaches another pod, makes the user log in again"

func (b *Box) Seal(plaintext []byte) (string, error) {
	ct, err := identity.Encrypt(b.key, plaintext)
	if err != nil {
		return "", fmt.Errorf("atrest: seal: %w", err)
	}
	return base64.StdEncoding.EncodeToString(ct), nil
}

func (b *Box) Open(sealed string) ([]byte, error) {
	ct, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return nil, fmt.Errorf("atrest: decode: %w", err)
	}
	pt, err := identity.Decrypt(b.key, ct)
	if err != nil {
		return nil, fmt.Errorf("atrest: open: %w", err)
	}
	return pt, nil
}

func Hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
