// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"crypto/elliptic"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestKeyOriginString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		origin KeyOrigin
		want   string
	}{
		{KeyOriginEphemeral, "ephemeral"},
		{KeyOriginSeedDerived, "seed-derived"},
		{KeyOriginPersistedLoaded, "persisted-loaded"},
		{KeyOriginPersistedGenerated, "persisted-generated"},
		{KeyOriginEphemeralDegraded, "ephemeral-degraded"},
		{KeyOrigin(99), "ephemeral"},
	}
	for _, tt := range tests {
		if got := tt.origin.String(); got != tt.want {
			t.Errorf("KeyOrigin(%d).String() = %q, want %q", tt.origin, got, tt.want)
		}
	}
}

func TestNewKeyManager(t *testing.T) {
	t.Parallel()
	km, err := NewKeyManager("test-component")
	if err != nil {
		t.Fatal(err)
	}

	kp := km.CurrentKeyPair()
	if kp == nil {
		t.Fatal("key pair should not be nil")
	}
	if kp.PrivateKey == nil || kp.PublicKey == nil {
		t.Fatal("keys should not be nil")
	}
	if kp.PublicKey.Curve != elliptic.P256() {
		t.Error("expected P-256 curve")
	}
	if !strings.HasPrefix(kp.KeyID, "test-component-key-") {
		t.Errorf("key ID %q should have correct prefix", kp.KeyID)
	}
}

func TestKeyManagerJWKS(t *testing.T) {
	t.Parallel()
	km, err := NewKeyManager("gw-01")
	if err != nil {
		t.Fatal(err)
	}

	jwks := km.JWKS()
	if jwks == nil {
		t.Fatal("JWKS should not be nil")
	}

	keys, ok := jwks["keys"].([]any)
	if !ok || len(keys) != 1 {
		t.Fatalf("JWKS should have exactly 1 key, got %v", jwks["keys"])
	}

	key := keys[0].(map[string]any)
	if key["kty"] != "EC" {
		t.Errorf("kty = %v, want EC", key["kty"])
	}
	if key["crv"] != "P-256" {
		t.Errorf("crv = %v, want P-256", key["crv"])
	}
	if key["alg"] != "ES256" {
		t.Errorf("alg = %v, want ES256", key["alg"])
	}
	if key["use"] != "sig" {
		t.Errorf("use = %v, want sig", key["use"])
	}

	xStr := key["x"].(string)
	yStr := key["y"].(string)
	if strings.Contains(xStr, "=") || strings.Contains(yStr, "=") {
		t.Error("base64url coordinates should not contain padding")
	}

	xBytes, err := base64.RawURLEncoding.DecodeString(xStr)
	if err != nil {
		t.Fatalf("decode x: %v", err)
	}
	yBytes, err := base64.RawURLEncoding.DecodeString(yStr)
	if err != nil {
		t.Fatalf("decode y: %v", err)
	}
	if len(xBytes) != 32 {
		t.Errorf("x coordinate length = %d, want 32", len(xBytes))
	}
	if len(yBytes) != 32 {
		t.Errorf("y coordinate length = %d, want 32", len(yBytes))
	}

	kp := km.CurrentKeyPair()
	if key["kid"] != kp.KeyID {
		t.Errorf("kid = %v, want %v", key["kid"], kp.KeyID)
	}
}

func TestKeyManagerRotate(t *testing.T) {
	t.Parallel()
	km, err := NewKeyManager("rotate-test")
	if err != nil {
		t.Fatal(err)
	}

	kp1 := km.CurrentKeyPair()
	kp2, err := km.RotateKey()
	if err != nil {
		t.Fatal(err)
	}

	if kp1.KeyID == kp2.KeyID {
		t.Error("rotated key should have different ID")
	}

	current := km.CurrentKeyPair()
	if current.KeyID != kp2.KeyID {
		t.Error("current key should be the rotated key")
	}
}

func TestKeyManagerRotateRetainsPreviousKey(t *testing.T) {
	t.Parallel()
	km, err := NewKeyManager("retain-test")
	if err != nil {
		t.Fatal(err)
	}
	kp1 := km.CurrentKeyPair()
	kp2, err := km.RotateKey()
	if err != nil {
		t.Fatal(err)
	}

	jwks := km.JWKS()
	keys := jwks["keys"].([]any)
	if len(keys) != 2 {
		t.Fatalf("JWKS should carry 2 keys (current + retired), got %d", len(keys))
	}

	kids := map[string]bool{}
	for _, k := range keys {
		kids[k.(map[string]any)["kid"].(string)] = true
	}
	if !kids[kp1.KeyID] {
		t.Errorf("retired kid %s missing from JWKS", kp1.KeyID)
	}
	if !kids[kp2.KeyID] {
		t.Errorf("current kid %s missing from JWKS", kp2.KeyID)
	}
}

func TestKeyManagerPruneRetained(t *testing.T) {
	t.Parallel()
	km, err := NewKeyManager("prune-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := km.RotateKey(); err != nil {
		t.Fatal(err)
	}

	dropped := km.PruneRetired(time.Now(), 10*time.Second)
	if dropped != 0 {
		t.Errorf("expected 0 pruned within overlap, got %d", dropped)
	}
	if got := len(km.JWKS()["keys"].([]any)); got != 2 {
		t.Errorf("expected 2 keys in JWKS, got %d", got)
	}

	dropped = km.PruneRetired(time.Now().Add(time.Minute), time.Second)
	if dropped != 1 {
		t.Errorf("expected 1 pruned past overlap, got %d", dropped)
	}
	if got := len(km.JWKS()["keys"].([]any)); got != 1 {
		t.Errorf("expected 1 key in JWKS post-prune, got %d", got)
	}
}

func TestKeyManagerPruneZeroOverlapDropsImmediately(t *testing.T) {
	t.Parallel()
	km, err := NewKeyManager("zero-overlap-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := km.RotateKey(); err != nil {
		t.Fatal(err)
	}
	dropped := km.PruneRetired(time.Now(), 0)
	if dropped != 1 {
		t.Errorf("expected 1 pruned with zero overlap, got %d", dropped)
	}
}

func TestKeyManagerPruneMultipleRotations(t *testing.T) {
	t.Parallel()
	km, err := NewKeyManager("multi-rotate")
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := km.RotateKey(); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(km.JWKS()["keys"].([]any)); got != 4 {
		t.Errorf("expected 4 keys (1 current + 3 retired) before prune, got %d", got)
	}
	dropped := km.PruneRetired(time.Now().Add(time.Hour), time.Second)
	if dropped != 3 {
		t.Errorf("expected 3 pruned, got %d", dropped)
	}
	if got := len(km.JWKS()["keys"].([]any)); got != 1 {
		t.Errorf("expected only current key after prune, got %d", got)
	}
}
