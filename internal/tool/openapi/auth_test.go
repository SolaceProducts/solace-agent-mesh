// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package openapi

import (
	"net/http"
	"testing"
)

func TestApplyAuth(t *testing.T) {
	tests := []struct {
		name       string
		auth       *AuthConfig
		headers    map[string]string
		checkKey   string
		checkValue string
	}{
		{
			name:       "bearer token",
			auth:       &AuthConfig{Type: AuthTypeBearer, Token: "tok123"},
			checkKey:   "Authorization",
			checkValue: "Bearer tok123",
		},
		{
			name:       "api_key default header",
			auth:       &AuthConfig{Type: AuthTypeAPIKey, APIKey: "key456"},
			checkKey:   "X-API-Key",
			checkValue: "key456",
		},
		{
			name:       "api_key custom header",
			auth:       &AuthConfig{Type: AuthTypeAPIKey, APIKey: "key789", HeaderName: "X-Custom-Key"},
			checkKey:   "X-Custom-Key",
			checkValue: "key789",
		},
		{
			name:       "basic auth",
			auth:       &AuthConfig{Type: AuthTypeBasic, Username: "user", Password: "pass"},
			checkKey:   "Authorization",
			checkValue: "Basic dXNlcjpwYXNz",
		},
		{
			name:       "static headers",
			auth:       nil,
			headers:    map[string]string{"X-Trace": "abc"},
			checkKey:   "X-Trace",
			checkValue: "abc",
		},
		{
			name:       "nil auth no panic",
			auth:       nil,
			checkKey:   "",
			checkValue: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "http://example.com", nil)
			applyAuth(req, tt.auth, tt.headers)
			if tt.checkKey != "" {
				got := req.Header.Get(tt.checkKey)
				if got != tt.checkValue {
					t.Errorf("header %q = %q, want %q", tt.checkKey, got, tt.checkValue)
				}
			}
		})
	}
}

func TestApplyAPIKey_InQuery(t *testing.T) {
	auth := &AuthConfig{Type: AuthTypeAPIKey, HeaderName: "api_key", APIKey: "k123", In: "query"}
	req, _ := http.NewRequest("GET", "http://example.com/foo", nil)
	applyAuth(req, auth, nil)

	if got := req.URL.Query().Get("api_key"); got != "k123" {
		t.Errorf("query api_key = %q, want k123", got)
	}
	if got := req.Header.Get("api_key"); got != "" {
		t.Errorf("header api_key should be empty when In=query, got %q", got)
	}
}

func TestApplyAPIKey_InCookie(t *testing.T) {
	auth := &AuthConfig{Type: AuthTypeAPIKey, HeaderName: "session_key", APIKey: "c456", In: "cookie"}
	req, _ := http.NewRequest("GET", "http://example.com/foo", nil)
	applyAuth(req, auth, nil)

	if got := req.Header.Get("Cookie"); got != "session_key=c456" {
		t.Errorf("Cookie = %q, want session_key=c456", got)
	}
	if got := req.Header.Get("session_key"); got != "" {
		t.Errorf("header session_key should be empty when In=cookie, got %q", got)
	}
}

func TestApplyAPIKey_InHeader_Default(t *testing.T) {
	auth := &AuthConfig{Type: AuthTypeAPIKey, HeaderName: "X-API-Key", APIKey: "h789"}
	req, _ := http.NewRequest("GET", "http://example.com/foo", nil)
	applyAuth(req, auth, nil)

	if got := req.Header.Get("X-API-Key"); got != "h789" {
		t.Errorf("header X-API-Key = %q, want h789", got)
	}
}
