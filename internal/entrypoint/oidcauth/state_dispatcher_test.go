// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oidcauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/trust"
)

type stubVerifier struct {
	claims *trust.StateClaims
	err    error
}

func (s *stubVerifier) VerifyStateToken(_ context.Context, _ string) (*trust.StateClaims, error) {
	return s.claims, s.err
}

func TestStateDispatcher_HappyPath302sToTargetLinkURI(t *testing.T) {
	sv := &stubVerifier{claims: &trust.StateClaims{
		TargetLinkURI: "https://gw-mcp-a.example.com/gw/mcp-a/oauth/finish",
	}}
	d := NewStateDispatcher(sv, testLogger())

	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/callback?code=AUTH_CODE&state=eyJ.payload.sig", nil)
	w := httptest.NewRecorder()
	d.Handle(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatalf("Location header is not a URL: %v", err)
	}
	if loc.Scheme != "https" || loc.Host != "gw-mcp-a.example.com" || loc.Path != "/gw/mcp-a/oauth/finish" {
		t.Errorf("Location = %s, want https://gw-mcp-a.example.com/gw/mcp-a/oauth/finish", loc.String())
	}
	if loc.Query().Get("code") != "AUTH_CODE" {
		t.Errorf("Location code = %q, want AUTH_CODE", loc.Query().Get("code"))
	}
	if loc.Query().Get("state") != "eyJ.payload.sig" {
		t.Errorf("Location state = %q, want eyJ.payload.sig", loc.Query().Get("state"))
	}
}

func TestStateDispatcher_VerifyFailure_400Opaque(t *testing.T) {
	sv := &stubVerifier{err: errors.New("state JWT: signature invalid")}
	d := NewStateDispatcher(sv, testLogger())
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/callback?code=X&state=eyJ.bad.sig", nil)
	w := httptest.NewRecorder()
	d.Handle(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	var body struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Error != "invalid_request" {
		t.Errorf("body.error = %q, want invalid_request", body.Error)
	}
	if body.ErrorDescription != "" {
		t.Errorf("body.error_description = %q, want empty (opaque)", body.ErrorDescription)
	}
}

func TestStateDispatcher_IdPErrorPassThrough(t *testing.T) {
	sv := &stubVerifier{claims: &trust.StateClaims{
		TargetLinkURI: "https://gw-mcp-a.example.com/gw/mcp-a/oauth/finish",
	}}
	d := NewStateDispatcher(sv, testLogger())
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/callback?error=access_denied&error_description=User+denied&state=eyJ.x.y", nil)
	w := httptest.NewRecorder()
	d.Handle(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("error") != "access_denied" {
		t.Errorf("propagated error = %q, want access_denied", loc.Query().Get("error"))
	}
	if loc.Query().Get("error_description") != "User denied" {
		t.Errorf("propagated description = %q, want \"User denied\"", loc.Query().Get("error_description"))
	}
	if loc.Query().Get("code") != "" {
		t.Errorf("code should NOT be set on error path; got %q", loc.Query().Get("code"))
	}
}

func TestStateDispatcher_BothCodeAndError_ErrorWins(t *testing.T) {
	sv := &stubVerifier{claims: &trust.StateClaims{
		TargetLinkURI: "https://gw-mcp-a.example.com/gw/mcp-a/oauth/finish",
	}}
	d := NewStateDispatcher(sv, testLogger())
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/callback?code=ATTACKER_CODE&error=access_denied&error_description=denied&state=eyJ.x.y", nil)
	w := httptest.NewRecorder()
	d.Handle(w, req)
	resp := w.Result()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("error") != "access_denied" {
		t.Errorf("propagated error = %q, want access_denied", loc.Query().Get("error"))
	}
	if loc.Query().Get("code") != "" {
		t.Errorf("code MUST NOT be forwarded when error is present; got %q", loc.Query().Get("code"))
	}
}

func TestStateDispatcher_MissingState_400Opaque(t *testing.T) {
	sv := &stubVerifier{err: errors.New("state JWT: empty token string")}
	d := NewStateDispatcher(sv, testLogger())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback?code=X", nil)
	w := httptest.NewRecorder()
	d.Handle(w, req)
	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Result().StatusCode)
	}
}
