// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package trust

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func p256Coordinates(t *testing.T) (*ecdsa.PublicKey, []byte, []byte) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := priv.PublicKey.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return &priv.PublicKey, raw[1:33], raw[33:]
}

func jwksWith(x, y []byte) map[string]any {
	enc := base64.RawURLEncoding.EncodeToString
	return map[string]any{"keys": []any{map[string]any{
		"kid": "k1", "kty": "EC", "crv": "P-256", "x": enc(x), "y": enc(y),
	}}}
}

func TestFindPublicKeyAcceptsASignPaddedCoordinate(t *testing.T) {
	want, x, y := p256Coordinates(t)

	got, err := findPublicKey(jwksWith(append([]byte{0}, x...), y), "k1")
	if err != nil {
		t.Fatalf("findPublicKey: %v", err)
	}
	if !got.Equal(want) {
		t.Error("resolved a different key")
	}
}

func TestFindPublicKeyRejectsAnOffCurvePoint(t *testing.T) {
	_, x, y := p256Coordinates(t)
	offCurve := bytes.Clone(y)
	offCurve[len(offCurve)-1] ^= 1

	if _, err := findPublicKey(jwksWith(x, offCurve), "k1"); err == nil {
		t.Fatal("off-curve JWK accepted, want an error")
	}
}
