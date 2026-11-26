// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestCardAuthConfigFromStored_Bearer(t *testing.T) {
	got := cardAuthConfigFromStored("bearer", map[string]any{"token": "abc"}, nil)
	if got == nil || got.BearerToken != "abc" {
		t.Errorf("BearerToken = %+v, want {abc}", got)
	}
}

func TestCardAuthConfigFromStored_Bearer_EmptyToken(t *testing.T) {
	if got := cardAuthConfigFromStored("bearer", map[string]any{"token": ""}, nil); got != nil {
		t.Errorf("want nil for empty token, got %+v", got)
	}
}

func TestCardAuthConfigFromStored_Apikey_DefaultHeader(t *testing.T) {
	got := cardAuthConfigFromStored("apikey", map[string]any{"api_key": "k1"}, nil)
	if got == nil {
		t.Fatal("want non-nil")
	}
	if got.ApikeyValue != "k1" {
		t.Errorf("ApikeyValue = %q, want k1", got.ApikeyValue)
	}
	if got.ApikeyHeaderName != "X-API-Key" {
		t.Errorf("header default = %q, want X-API-Key", got.ApikeyHeaderName)
	}
}

func TestCardAuthConfigFromStored_Apikey_CustomHeader(t *testing.T) {
	got := cardAuthConfigFromStored("apikey", map[string]any{
		"api_key":     "k1",
		"header_name": "X-Custom-Key",
	}, nil)
	if got.ApikeyHeaderName != "X-Custom-Key" {
		t.Errorf("header = %q, want X-Custom-Key", got.ApikeyHeaderName)
	}
}

func TestCardAuthConfigFromStored_OAuth2_JoinsScopes(t *testing.T) {
	got := cardAuthConfigFromStored("oauth2", map[string]any{
		"client_id":      "cid",
		"client_secret":  "csecret",
		"token_endpoint": "https://auth.example.com/token",
		"scopes":         []any{"read", "write"},
	}, nil)
	if got == nil {
		t.Fatal("want non-nil")
	}
	if got.OAuth2ClientID != "cid" || got.OAuth2ClientSecret != "csecret" {
		t.Errorf("client id/secret wrong: %+v", got)
	}
	if got.OAuth2TokenURL != "https://auth.example.com/token" {
		t.Errorf("token url = %q", got.OAuth2TokenURL)
	}
	if got.OAuth2Scope != "read write" {
		t.Errorf("scope = %q, want 'read write'", got.OAuth2Scope)
	}
}

func TestCardAuthConfigFromStored_OAuth2_MissingClientSecret(t *testing.T) {
	got := cardAuthConfigFromStored("oauth2", map[string]any{
		"client_id":      "cid",
		"token_endpoint": "https://e/token",
	}, nil)
	if got != nil {
		t.Errorf("missing client_secret should return nil, got %+v", got)
	}
}

func TestCardAuthConfigFromStored_Basic(t *testing.T) {
	got := cardAuthConfigFromStored("basic", map[string]any{
		"username": "u1", "password": "p1",
	}, nil)
	if got == nil || got.BasicUsername != "u1" || got.BasicPassword != "p1" {
		t.Errorf("got %+v", got)
	}
}

func TestCardAuthConfigFromStored_Basic_MissingPassword(t *testing.T) {
	if got := cardAuthConfigFromStored("basic", map[string]any{"username": "u1"}, nil); got != nil {
		t.Errorf("missing password should return nil, got %+v", got)
	}
}

func TestCardAuthConfigFromStored_NoneAndEmpty(t *testing.T) {
	if got := cardAuthConfigFromStored("", nil, nil); got != nil {
		t.Errorf("empty type should return nil, got %+v", got)
	}
	if got := cardAuthConfigFromStored("none", map[string]any{"token": "x"}, nil); got != nil {
		t.Errorf("'none' should return nil, got %+v", got)
	}
	if got := cardAuthConfigFromStored("bearer", nil, nil); got != nil {
		t.Errorf("nil stored should return nil, got %+v", got)
	}
}

func TestCardAuthConfigFromStored_UnknownType_WarnAndNil(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	got := cardAuthConfigFromStored("custom-scheme", map[string]any{"foo": "bar"}, logger)
	if got != nil {
		t.Errorf("unknown type should return nil, got %+v", got)
	}
	if !strings.Contains(buf.String(), "unknown auth type") {
		t.Errorf("expected warning log, got: %s", buf.String())
	}
}

func TestScopeStringFromStored_AllShapes(t *testing.T) {
	if s := scopeStringFromStored([]any{"a", "b", "c"}); s != "a b c" {
		t.Errorf("array: got %q", s)
	}
	if s := scopeStringFromStored([]string{"x", "y"}); s != "x y" {
		t.Errorf("string slice: got %q", s)
	}
	if s := scopeStringFromStored("already space-joined"); s != "already space-joined" {
		t.Errorf("string passthrough: got %q", s)
	}
	if s := scopeStringFromStored(nil); s != "" {
		t.Errorf("nil: got %q", s)
	}
}
