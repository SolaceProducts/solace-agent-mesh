// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/hmacutil"
)

func generateState() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func signState(state, secretKey string) string {
	sig := base64.RawURLEncoding.EncodeToString(hmacutil.Sum([]byte(secretKey), []byte(state)))
	return state + "." + sig
}

func verifySignedState(signed, secretKey string) (string, error) {
	parts := splitLast(signed, ".")
	if len(parts) != 2 {
		return "", errors.New("malformed state cookie")
	}
	state, sig := parts[0], parts[1]

	sigBytes, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmacutil.Valid([]byte(secretKey), []byte(state), sigBytes) {
		return "", errors.New("state signature mismatch")
	}
	return state, nil
}

func splitLast(s, sep string) []string {
	idx := strings.LastIndex(s, sep)
	if idx < 0 {
		return []string{s}
	}
	return []string{s[:idx], s[idx+len(sep):]}
}

const oidcStateCookieName = "sam_oidc_state"

func stateCookieFraming(secure bool) (http.SameSite, bool) {
	if !secure {
		return http.SameSiteLaxMode, false
	}
	return http.SameSiteNoneMode, true
}

func setOIDCStateCookie(w http.ResponseWriter, state, secretKey string, secure bool) {
	sameSite, partitioned := stateCookieFraming(secure)
	http.SetCookie(w, &http.Cookie{
		Name:        oidcStateCookieName,
		Value:       signState(state, secretKey),
		Path:        "/",
		MaxAge:      300,
		HttpOnly:    true,
		Secure:      secure,
		SameSite:    sameSite,
		Partitioned: partitioned,
	})
}

func getOIDCStateCookie(r *http.Request, secretKey string) (string, error) {
	cookie, err := r.Cookie(oidcStateCookieName)
	if err != nil {
		return "", fmt.Errorf("state cookie not found: %w", err)
	}
	return verifySignedState(cookie.Value, secretKey)
}

func clearOIDCStateCookie(w http.ResponseWriter, secure bool) {
	sameSite, partitioned := stateCookieFraming(secure)
	http.SetCookie(w, &http.Cookie{
		Name:        oidcStateCookieName,
		Value:       "",
		Path:        "/",
		MaxAge:      -1,
		HttpOnly:    true,
		Secure:      secure,
		SameSite:    sameSite,
		Partitioned: partitioned,
	})
}
