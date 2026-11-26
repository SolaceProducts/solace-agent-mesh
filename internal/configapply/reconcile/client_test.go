// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package reconcile

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/configapply/target"
)

type fakeRefresher struct {
	calls    atomic.Int32
	newToken string
	err      error
}

func (f *fakeRefresher) ForceRefresh(_ context.Context, _ target.Target) (string, error) {
	f.calls.Add(1)
	return f.newToken, f.err
}

func TestClient_RetriesOnceWithRefreshedTokenOn401(t *testing.T) {
	var attempts atomic.Int32
	var sawTokens []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/rbac/roles", func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		sawTokens = append(sawTokens, r.Header.Get("Authorization"))
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"meta":{"pagination":{"pageNumber":1,"pageSize":100,"count":0,"totalPages":1}}}`))
	})
	srv := newV2Platform(t, mux)

	rf := &fakeRefresher{newToken: "new-tok"}
	c := NewClientWithAutoRefresh(
		target.Resolved{URL: srv.URL, Token: "stale-tok"},
		target.Target{Name: "dev", URL: srv.URL},
		rf,
	)

	if _, err := c.ListRBACRoles(t.Context()); err != nil {
		t.Fatalf("ListRBACRoles: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("attempts = %d; want 2 (initial 401 + retry)", got)
	}
	if rf.calls.Load() != 1 {
		t.Errorf("ForceRefresh calls = %d; want 1", rf.calls.Load())
	}
	if len(sawTokens) != 2 || sawTokens[0] != "Bearer stale-tok" || sawTokens[1] != "Bearer new-tok" {
		t.Errorf("authorization headers = %v; want [Bearer stale-tok, Bearer new-tok]", sawTokens)
	}
}

func TestClient_DoesNotRetryWhenRefresherReturnsError(t *testing.T) {
	var attempts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := newV2Platform(t, mux)

	rf := &fakeRefresher{err: errors.New("boom")}
	c := NewClientWithAutoRefresh(
		target.Resolved{URL: srv.URL, Token: "stale-tok"},
		target.Target{Name: "dev", URL: srv.URL},
		rf,
	)

	_, err := c.ListRBACRoles(t.Context())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("err = %v; want HTTPError 401", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d; want 1 (no retry on refresh failure)", got)
	}
	if rf.calls.Load() != 1 {
		t.Errorf("ForceRefresh calls = %d; want 1", rf.calls.Load())
	}
}

func TestClient_NoRefresherDoesNotRetry(t *testing.T) {
	var attempts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := newV2Platform(t, mux)

	c := NewClient(target.Resolved{URL: srv.URL, Token: "tok"})
	if _, err := c.ListRBACRoles(t.Context()); err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d; want 1 (no retry without refresher)", got)
	}
}

func TestClient_NoRetryOnSecond401(t *testing.T) {
	var attempts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := newV2Platform(t, mux)

	rf := &fakeRefresher{newToken: "rotated-but-also-bad"}
	c := NewClientWithAutoRefresh(
		target.Resolved{URL: srv.URL, Token: "stale-tok"},
		target.Target{Name: "dev", URL: srv.URL},
		rf,
	)

	_, err := c.ListRBACRoles(t.Context())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("err = %v; want HTTPError 401", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("attempts = %d; want 2 (one retry, no further retries)", got)
	}
	if rf.calls.Load() != 1 {
		t.Errorf("ForceRefresh calls = %d; want 1", rf.calls.Load())
	}
}

func TestClient_NoRetryWithoutToken(t *testing.T) {
	var attempts atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v2/platform/rbac/roles", func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := newV2Platform(t, mux)

	rf := &fakeRefresher{newToken: "should-not-be-used"}
	c := NewClientWithAutoRefresh(
		target.Resolved{URL: srv.URL},
		target.Target{Name: "dev", URL: srv.URL},
		rf,
	)

	if _, err := c.ListRBACRoles(t.Context()); err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d; want 1", got)
	}
	if rf.calls.Load() != 0 {
		t.Errorf("ForceRefresh calls = %d; want 0 (no token to refresh)", rf.calls.Load())
	}
}

type staleListDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func TestClient_RecordsSchemaSkewThroughDo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"a","name":"n","contentHash":"deadbeef"}],"meta":{"pagination":{"pageNumber":1}}}`))
	}))
	defer srv.Close()

	c := NewClient(target.Resolved{URL: srv.URL})
	sink := NewSchemaSkewSink()
	c.SetSchemaSkewSink(sink)

	var resp struct {
		Data []staleListDTO `json:"data"`
		Meta map[string]any `json:"meta"`
	}
	if err := c.do(t.Context(), http.MethodGet, "/api/v2/platform/skills?origin=custom&pageSize=100", nil, &resp); err != nil {
		t.Fatalf("do: %v", err)
	}

	findings := sink.Findings()
	if len(findings) != 1 || findings[0].Key != "contentHash" {
		t.Fatalf("findings = %+v, want one contentHash finding", findings)
	}
	if want := "/api/v2/platform/skills"; findings[0].Paths[0] != want {
		t.Errorf("path = %q, want %q (query stripped)", findings[0].Paths[0], want)
	}
}

func TestClient_NoSkewSinkSkipsDetection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"a","unexpected":true}`))
	}))
	defer srv.Close()

	c := NewClient(target.Resolved{URL: srv.URL})
	var resp struct {
		ID string `json:"id"`
	}
	if err := c.do(t.Context(), http.MethodGet, "/api/v2/platform/x", nil, &resp); err != nil {
		t.Fatalf("do: %v", err)
	}
}

type fakeOAuthResolver struct {
	fakeRefresher
	cached string
}

func (f *fakeOAuthResolver) ResolveSAMToken(context.Context, target.Target) (string, error) {
	return f.cached, nil
}

func TestNewClientForTarget_RefreshesOnlyCachedOAuthTokens(t *testing.T) {
	for _, tc := range []struct {
		name        string
		auth        target.AuthSpec
		env         map[string]string
		wantRefresh int32
	}{
		{name: "oauth cache", auth: target.AuthSpec{Type: target.AuthOAuth}, wantRefresh: 1},
		{name: "bearer env", auth: target.AuthSpec{Type: target.AuthBearerToken, EnvVar: "CI_SAM_TOKEN"}, env: map[string]string{"CI_SAM_TOKEN": "ci-tok"}},
		{name: "platform token overrides oauth", auth: target.AuthSpec{Type: target.AuthOAuth}, env: map[string]string{target.DefaultTokenEnvVar: "ci-tok"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(target.DefaultTokenEnvVar, "")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			srv := httptest.NewServer(servedOnV2(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			}))
			t.Cleanup(srv.Close)

			resolver := &fakeOAuthResolver{cached: "cached-tok"}
			c, err := newClientForTarget(t.Context(), target.Target{Name: "dev", URL: srv.URL, Auth: tc.auth}, resolver)
			if err != nil {
				t.Fatalf("newClientForTarget: %v", err)
			}
			if _, err := c.ListRBACRoles(t.Context()); err == nil {
				t.Fatal("ListRBACRoles succeeded; want the 401")
			}
			if got := resolver.calls.Load(); got != tc.wantRefresh {
				t.Errorf("ForceRefresh calls = %d, want %d", got, tc.wantRefresh)
			}
		})
	}
}
