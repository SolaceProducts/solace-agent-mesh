// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.Handler) (*client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	cfg := &bamboohrConfig{
		Subdomain:      "acme",
		APIKey:         "test-key",
		LookupKey:      defaultLookupKey,
		CacheTTL:       defaultCacheTTL,
		BaseURL:        srv.URL,
		RequestTimeout: 2 * time.Second,
	}
	c := newClient(cfg, slog.New(slog.DiscardHandler))
	c.retryBackoff = 5 * time.Millisecond
	return c, srv
}

func TestClient_FetchDirectory_Success(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/acme/v1/employees/directory" {
			t.Errorf("path = %q", got)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "test-key" || pass != "x" {
			t.Errorf("basic auth = (%q,%q,%v)", user, pass, ok)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("accept = %q", got)
		}
		_, _ = io.WriteString(w, `{"employees":[{"id":"1","workEmail":"Jane@acme.co","displayName":"Jane Doe"}]}`)
	})
	c, _ := newTestClient(t, handler)

	emps, err := c.fetchEmployeeDirectory(t.Context())
	if err != nil {
		t.Fatalf("fetchEmployeeDirectory: %v", err)
	}
	if len(emps) != 1 || emps[0].WorkEmail != "Jane@acme.co" {
		t.Errorf("unexpected employees: %+v", emps)
	}
}

func TestClient_FetchDirectory_Unauthorized(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
	c, _ := newTestClient(t, handler)

	_, err := c.fetchEmployeeDirectory(t.Context())
	if err == nil {
		t.Fatal("expected error on 401")
	}
	ce, ok := errors.AsType[*clientError](err)
	if !ok {
		t.Fatalf("expected *clientError, got %T: %v", err, err)
	}
	if ce.Status != http.StatusUnauthorized {
		t.Errorf("status = %d", ce.Status)
	}
}

func TestClient_FetchDirectory_429ThenSuccess(t *testing.T) {
	var attempts atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			http.Error(w, "rate", http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{"employees":[]}`)
	})
	c, _ := newTestClient(t, handler)

	if _, err := c.fetchEmployeeDirectory(t.Context()); err != nil {
		t.Fatalf("fetchEmployeeDirectory: %v", err)
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestClient_FetchDirectory_500Exhausted(t *testing.T) {
	var attempts atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	c, _ := newTestClient(t, handler)

	_, err := c.fetchEmployeeDirectory(t.Context())
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if got := attempts.Load(); got != int32(c.retryAttempts) {
		t.Errorf("attempts = %d, want %d", got, c.retryAttempts)
	}
}

func TestClient_FetchDirectory_Timeout(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.(http.Flusher).Flush()
		time.Sleep(200 * time.Millisecond)
	})
	c, _ := newTestClient(t, handler)
	c.http.Timeout = 50 * time.Millisecond

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	_, err := c.fetchEmployeeDirectory(ctx)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestClient_FetchWhosOut(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/acme/v1/time_off/whos_out" {
			t.Errorf("path = %q", got)
		}
		if got := r.URL.Query().Get("start"); got != "2026-04-16" {
			t.Errorf("start = %q", got)
		}
		_, _ = io.WriteString(w, `[{"employeeId":42,"start":"2026-04-16","end":"2026-04-17","type":"Vacation"}]`)
	})
	c, _ := newTestClient(t, handler)

	entries, err := c.fetchWhosOut(t.Context(), "2026-04-16", "2026-04-30")
	if err != nil {
		t.Fatalf("fetchWhosOut: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d", len(entries))
	}
	if !matchesEmployee(entries[0].EmployeeID, "42") {
		t.Errorf("matchesEmployee failed for %v", entries[0].EmployeeID)
	}
}

func photoClient(t *testing.T, baseURL string) *client {
	t.Helper()
	cfg := &bamboohrConfig{
		Subdomain:      "acme",
		APIKey:         "test-key",
		BaseURL:        baseURL,
		LookupKey:      defaultLookupKey,
		CacheTTL:       defaultCacheTTL,
		RequestTimeout: defaultRequestTimeout,
	}
	c := newClient(cfg, slog.New(slog.DiscardHandler))
	c.retryBackoff = time.Millisecond
	return c
}

func TestClient_FetchPhoto(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, _, ok := r.BasicAuth(); !ok || user != "test-key" {
			t.Errorf("basic auth missing on a photo served by the configured BambooHR host")
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 0x50, 0x4E, 0x47})
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body, ct, err := photoClient(t, srv.URL).fetchPhoto(t.Context(), srv.URL+"/photo.png")
	if err != nil {
		t.Fatalf("fetchPhoto: %v", err)
	}
	if ct != "image/png" {
		t.Errorf("content type = %q", ct)
	}
	if len(body) != 4 {
		t.Errorf("photo size = %d", len(body))
	}
}

func TestClient_FetchPhoto_KeyGoesOnlyToTheConfiguredHost(t *testing.T) {
	var sawAuthorization atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sawAuthorization.Store(true)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 0x50, 0x4E, 0x47})
	}))
	defer other.Close()

	body, _, err := photoClient(t, "https://api.bamboohr.example").fetchPhoto(t.Context(), other.URL+"/photo.png")
	if err != nil {
		t.Fatalf("fetchPhoto(off-host photo): %v", err)
	}
	if len(body) != 4 {
		t.Errorf("off-host photo size = %d, want it fetched without credentials", len(body))
	}
	if sawAuthorization.Load() {
		t.Fatal("the BambooHR API key was sent to a host other than the configured BambooHR host")
	}
}

func TestPhotoMayReceiveTheKey(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, base, photo string
		want              bool
	}{
		{"the configured host", "https://api.bamboohr.com/api/gateway.php", "https://api.bamboohr.com/x.jpg", true},
		{"the configured host in another case", "https://api.bamboohr.com", "https://API.BambooHR.com/x.jpg", true},
		{"the configured host on the default port written out", "https://api.bamboohr.com", "https://api.bamboohr.com:443/x.jpg", true},
		{"another tenant of the same domain", "https://api.bamboohr.com", "https://evil-tenant.bamboohr.com/x.jpg", false},
		{"a deeper subdomain", "https://api.bamboohr.com", "https://x.y.bamboohr.com/x.jpg", false},
		{"a sibling that is the tenant's own", "https://api.bamboohr.com", "https://acme.bamboohr.com/x.jpg", false},
		{"the apex", "https://api.bamboohr.com", "https://bamboohr.com/x.jpg", false},
		{"a suffix lookalike", "https://api.bamboohr.com", "https://evilbamboohr.com/x.jpg", false},
		{"a domain that merely starts with it", "https://api.bamboohr.com", "https://api.bamboohr.com.evil.example/x.jpg", false},
		{"another port", "https://api.bamboohr.com", "https://api.bamboohr.com:8443/x.jpg", false},
		{"the same host downgraded to http", "https://api.bamboohr.com", "http://api.bamboohr.com/x.jpg", false},
		{"the same host and port downgraded to http", "https://api.bamboohr.com:8443", "http://api.bamboohr.com:8443/x.jpg", false},
		{"a co.uk base and another co.uk host", "https://hr.co.uk", "https://attacker.co.uk/x.jpg", false},
		{"an IP base and a near address", "https://10.0.0.5", "https://1.0.0.5/x.jpg", false},
		{"an IP base and the same IP", "https://10.0.0.5:8443", "https://10.0.0.5:8443/x.jpg", true},
		{"an IP base on another port", "https://192.168.1.10:8443", "https://192.168.1.10:9443/x.jpg", false},
		{"an http base and the same origin", "http://127.0.0.1:8080", "http://127.0.0.1:8080/x.jpg", true},
		{"a relative reference", "https://api.bamboohr.com", "/x.jpg", false},
		{"no URL", "https://api.bamboohr.com", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := photoClient(t, tc.base).mayReceiveAPIKey(tc.photo); got != tc.want {
				t.Fatalf("base %q, photo %q: mayReceiveAPIKey = %v, want %v", tc.base, tc.photo, got, tc.want)
			}
		})
	}
}

func TestClient_FetchPhoto_NeverForwardsTheKeyAcrossARedirect(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked.Store(true)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 0x50, 0x4E, 0x47})
	}))
	defer target.Close()
	var firstHopAuthorized atomic.Bool
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/photo":
			if r.Header.Get("Authorization") != "" {
				firstHopAuthorized.Store(true)
			}
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/cross":
			http.Redirect(w, r, target.URL+"/photo.png", http.StatusFound)
		default:
			if r.Header.Get("Authorization") != "" {
				leaked.Store(true)
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte{0x89, 0x50, 0x4E, 0x47})
		}
	}))
	defer origin.Close()

	c := photoClient(t, origin.URL)
	for _, path := range []string{"/photo", "/cross"} {
		if _, _, err := c.fetchPhoto(t.Context(), origin.URL+path); err != nil {
			t.Fatalf("fetchPhoto(%s): %v", path, err)
		}
	}
	if !firstHopAuthorized.Load() {
		t.Error("the first request to the configured host carried no key")
	}
	if leaked.Load() {
		t.Fatal("the BambooHR API key was forwarded across a redirect")
	}
}

func TestClient_FetchPhoto_EmptyURL(t *testing.T) {
	cfg := &bamboohrConfig{
		Subdomain:      "acme",
		APIKey:         "test-key",
		BaseURL:        "https://unused.test",
		LookupKey:      defaultLookupKey,
		CacheTTL:       defaultCacheTTL,
		RequestTimeout: defaultRequestTimeout,
	}
	c := newClient(cfg, slog.New(slog.DiscardHandler))
	body, ct, err := c.fetchPhoto(t.Context(), "")
	if err != nil || body != nil || ct != "" {
		t.Errorf("empty url should return zero values (got body=%v ct=%q err=%v)", body, ct, err)
	}
}

func TestClient_FetchPhoto_404(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()
	cfg := &bamboohrConfig{
		Subdomain:      "acme",
		APIKey:         "test-key",
		BaseURL:        "https://unused.test",
		LookupKey:      defaultLookupKey,
		CacheTTL:       defaultCacheTTL,
		RequestTimeout: defaultRequestTimeout,
	}
	c := newClient(cfg, slog.New(slog.DiscardHandler))
	c.retryBackoff = time.Millisecond

	_, _, err := c.fetchPhoto(t.Context(), srv.URL+"/missing.jpg")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 in error, got: %v", err)
	}
}

func TestMatchesEmployee(t *testing.T) {
	cases := []struct {
		entry  any
		target string
		want   bool
	}{
		{"42", "42", true},
		{42, "42", true},
		{int64(42), "42", true},
		{42.0, "42", true},
		{"abc", "42", false},
		{nil, "42", false},
	}
	for _, tc := range cases {
		if got := matchesEmployee(tc.entry, tc.target); got != tc.want {
			t.Errorf("matchesEmployee(%v, %q) = %v, want %v", tc.entry, tc.target, got, tc.want)
		}
	}
}

func TestClient_APICallsNeverForwardTheKeyAcrossARedirect(t *testing.T) {
	t.Parallel()
	for _, call := range []string{"the directory", "who is out"} {
		for _, hop := range []string{"another port", "the same host"} {
			t.Run(call+" redirected to "+hop, func(t *testing.T) {
				t.Parallel()
				var leaked atomic.Bool
				var otherPort *httptest.Server
				if hop == "another port" {
					otherPort = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Header.Get("Authorization") != "" {
							leaked.Store(true)
						}
						_, _ = io.WriteString(w, `{"employees":[],"fields":[]}`)
					}))
					defer otherPort.Close()
				}
				var self atomic.Pointer[httptest.Server]
				c, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/landed") {
						if r.Header.Get("Authorization") != "" {
							leaked.Store(true)
						}
						_, _ = io.WriteString(w, `{"employees":[],"fields":[]}`)
						return
					}
					target := self.Load().URL + "/landed"
					if otherPort != nil {
						target = otherPort.URL + "/landed"
					}
					http.Redirect(w, r, target, http.StatusFound)
				}))
				self.Store(srv)
				var err error
				if call == "the directory" {
					_, err = c.fetchEmployeeDirectory(t.Context())
				} else {
					_, err = c.fetchWhosOut(t.Context(), "2026-04-16", "2026-04-30")
				}
				if err != nil {
					t.Logf("%s returned %v; the point is that no key crossed the redirect", call, err)
				}
				if leaked.Load() {
					t.Fatalf("the BambooHR API key was forwarded across a redirect on %s", call)
				}
			})
		}
	}
}

func TestClient_ARedirectLoopStopsAfterTenRequests(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	c, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, r.URL.Path, http.StatusFound)
	}))
	if _, _, err := c.fetchPhoto(t.Context(), srv.URL+"/loop"); err == nil {
		t.Fatal("fetchPhoto followed a redirect loop without an error")
	}
	if got := requests.Load(); got != 10 {
		t.Fatalf("requests served = %d, want 10 before the redirect cap stops the loop", got)
	}
}

func TestClient_APICallsDropTheKeyOnEveryHopOfAMultiHopRedirect(t *testing.T) {
	t.Parallel()
	for _, call := range []string{"the directory", "who is out"} {
		t.Run(call, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			keyOnRequest := map[string]bool{}
			var self atomic.Pointer[httptest.Server]
			c, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hop := "first"
				switch {
				case strings.HasSuffix(r.URL.Path, "/hop1"):
					hop = "hop1"
				case strings.HasSuffix(r.URL.Path, "/hop2"):
					hop = "hop2"
				}
				mu.Lock()
				keyOnRequest[hop] = r.Header.Get("Authorization") != ""
				mu.Unlock()
				switch hop {
				case "first":
					http.Redirect(w, r, self.Load().URL+"/hop1", http.StatusFound)
				case "hop1":
					http.Redirect(w, r, self.Load().URL+"/hop2", http.StatusFound)
				default:
					_, _ = io.WriteString(w, `{"employees":[],"fields":[]}`)
				}
			}))
			self.Store(srv)
			var err error
			if call == "the directory" {
				_, err = c.fetchEmployeeDirectory(t.Context())
			} else {
				_, err = c.fetchWhosOut(t.Context(), "2026-04-16", "2026-04-30")
			}
			if err != nil {
				t.Logf("%s returned %v after the redirects", call, err)
			}
			mu.Lock()
			defer mu.Unlock()
			if !keyOnRequest["first"] {
				t.Errorf("the first request of %s carried no key, so the test proves nothing: %v", call, keyOnRequest)
			}
			if _, reached := keyOnRequest["hop2"]; !reached {
				t.Fatalf("%s never reached the second hop: %v", call, keyOnRequest)
			}
			if keyOnRequest["hop1"] || keyOnRequest["hop2"] {
				t.Errorf("the key crossed a redirect on %s: %v", call, keyOnRequest)
			}
		})
	}
}
