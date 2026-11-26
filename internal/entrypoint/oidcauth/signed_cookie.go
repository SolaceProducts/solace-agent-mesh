// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/hmacutil"
)

func signValue(value, secretKey string) string {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(value))
	sig := base64.RawURLEncoding.EncodeToString(hmacutil.Sum([]byte(secretKey), []byte(encoded)))
	return encoded + "." + sig
}

func verifySignedValue(signed, secretKey string) (string, error) {
	idx := strings.LastIndex(signed, ".")
	if idx < 0 {
		return "", errors.New("malformed signed cookie")
	}
	encoded, sig := signed[:idx], signed[idx+1:]

	sigBytes, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmacutil.Valid([]byte(secretKey), []byte(encoded), sigBytes) {
		return "", errors.New("signature mismatch")
	}

	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode cookie value: %w", err)
	}
	return string(raw), nil
}

type cookieOpts struct {
	Name     string
	MaxAge   int
	Secure   bool
	SameSite http.SameSite
}

func setSignedCookie(w http.ResponseWriter, opts cookieOpts, value, secretKey string) {
	http.SetCookie(w, &http.Cookie{
		Name:     opts.Name,
		Value:    signValue(value, secretKey),
		Path:     "/",
		MaxAge:   opts.MaxAge,
		HttpOnly: true,
		Secure:   opts.Secure,
		SameSite: opts.SameSite,
	})
}

func getSignedCookie(r *http.Request, name, secretKey string) (string, error) {
	c, err := r.Cookie(name)
	if err != nil {
		return "", err
	}
	return verifySignedValue(c.Value, secretKey)
}

func clearSignedCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
