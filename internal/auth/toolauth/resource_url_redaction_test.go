// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package toolauth

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResourceURL_CredentialIsRedactedOnTheSuccessPath(t *testing.T) {
	const canary = "sk-live-MUST-NOT-APPEAR"

	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 "https://auth.example.com",
				"authorization_endpoint": "https://auth.example.com/authorize",
				"token_endpoint":         "https://auth.example.com/token",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer authSrv.Close()

	resSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              r.Host,
				"authorization_servers": []string{authSrv.URL},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer resSrv.Close()

	credentialed := func(raw string) string {
		return strings.Replace(raw, "http://", "http://svc:"+canary+"@", 1)
	}

	for _, tc := range []struct{ name, resourceURL string }{
		{"userinfo", credentialed(resSrv.URL)},
		{"api key in query", resSrv.URL + "?api_key=" + canary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearDiscoveryCacheForTest(t)

			var logs bytes.Buffer
			h := NewAuthHandler(&AuthConfig{
				Type:        "oauth2",
				ResourceURL: tc.resourceURL,
			}, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))

			if _, err := h.ensureScheme(context.Background()); err != nil {
				t.Fatalf("discovery did not succeed, so the INFO line this test exists for never ran: %v", err)
			}

			got := logs.String()
			if !strings.Contains(got, "oauth2 metadata discovered") {
				t.Fatalf("the INFO line never fired, so this asserts nothing: %s", got)
			}
			if strings.Contains(got, canary) {
				t.Errorf("logs carry the credential: %s", got)
			}
			if !strings.Contains(got, "resource_url") {
				t.Errorf("resource_url field disappeared — redaction must mask it, not drop the field: %s", got)
			}
		})
	}
}

func TestResourceURL_CredentialIsRedactedOnTheFailurePath(t *testing.T) {
	const canary = "sk-live-MUST-NOT-APPEAR"

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer dead.Close()

	clearDiscoveryCacheForTest(t)

	var logs bytes.Buffer
	h := NewAuthHandler(&AuthConfig{
		Type:        "oauth2",
		ResourceURL: strings.Replace(dead.URL, "http://", "http://svc:"+canary+"@", 1) + "/sse",
	}, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))

	_, err := h.ensureScheme(context.Background())
	if err == nil {
		t.Fatal("discovery unexpectedly succeeded, so the error wraps this test exists for never ran")
	}
	if strings.Contains(err.Error(), canary) {
		t.Errorf("discovery error carries the credential: %v", err)
	}
	if strings.Contains(logs.String(), canary) {
		t.Errorf("logs carry the credential: %s", logs.String())
	}
}
