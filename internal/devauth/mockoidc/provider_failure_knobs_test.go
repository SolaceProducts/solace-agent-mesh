// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mockoidc

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func jwtHeaderKid(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		t.Fatalf("malformed jwt: %q", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode jwt header: %v", err)
	}
	var hdr struct {
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(raw, &hdr); err != nil {
		t.Fatalf("unmarshal jwt header: %v", err)
	}
	return hdr.Kid
}

func jwksKid(t *testing.T, p *MockOIDCProvider) string {
	t.Helper()
	resp, err := http.Get(p.URL() + "/jwks.json")
	if err != nil {
		t.Fatalf("GET jwks: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var doc struct {
		Keys []struct {
			Kid string `json:"kid"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode jwks: %v", err)
	}
	if len(doc.Keys) != 1 {
		t.Fatalf("want 1 jwks key, got %d", len(doc.Keys))
	}
	return doc.Keys[0].Kid
}

func providerReachable(issuer string) bool {
	resp, err := http.Get(issuer + "/.well-known/openid-configuration")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func TestRotateSigningKey_ChangesJWKSAndTokenHeader(t *testing.T) {
	p, err := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []FakeOIDCUser{{Sub: "alice", Email: "alice@x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })

	kidBefore := jwksKid(t, p)
	_, idtBefore, _ := authorizeAndExchange(t, p, "alice")
	if got := jwtHeaderKid(t, idtBefore); got != kidBefore {
		t.Fatalf("pre-rotation token kid %q != jwks kid %q", got, kidBefore)
	}

	if err := p.RotateSigningKey(); err != nil {
		t.Fatalf("RotateSigningKey: %v", err)
	}

	kidAfter := jwksKid(t, p)
	if kidAfter == kidBefore {
		t.Fatalf("jwks kid unchanged after rotation: %q", kidAfter)
	}
	_, idtAfter, _ := authorizeAndExchange(t, p, "alice")
	if got := jwtHeaderKid(t, idtAfter); got != kidAfter {
		t.Fatalf("post-rotation token kid %q != new jwks kid %q", got, kidAfter)
	}
}

func TestStopThenRestart(t *testing.T) {
	p, err := NewMockOIDCProvider(MockOIDCProviderConfig{
		ClientID: "c", ClientSecret: "s",
		Users: []FakeOIDCUser{{Sub: "alice", Email: "alice@x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })

	issuer := p.URL()
	if !providerReachable(issuer) {
		t.Fatal("provider should be reachable before Stop")
	}

	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if providerReachable(issuer) {
		t.Fatal("provider should be unreachable after Stop")
	}

	if err := p.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if p.URL() != issuer {
		t.Fatalf("issuer changed across restart: %q -> %q", issuer, p.URL())
	}
	if !providerReachable(issuer) {
		t.Fatal("provider should be reachable after Restart")
	}
	if _, idt, _ := authorizeAndExchange(t, p, "alice"); idt == "" {
		t.Fatal("expected a working token flow after Restart")
	}
}
