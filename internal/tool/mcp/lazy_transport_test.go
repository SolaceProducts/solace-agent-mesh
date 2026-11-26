// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestBearerRoundTripper_InjectsHeaders(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rt := &bearerRoundTripper{
		fetch: func(_ context.Context) (http.Header, error) {
			return http.Header{"Authorization": []string{"Bearer abc123"}}, nil
		},
		base: http.DefaultTransport,
	}
	client := &http.Client{Transport: rt}

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if seen != "Bearer abc123" {
		t.Errorf("server saw Authorization = %q, want 'Bearer abc123'", seen)
	}
}

func TestBearerRoundTripper_PropagatesAuthRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server must not be reached when fetch returns ErrAuthRequired")
	}))
	defer srv.Close()

	rt := &bearerRoundTripper{
		fetch: func(_ context.Context) (http.Header, error) {
			return nil, ErrAuthRequired
		},
	}
	client := &http.Client{Transport: rt}

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	resp, err := client.Do(req)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected error when fetch returns ErrAuthRequired, got nil")
	}
	if !errors.Is(err, ErrAuthRequired) {
		t.Errorf("error = %v, want it to wrap ErrAuthRequired", err)
	}
}

func TestBearerRoundTripper_RefreshesPerRequest(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var counter atomic.Int64
	rt := &bearerRoundTripper{
		fetch: func(_ context.Context) (http.Header, error) {
			n := counter.Add(1)
			return http.Header{"Authorization": []string{"Bearer token-" + string(rune('0'+n))}}, nil
		},
	}
	client := &http.Client{Transport: rt}

	for range 3 {
		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	if len(seen) != 3 {
		t.Fatalf("server saw %d requests, want 3", len(seen))
	}
	if seen[0] == seen[1] || seen[1] == seen[2] {
		t.Errorf("tokens did not refresh: %v", seen)
	}
}

func TestBearerRoundTripper_SkipsWhenAuthPreset(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	fetchCalls := 0
	rt := &bearerRoundTripper{
		fetch: func(_ context.Context) (http.Header, error) {
			fetchCalls++
			return http.Header{"Authorization": []string{"Bearer captured-first-user"}}, nil
		},
	}
	client := &http.Client{Transport: rt}

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Authorization", "Bearer per-request-user-b")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	_ = resp.Body.Close()

	if seen != "Bearer per-request-user-b" {
		t.Errorf("server saw Authorization = %q, want 'Bearer per-request-user-b' — RoundTripper overwrote a pre-set header", seen)
	}
	if fetchCalls != 0 {
		t.Errorf("fetch was called %d times when Authorization was pre-set, want 0", fetchCalls)
	}
}

func TestErrAuthRequired_UnwrapsThroughRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("server must not be reached when fetch returns ErrAuthRequired")
	}))
	defer srv.Close()

	rt := &bearerRoundTripper{
		fetch: func(_ context.Context) (http.Header, error) { return nil, ErrAuthRequired },
	}
	client := &http.Client{Transport: rt}

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, rtErr := client.Do(req)
	if rtErr == nil {
		t.Fatal("expected a transport error")
	}
	wrapped := fmt.Errorf("lazy connect %q: start transport: %w", "test-client", rtErr)
	if !errors.Is(wrapped, ErrAuthRequired) {
		t.Errorf("errors.Is returned false on wrapped err = %v; executeWithAuth's recovery path would miss it", wrapped)
	}
}

func TestBearerRoundTripper_NilFetch(t *testing.T) {
	rt := &bearerRoundTripper{}
	req, _ := http.NewRequest(http.MethodGet, "http://example.invalid", nil)
	_, err := rt.RoundTrip(req)
	if err == nil {
		t.Fatal("expected error when fetch is nil, got nil")
	}
}
