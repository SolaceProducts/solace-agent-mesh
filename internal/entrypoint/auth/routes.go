// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
)

func RegisterAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/csrf-token", HandleCSRFToken)
	mux.Handle("POST /api/v1/auth/logout", httputil.RequireJSONMiddleware(http.HandlerFunc(handleLogout)))

	mux.HandleFunc("GET /api/v1/auth/login", handleNotImplemented)
	mux.HandleFunc("GET /api/v1/auth/callback", handleNotImplemented)
	mux.Handle("POST /api/v1/auth/refresh", httputil.RequireJSONMiddleware(http.HandlerFunc(handleNotImplemented)))
}

func HandleCSRFToken(w http.ResponseWriter, r *http.Request) {
	token, err := generateToken(32)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, ErrorServerConfiguration, "Failed to generate CSRF token")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "csrf_token",
		Value:    token,
		Path:     "/",
		MaxAge:   3600,
		HttpOnly: false,
		Secure:   servedOverHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})

	writeAuthJSON(w, http.StatusOK, map[string]any{
		"message":   "CSRF token set",
		"csrfToken": token,
	})
}

func servedOverHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func handleLogout(w http.ResponseWriter, _ *http.Request) {
	writeAuthJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Logged out successfully",
	})
}

func handleNotImplemented(w http.ResponseWriter, _ *http.Request) {
	writeAuthJSON(w, http.StatusNotImplemented, map[string]any{
		"message":           "Authentication not configured on this entrypoint.",
		"validationDetails": nil,
	})
}

func generateToken(nbytes int) (string, error) {
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func writeAuthJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
