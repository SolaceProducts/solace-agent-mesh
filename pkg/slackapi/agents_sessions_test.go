// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package slackapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	goslack "github.com/slack-go/slack"
)

func TestSetSessionStatus_SendsAllFields(t *testing.T) {
	t.Parallel()

	var gotValues url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotValues = r.Form
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client := goslack.New("xoxb-test", goslack.OptionAPIURL(srv.URL+"/"))
	adapter := NewSlackAPIAdapter(client, "xoxb-test", WithDisabledRateLimiting(), WithAPIBaseURL(srv.URL+"/"))

	if err := adapter.SetSessionStatus(t.Context(), "C1", "1700000000.000001", "processing"); err != nil {
		t.Fatalf("SetSessionStatus: %v", err)
	}

	for field, want := range map[string]string{
		"token":      "xoxb-test",
		"channel_id": "C1",
		"thread_ts":  "1700000000.000001",
		"status":     "processing",
	} {
		if got := gotValues.Get(field); got != want {
			t.Errorf("form field %q = %q, want %q", field, got, want)
		}
	}
}

func TestSetSessionStatus_NonOKStatus_DoesNotAttemptJSONDecode(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("<html><body>503 Service Unavailable</body></html>"))
	}))
	defer srv.Close()

	client := goslack.New("xoxb-test", goslack.OptionAPIURL(srv.URL+"/"))
	adapter := NewSlackAPIAdapter(client, "xoxb-test", WithDisabledRateLimiting(), WithAPIBaseURL(srv.URL+"/"))

	err := adapter.SetSessionStatus(t.Context(), "C1", "ts", "processing")
	if err == nil {
		t.Fatal("expected an error for a 503 response")
	}
	if strings.Contains(err.Error(), "invalid character") || strings.Contains(err.Error(), "decode") {
		t.Errorf("error = %q, must report the status code rather than a JSON decode failure", err.Error())
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error = %q, want it to mention the 503 status", err.Error())
	}
}

func TestSetSessionStatus_OKFalse_ReturnsSlackError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
	}))
	defer srv.Close()

	client := goslack.New("xoxb-test", goslack.OptionAPIURL(srv.URL+"/"))
	adapter := NewSlackAPIAdapter(client, "xoxb-test", WithDisabledRateLimiting(), WithAPIBaseURL(srv.URL+"/"))

	err := adapter.SetSessionStatus(t.Context(), "C1", "ts", "processing")
	if err == nil || !strings.Contains(err.Error(), "invalid_auth") {
		t.Errorf("error = %v, want it to mention invalid_auth", err)
	}
}

func TestSetSessionStatus_ThrottleError(t *testing.T) {
	t.Parallel()

	rl := newRateLimiter()
	if err := rl.throttle(t.Context(), methodAgentsSessionsSetStatus); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	adapter := &slackAPIAdapterImpl{limiter: rl}
	if err := adapter.SetSessionStatus(ctx, "C1", "ts", "processing"); err == nil {
		t.Error("SetSessionStatus: expected throttle error")
	}
}
