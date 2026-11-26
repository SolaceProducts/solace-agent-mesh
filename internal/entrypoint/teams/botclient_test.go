// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package teams

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func tokenResponse() io.ReadCloser {
	b, _ := json.Marshal(map[string]any{
		"access_token": "tok", "token_type": "Bearer", "expires_in": 3600,
	})
	return io.NopCloser(bytes.NewReader(b))
}

func TestFetchToken_SingleTenantURL(t *testing.T) {
	const tenant = "my-tenant-id"
	var capturedURL string

	bc := NewBotClient("app-id", "app-secret", tenant, teamsNoopLogger())
	bc.client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			capturedURL = r.URL.String()
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       tokenResponse(),
			}, nil
		}),
	}

	token, _, err := bc.fetchToken(t.Context())
	if err != nil {
		t.Fatalf("fetchToken: %v", err)
	}
	if token != "tok" {
		t.Errorf("token = %q, want %q", token, "tok")
	}

	wantPrefix := "https://login.microsoftonline.com/" + tenant + "/oauth2/v2.0/token"
	if !strings.HasPrefix(capturedURL, wantPrefix) {
		t.Errorf("token URL = %q, want prefix %q", capturedURL, wantPrefix)
	}
}

func TestFetchToken_MultiTenantURL(t *testing.T) {
	var capturedURL string

	bc := NewBotClient("app-id", "app-secret", "", teamsNoopLogger())
	bc.client = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			capturedURL = r.URL.String()
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       tokenResponse(),
			}, nil
		}),
	}

	_, _, err := bc.fetchToken(t.Context())
	if err != nil {
		t.Fatalf("fetchToken: %v", err)
	}

	if !strings.HasPrefix(capturedURL, oauthTokenURL) {
		t.Errorf("token URL = %q, want prefix %q", capturedURL, oauthTokenURL)
	}
}

func TestNewBotClient_TenantIDWiring(t *testing.T) {
	bc := NewBotClient("app-id", "secret", "tenant-123", teamsNoopLogger())
	if bc.tenantID != "tenant-123" {
		t.Errorf("tenantID = %q, want %q", bc.tenantID, "tenant-123")
	}

	bcEmpty := NewBotClient("app-id", "secret", "", teamsNoopLogger())
	if bcEmpty.tenantID != "" {
		t.Errorf("tenantID = %q, want empty for multi-tenant", bcEmpty.tenantID)
	}
}
