// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

func verifyES256AgainstJWK(t *testing.T, encode func(x, y []byte) ([]byte, []byte)) error {
	t.Helper()
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	x, y := encode(ecXY(t, &ecKey.PublicKey))
	const kid = "ec-kid"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kty": "EC", "alg": "ES256", "use": "sig", "kid": kid, "crv": "P-256",
			"x": base64.RawURLEncoding.EncodeToString(x),
			"y": base64.RawURLEncoding.EncodeToString(y),
		}}})
	}))
	t.Cleanup(srv.Close)

	v, err := NewJWKSIDTokenVerifier("https://issuer.example", "client-1",
		func(context.Context) (string, error) { return srv.URL, nil }, srv.Client(), slog.Default())
	if err != nil {
		t.Fatalf("NewJWKSIDTokenVerifier: %v", err)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, validClaims("https://issuer.example", "client-1"))
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(ecKey)
	if err != nil {
		t.Fatalf("SignedString: %v", err)
	}
	_, err = v.VerifyIDToken(t.Context(), signed)
	return err
}

func TestVerifier_ES256AcceptsSignPaddedCoordinate(t *testing.T) {
	err := verifyES256AgainstJWK(t, func(x, y []byte) ([]byte, []byte) {
		return append([]byte{0}, x...), y
	})
	if err != nil {
		t.Fatalf("VerifyIDToken with a 33-byte zero-prefixed x: %v", err)
	}
}

func TestVerifier_ES256RejectsOffCurveKey(t *testing.T) {
	err := verifyES256AgainstJWK(t, func(x, y []byte) ([]byte, []byte) {
		offCurve := bytes.Clone(y)
		offCurve[len(offCurve)-1] ^= 1
		return x, offCurve
	})
	mustVerifyError(t, err, IDTokenFailKeyResolution)
}
