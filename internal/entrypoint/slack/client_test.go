// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slack

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	goslack "github.com/slack-go/slack"
)

func TestNewSlackClient_RetriesARateLimitedCall(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"channel":"C1","ts":"1.0"}`))
	}))
	t.Cleanup(srv.Close)

	client := newSlackClient(&SlackConfig{BotToken: "xoxb-test", APIBaseURL: srv.URL + "/"})

	if _, _, err := client.PostMessageContext(t.Context(), "C1", goslack.MsgOptionText("hello", false)); err != nil {
		t.Fatalf("PostMessage: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
}

func TestNewSlackClient_FailsAtOnceOnALongRetryAfter(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)

	client := newSlackClient(&SlackConfig{BotToken: "xoxb-test", APIBaseURL: srv.URL + "/"})

	_, _, err := client.PostMessageContext(t.Context(), "C1", goslack.MsgOptionText("hello", false))
	var limited *goslack.RateLimitedError
	if !errors.As(err, &limited) {
		t.Fatalf("PostMessage error = %v, want a rate-limited error", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("calls = %d, want 1", got)
	}
}
