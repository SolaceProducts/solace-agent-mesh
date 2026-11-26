// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package gemini

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
)

const secretLocation = "/login/SECRETPATH?token=SECRETQUERY&state=abc"

func assertNoRedirectURL(t *testing.T, err error, otherHost string) {
	t.Helper()
	msg := err.Error()
	for _, leaked := range []string{"SECRETPATH", "SECRETQUERY", "/login"} {
		if strings.Contains(msg, leaked) {
			t.Errorf("error = %q, want no part of the redirect's Location (%s)", msg, leaked)
		}
	}
	if otherHost != "" && strings.Contains(msg, otherHost) {
		t.Errorf("error = %q, want no redirect host", msg)
	}
}

func assertConfigFailure(t *testing.T, err error, sentinel error) {
	t.Helper()
	if de, ok := errors.AsType[*decision.Error](err); !ok || de.Kind != decision.FailureConfig || !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want a config failure wrapping %v", err, sentinel)
	}
}

func TestEvaluateRefusesARedirectToAnotherOrigin(t *testing.T) {
	var reachedOther atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reachedOther.Add(1) }))
	t.Cleanup(other.Close)
	var calls atomic.Int32
	c := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, other.URL+secretLocation, http.StatusTemporaryRedirect)
	})

	_, err := c.Evaluate(t.Context(), triageRequest())
	assertConfigFailure(t, err, decision.ErrCrossOriginRedirect)
	assertNoRedirectURL(t, err, strings.TrimPrefix(other.URL, "http://"))
	if !strings.Contains(err.Error(), decision.ErrCrossOriginRedirect.Error()) {
		t.Errorf("error = %q, want the refusal's own text", err)
	}
	if reachedOther.Load() != 0 {
		t.Error("the redirect reached the other origin")
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1: a misconfigured endpoint is not retried", calls.Load())
	}
}

func TestEvaluateStopsARedirectLoopWithoutRetrying(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, secretLocation, http.StatusTemporaryRedirect)
	})

	_, err := c.Evaluate(t.Context(), triageRequest())
	assertConfigFailure(t, err, decision.ErrTooManyRedirects)
	assertNoRedirectURL(t, err, "")
	if !strings.Contains(err.Error(), decision.ErrTooManyRedirects.Error()) {
		t.Errorf("error = %q, want the limit's own text", err)
	}
	if calls.Load() != 10 {
		t.Errorf("requests = %d, want 10: one call followed to the redirect limit, not retried", calls.Load())
	}
}

func TestEvaluateKeepsTheSharedPolicysRefusalWithoutTheLocation(t *testing.T) {
	sharedRefusal := errors.New("shared policy refused the redirect")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secretLocation, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL, APIKey: "test-key", HTTPClient: &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return sharedRefusal },
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.transport.Backoff = time.Millisecond

	_, err = c.Evaluate(t.Context(), triageRequest())
	if !errors.Is(err, sharedRefusal) {
		t.Fatalf("err = %v, want the shared policy's refusal", err)
	}
	assertNoRedirectURL(t, err, "")
}

func TestEvaluateReportsAnUnparsableLocationWithoutQuotingIt(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, "", func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "http://[::1%SECRETPATH/?token=SECRETQUERY")
		w.WriteHeader(http.StatusTemporaryRedirect)
	})

	_, err := c.Evaluate(t.Context(), triageRequest())
	assertNoRedirectURL(t, err, "")
	if de, ok := errors.AsType[*decision.Error](err); !ok || de.Kind != decision.FailureConfig {
		t.Fatalf("err = %v, want a config failure", err)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1: a misconfigured endpoint is not retried", calls.Load())
	}
}

func TestEvaluateKeepsAFollowedRedirectsLocationOutOfTheErrorAndRetryLog(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1beta/decisions" {
			calls.Add(1)
			http.Redirect(w, r, secretLocation, http.StatusTemporaryRedirect)
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	})
	var logs bytes.Buffer
	req := triageRequest()
	req.Logger = slog.New(slog.NewTextHandler(&logs, nil))

	_, err := c.Evaluate(t.Context(), req)
	assertNoRedirectURL(t, err, "")
	if de, ok := errors.AsType[*decision.Error](err); !ok || de.Kind != decision.FailureUnreachable {
		t.Fatalf("err = %v, want an unreachable failure", err)
	}
	if calls.Load() != decision.MaxAttempts {
		t.Errorf("calls = %d, want %d: a failure after a redirect is retried", calls.Load(), decision.MaxAttempts)
	}
	if !strings.Contains(logs.String(), "retrying") {
		t.Fatalf("logs = %q, want the retry WARN", logs.String())
	}
	for _, leaked := range []string{"SECRETPATH", "SECRETQUERY", "/login"} {
		if strings.Contains(logs.String(), leaked) {
			t.Errorf("retry log names the redirect's Location (%s): %q", leaked, logs.String())
		}
	}
}

func TestEvaluateFollowsARedirectOnTheSameOrigin(t *testing.T) {
	var keys []string
	c := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("x-goog-api-key"))
		if r.URL.Path == "/v1beta/decisions" {
			http.Redirect(w, r, "/moved/v1beta/decisions", http.StatusPermanentRedirect)
			return
		}
		_, _ = w.Write(documentedResponse(t))
	})
	if _, err := c.Evaluate(t.Context(), triageRequest()); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if len(keys) != 2 || keys[1] != "test-key" {
		t.Errorf("keys seen = %q, want the key on both requests to the same origin", keys)
	}
}
