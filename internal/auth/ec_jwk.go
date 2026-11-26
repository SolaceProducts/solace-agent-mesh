// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"fmt"
)

const (
	p256CoordinateLen   = 32
	uncompressedPointID = 0x04
)

func P256PublicKey(x, y []byte) (*ecdsa.PublicKey, error) {
	x, y = bytes.TrimLeft(x, "\x00"), bytes.TrimLeft(y, "\x00")
	if len(x) > p256CoordinateLen || len(y) > p256CoordinateLen {
		return nil, fmt.Errorf("P-256 coordinate longer than %d bytes", p256CoordinateLen)
	}
	point := make([]byte, 1+2*p256CoordinateLen)
	point[0] = uncompressedPointID
	copy(point[1+p256CoordinateLen-len(x):1+p256CoordinateLen], x)
	copy(point[1+2*p256CoordinateLen-len(y):], y)
	key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), point)
	if err != nil {
		return nil, fmt.Errorf("parse P-256 public key: %w", err)
	}
	return key, nil
}
