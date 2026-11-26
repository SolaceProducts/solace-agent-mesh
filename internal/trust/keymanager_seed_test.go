// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestKeyManagerWithSeed_Deterministic(t *testing.T) {
	t.Parallel()
	seed := []byte("a-stable-session-secret-key-value")

	km1, err := NewKeyManagerWithSeed("gw-webui", seed)
	if err != nil {
		t.Fatal(err)
	}
	km2, err := NewKeyManagerWithSeed("gw-webui", seed)
	if err != nil {
		t.Fatal(err)
	}

	kp1, kp2 := km1.CurrentKeyPair(), km2.CurrentKeyPair()
	if !kp1.PrivateKey.Equal(kp2.PrivateKey) {
		t.Error("same seed + componentID must derive the same private key across restarts")
	}
	if !kp1.PublicKey.Equal(kp2.PublicKey) {
		t.Error("same seed + componentID must derive the same public key across restarts")
	}
	if kp1.KeyID != kp2.KeyID {
		t.Errorf("kid must be stable across restarts: %q vs %q", kp1.KeyID, kp2.KeyID)
	}
}

func TestKeyManagerWithSeed_DomainSeparation(t *testing.T) {
	t.Parallel()
	seed := []byte("shared-process-seed")

	a, err := NewKeyManagerWithSeed("gw-webui", seed)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewKeyManagerWithSeed("gw-mcp", seed)
	if err != nil {
		t.Fatal(err)
	}

	if a.CurrentKeyPair().PublicKey.Equal(b.CurrentKeyPair().PublicKey) {
		t.Error("different componentIDs must derive different keys from the same seed")
	}
	if a.CurrentKeyPair().KeyID == b.CurrentKeyPair().KeyID {
		t.Error("different keys must have different kids")
	}
}

func TestKeyManagerWithSeed_EmptySeedIsRandom(t *testing.T) {
	t.Parallel()

	a, err := NewKeyManagerWithSeed("gw", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewKeyManagerWithSeed("gw", nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.CurrentKeyPair().PublicKey.Equal(b.CurrentKeyPair().PublicKey) {
		t.Error("empty seed must fall back to random generation; keys should differ")
	}
}

func TestKeyManagerWithSeed_SurvivesRestart(t *testing.T) {
	t.Parallel()
	seed := []byte("session-secret-that-outlives-restart")
	const id = "gw-webui"

	kmBefore, err := NewKeyManagerWithSeed(id, seed)
	if err != nil {
		t.Fatal(err)
	}
	token, err := NewSigner(kmBefore, id, time.Hour, time.Hour).Sign(SignClaims{Subject: "user@example.com"})
	if err != nil {
		t.Fatal(err)
	}

	kmAfter, err := NewKeyManagerWithSeed(id, seed)
	if err != nil {
		t.Fatal(err)
	}
	pub := NewSigner(kmAfter, id, time.Hour, time.Hour).PublicKey()

	parsed, err := jwt.Parse(token, func(*jwt.Token) (any, error) { return pub, nil })
	if err != nil {
		t.Fatalf("token signed before restart must verify after restart: %v", err)
	}
	if !parsed.Valid {
		t.Error("token should be valid after restart")
	}
}
