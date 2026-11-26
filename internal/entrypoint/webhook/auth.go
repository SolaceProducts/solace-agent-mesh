// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"hash"
	"net/http"
	"strings"
)

var errUnauthorized = errors.New("unauthorized")

func verifyToken(t *TokenAuthConfig, r *http.Request) error {
	var presented string
	switch strings.ToLower(t.Location) {
	case "", "bearer":
		presented = bearerToken(r.Header.Get("Authorization"))
	case "header":
		presented = r.Header.Get(t.Name)
	case "query":
		presented = r.URL.Query().Get(t.Name)
	default:
		return errUnauthorized
	}
	if presented == "" || subtle.ConstantTimeCompare([]byte(presented), []byte(t.Value)) != 1 {
		return errUnauthorized
	}
	return nil
}

func verifyHMAC(h *HMACAuthConfig, r *http.Request, body []byte) error {
	headerVal := r.Header.Get(h.Header)
	if headerVal == "" {
		return errUnauthorized
	}
	if h.Prefix != "" && !strings.HasPrefix(headerVal, h.Prefix) {
		return errUnauthorized
	}
	headerVal = strings.TrimPrefix(headerVal, h.Prefix)

	var presented []byte
	var err error
	switch strings.ToLower(h.Encoding) {
	case "", "hex":
		presented, err = hex.DecodeString(headerVal)
	case "base64":
		presented, err = base64.StdEncoding.DecodeString(headerVal)
	case "base64url":
		presented, err = base64.URLEncoding.DecodeString(headerVal)
	case "base64raw":
		presented, err = base64.RawStdEncoding.DecodeString(headerVal)
	case "base64rawurl":
		presented, err = base64.RawURLEncoding.DecodeString(headerVal)
	default:
		return errUnauthorized
	}
	if err != nil {
		return errUnauthorized
	}

	mac := hmac.New(hashForAlgo(h.Algo), []byte(h.Secret))
	mac.Write(body)
	expected := mac.Sum(nil)

	if !hmac.Equal(presented, expected) {
		return errUnauthorized
	}
	return nil
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return parts[1]
}

func hashForAlgo(algo string) func() hash.Hash {
	switch strings.ToLower(algo) {
	case "sha512":
		return sha512.New
	default:
		return sha256.New
	}
}
