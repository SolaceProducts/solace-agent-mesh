// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package oauth2cc

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func refreshServer(t *testing.T, status int, body string) (*httptest.Server, *http.Request) {
	t.Helper()
	var got http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		got = *r
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestRefresh_SendsAPublicClientRefreshGrant(t *testing.T) {
	srv, got := refreshServer(t, http.StatusOK, `{"access_token":"at-2","refresh_token":"rt-2","expires_in":3600}`)
	before := time.Now()

	tokens, err := Refresh(t.Context(), srv.Client(), RefreshRequest{TokenURL: srv.URL, ClientID: "desktop", RefreshToken: "rt-1"})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got.PostForm.Get("grant_type") != "refresh_token" || got.PostForm.Get("client_id") != "desktop" || got.PostForm.Get("refresh_token") != "rt-1" {
		t.Errorf("form = %v", got.PostForm)
	}
	if got.PostForm.Has("client_secret") {
		t.Error("a public client must not send a client secret")
	}
	if tokens.AccessToken != "at-2" || tokens.RefreshToken != "rt-2" {
		t.Errorf("tokens = %+v", tokens)
	}
	if tokens.ExpiresAt.Before(before.Add(time.Hour)) || tokens.ExpiresAt.After(time.Now().Add(time.Hour)) {
		t.Errorf("ExpiresAt = %v, want an hour after the request", tokens.ExpiresAt)
	}
}

func TestRefresh_KeepsTheRefreshTokenWhenNoneIsReturned(t *testing.T) {
	srv, _ := refreshServer(t, http.StatusOK, `{"access_token":"at-2"}`)

	tokens, err := Refresh(t.Context(), srv.Client(), RefreshRequest{TokenURL: srv.URL, ClientID: "desktop", RefreshToken: "rt-1"})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tokens.RefreshToken != "rt-1" {
		t.Errorf("RefreshToken = %q, want the one sent", tokens.RefreshToken)
	}
	if time.Until(tokens.ExpiresAt) < 59*time.Minute {
		t.Errorf("ExpiresAt = %v, want the default hour when expires_in is missing", tokens.ExpiresAt)
	}
}

func TestRefresh_InvalidGrantIsARejection(t *testing.T) {
	srv, _ := refreshServer(t, http.StatusForbidden, `{"error":"invalid_grant","error_description":"Unknown or invalid refresh token."}`)

	_, err := Refresh(t.Context(), srv.Client(), RefreshRequest{TokenURL: srv.URL, ClientID: "desktop", RefreshToken: "rt-1"})
	if !errors.Is(err, ErrRefreshTokenRejected) {
		t.Fatalf("err = %v, want ErrRefreshTokenRejected", err)
	}
}

func TestRefresh_OtherFailuresOmitTheResponseBody(t *testing.T) {
	srv, _ := refreshServer(t, http.StatusInternalServerError, `{"error":"server_error","error_description":"secret detail rt-1"}`)

	_, err := Refresh(t.Context(), srv.Client(), RefreshRequest{TokenURL: srv.URL, ClientID: "desktop", RefreshToken: "rt-1"})
	if err == nil || errors.Is(err, ErrRefreshTokenRejected) {
		t.Fatalf("err = %v, want a non-rejection failure", err)
	}
	if strings.Contains(err.Error(), "secret detail") || !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %q, want the status code and no body", err)
	}
}

func TestRefresh_FailuresKeepOnlyARegisteredErrorCode(t *testing.T) {
	cases := map[string]struct {
		body     string
		wantCode string
	}{
		"registered code": {
			body:     `{"error":"access_denied","error_description":"Cannot read properties of undefined (reading 'authenticated_at')"}`,
			wantCode: "access_denied",
		},
		"unregistered code": {
			body:     `{"error":"<b>see https://evil.example</b>"}`,
			wantCode: "",
		},
		"no JSON": {
			body:     `<html>upstream down</html>`,
			wantCode: "",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _ := refreshServer(t, http.StatusInternalServerError, tc.body)

			_, err := Refresh(t.Context(), srv.Client(), RefreshRequest{TokenURL: srv.URL, ClientID: "desktop", RefreshToken: "rt-1"})
			if err == nil || err.Error() != "oauth2cc: token endpoint returned HTTP 500" {
				t.Errorf("err = %v, want only the status code", err)
			}
			if got := TokenEndpointErrorCode(err); got != tc.wantCode {
				t.Errorf("TokenEndpointErrorCode = %q, want %q", got, tc.wantCode)
			}
		})
	}
}

func TestRefresh_RequiresItsInputs(t *testing.T) {
	if _, err := Refresh(t.Context(), http.DefaultClient, RefreshRequest{TokenURL: "https://id.example.com/oauth/token", ClientID: "desktop"}); err == nil {
		t.Fatal("Refresh without a refresh token succeeded")
	}
}
