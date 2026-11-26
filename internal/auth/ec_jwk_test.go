// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"testing"
)

type p256Point struct {
	key  *ecdsa.PublicKey
	x, y []byte
}

func findP256Point(t *testing.T, want func(x, y []byte) bool) p256Point {
	t.Helper()
	for k := 1; k < 1<<16; k++ {
		scalar := make([]byte, p256CoordinateLen)
		scalar[len(scalar)-2], scalar[len(scalar)-1] = byte(k>>8), byte(k)
		priv, err := ecdh.P256().NewPrivateKey(scalar)
		if err != nil {
			t.Fatal(err)
		}
		raw := priv.PublicKey().Bytes()
		x, y := raw[1:1+p256CoordinateLen], raw[1+p256CoordinateLen:]
		if !want(x, y) {
			continue
		}
		key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), raw)
		if err != nil {
			t.Fatal(err)
		}
		return p256Point{key: key, x: x, y: y}
	}
	t.Fatal("no scalar produced the requested point")
	return p256Point{}
}

func anyPoint(_, _ []byte) bool         { return true }
func leadingZeroX(x, _ []byte) bool     { return x[0] == 0 }
func leadingZeroY(_, y []byte) bool     { return y[0] == 0 }
func withZeroPrefix(b []byte) []byte    { return append([]byte{0}, b...) }
func withNonZeroPrefix(b []byte) []byte { return append([]byte{1}, b...) }
func trimZeros(b []byte) []byte         { return bytes.TrimLeft(b, "\x00") }
func flipLastBit(b []byte) []byte       { c := bytes.Clone(b); c[len(c)-1] ^= 1; return c }
func mustAccept(t *testing.T, p p256Point) func(name string, x, y []byte) {
	return func(name string, x, y []byte) {
		t.Helper()
		got, err := P256PublicKey(x, y)
		if err != nil {
			t.Errorf("%s: rejected: %v", name, err)
			return
		}
		if !got.Equal(p.key) {
			t.Errorf("%s: parsed a different key", name)
		}
	}
}

func TestP256PublicKey_AcceptsTheEncodingsTheOldConstructionDid(t *testing.T) {
	plain := findP256Point(t, anyPoint)
	shortX := findP256Point(t, leadingZeroX)
	shortY := findP256Point(t, leadingZeroY)

	mustAccept(t, plain)("full-length coordinates", plain.x, plain.y)
	mustAccept(t, shortX)("x with its leading zero dropped", trimZeros(shortX.x), shortX.y)
	mustAccept(t, shortY)("y with its leading zero dropped", shortY.x, trimZeros(shortY.y))
	mustAccept(t, plain)("sign-padded 33-byte x", withZeroPrefix(plain.x), plain.y)
	mustAccept(t, plain)("sign-padded 33-byte y", plain.x, withZeroPrefix(plain.y))
}

func TestP256PublicKey_RejectsInvalidPoints(t *testing.T) {
	p := findP256Point(t, anyPoint)
	for name, tc := range map[string]struct{ x, y []byte }{
		"off the curve":                    {p.x, flipLastBit(p.y)},
		"33-byte coordinate, nonzero lead": {withNonZeroPrefix(p.x), p.y},
		"empty coordinates":                {nil, nil},
	} {
		if _, err := P256PublicKey(tc.x, tc.y); err == nil {
			t.Errorf("%s: accepted, want an error", name)
		}
	}
}
