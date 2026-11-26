// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRestrictAPIKeys(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	mux.HandleFunc("GET /config/{id}", ok)
	mux.HandleFunc("GET /config/{id}/secret", ok)
	mux.HandleFunc("DELETE /config/{id}", ok)
	allowed := map[string]bool{"GET /config/{id}": true}

	keyCaller := NewTestSubject(SubjectFields{UserID: "system:ci", Authenticated: true, AuthMethod: APIKeyAuthMethod})
	person := NewTestSubject(SubjectFields{UserID: "alice", Authenticated: true, AuthMethod: "sam_access_token"})

	cases := []struct {
		name     string
		allowed  map[string]bool
		identity *Subject
		method   string
		path     string
		want     int
	}{
		{"key on an allowed route", allowed, keyCaller, "GET", "/config/a", http.StatusOK},
		{"key on a more specific unlisted route", allowed, keyCaller, "GET", "/config/a/secret", http.StatusForbidden},
		{"key on an unlisted method", allowed, keyCaller, "DELETE", "/config/a", http.StatusForbidden},
		{"key on a route the mux cannot route", allowed, keyCaller, "GET", "/nowhere", http.StatusNotFound},
		{"key on a method the path does not serve", allowed, keyCaller, "POST", "/config/a", http.StatusMethodNotAllowed},
		{"key with nothing allowed", nil, keyCaller, "GET", "/config/a", http.StatusForbidden},
		{"person on an unlisted route", allowed, person, "DELETE", "/config/a", http.StatusOK},
		{"no identity", allowed, nil, "DELETE", "/config/a", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.identity != nil {
				req = req.WithContext(ContextWithSubject(req.Context(), tc.identity))
			}
			rec := httptest.NewRecorder()
			RestrictAPIKeys(mux, mux, tc.allowed)(mux).ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if tc.want != http.StatusForbidden {
				return
			}
			var body errorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.ErrorType != ErrorAuthorizationFailure || body.ErrorID == "" {
				t.Errorf("body = %+v, want errorType %q with an errorId", body, ErrorAuthorizationFailure)
			}
		})
	}
}

func TestRestrictAPIKeysReportsEachRefusal(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	mux.HandleFunc("GET /allowed", ok)
	mux.HandleFunc("GET /unlisted", ok)
	allowed := map[string]bool{"GET /allowed": true}
	keyCaller := NewTestSubject(SubjectFields{UserID: "system:ci", Authenticated: true, AuthMethod: APIKeyAuthMethod})

	cases := []struct {
		name      string
		path      string
		wantCode  int
		wantCalls int
	}{
		{"unlisted route", "/unlisted", http.StatusForbidden, 1},
		{"allowed route", "/allowed", http.StatusOK, 0},
		{"unroutable path", "/nowhere", http.StatusNotFound, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var statuses []int
			record := func(_ *http.Request, status int) { statuses = append(statuses, status) }
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req = req.WithContext(ContextWithSubject(req.Context(), keyCaller))
			rec := httptest.NewRecorder()
			RestrictAPIKeys(mux, mux, allowed, record)(mux).ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if len(statuses) != tc.wantCalls {
				t.Fatalf("onReject calls = %v, want %d", statuses, tc.wantCalls)
			}
			for _, status := range statuses {
				if status != http.StatusForbidden {
					t.Errorf("onReject status = %d, want %d", status, http.StatusForbidden)
				}
			}
		})
	}
}

func TestRestrictAPIKeysNeverSendsAnUnroutableKeyToNext(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /config/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	allowed := map[string]bool{"GET /config/{id}": true}
	keyCaller := NewTestSubject(SubjectFields{UserID: "system:ci", Authenticated: true, AuthMethod: APIKeyAuthMethod})
	person := NewTestSubject(SubjectFields{UserID: "alice", Authenticated: true, AuthMethod: "sam_access_token"})

	cases := []struct {
		name        string
		identity    *Subject
		method      string
		path        string
		wantCode    int
		wantReached bool
	}{
		{"key on a path the mux cannot route", keyCaller, http.MethodGet, "/nowhere", http.StatusNotFound, false},
		{"key on a method the path does not serve", keyCaller, http.MethodPost, "/config/a", http.StatusMethodNotAllowed, false},
		{"person on a path the mux cannot route", person, http.MethodGet, "/nowhere", http.StatusOK, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reached := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			})
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req = req.WithContext(ContextWithSubject(req.Context(), tc.identity))
			rec := httptest.NewRecorder()
			RestrictAPIKeys(mux, mux, allowed)(next).ServeHTTP(rec, req)

			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if reached != tc.wantReached {
				t.Errorf("next reached = %v, want %v", reached, tc.wantReached)
			}
		})
	}
}

func TestRestrictAPIKeysRefusalNamesTheKey(t *testing.T) {
	capture := &attrCaptureHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })

	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
	mux.HandleFunc("GET /allowed", ok)
	mux.HandleFunc("GET /unlisted", ok)
	allowed := map[string]bool{"GET /allowed": true}

	req := httptest.NewRequest(http.MethodGet, "/unlisted", nil)
	req = req.WithContext(ContextWithSubject(req.Context(), NewTestSubject(SubjectFields{UserID: "system:ci", Authenticated: true, AuthMethod: APIKeyAuthMethod, CredentialID: "key-1"})))
	rec := httptest.NewRecorder()
	RestrictAPIKeys(mux, mux, allowed)(mux).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if got := capture.attrs["credentialID"]; got != "key-1" {
		t.Errorf("credentialID = %q, want %q", got, "key-1")
	}
}
