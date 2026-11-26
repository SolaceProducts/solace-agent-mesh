// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

func TestClassifyGraphFailure(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name               string
		status             int
		code               int
		want               graphFailureClass
		wantSendRetry      bool
		wantTransientRetry bool
	}{
		{name: "expired token under a 401", status: 401, code: 190, want: classCredentials},
		{name: "dead token wrapped in a 500 is not transient", status: 500, code: 190, want: classCredentials},
		{name: "dead token wrapped in a 429 is not rate limiting", status: 429, code: 190, want: classCredentials},
		{name: "dead session", status: 400, code: 102, want: classCredentials},
		{name: "capability denied", status: 403, code: 3, want: classCredentials},
		{name: "legacy permission code", status: 403, code: 10, want: classCredentials},
		{name: "bottom of Meta's permission band", status: 403, code: 200, want: classCredentials},
		{name: "middle of the permission band", status: 403, code: 294, want: classCredentials},
		{name: "top of the permission band", status: 403, code: 299, want: classCredentials},
		{name: "just past the permission band", status: 400, code: 300, want: classPermanent},

		{name: "401 with an unparseable body", status: 401, code: 0, want: classUnverified},
		{name: "403 with an unparseable body", status: 403, code: 0, want: classUnverified},
		{name: "403 carrying the app request limit", status: 403, code: 4, want: classRateLimited, wantSendRetry: true, wantTransientRetry: true},
		{name: "400 carrying the app request limit", status: 400, code: 4, want: classRateLimited, wantSendRetry: true, wantTransientRetry: true},

		{name: "429 with no envelope", status: 429, code: 0, want: classRateLimited, wantSendRetry: true, wantTransientRetry: true},
		{name: "pair rate limit under a 400", status: 400, code: 131056, want: classRateLimited, wantSendRetry: true, wantTransientRetry: true},
		{name: "business rate limit", status: 400, code: 80007, want: classRateLimited, wantSendRetry: true, wantTransientRetry: true},
		{name: "throughput reached", status: 400, code: 130429, want: classRateLimited, wantSendRetry: true, wantTransientRetry: true},
		{name: "bare 503", status: 503, code: 0, want: classTransient, wantTransientRetry: true},
		{name: "bare 500", status: 500, code: 0, want: classTransient, wantTransientRetry: true},
		{name: "window closed", status: 400, code: 131047, want: classPermanent},
		{name: "bad payload", status: 400, code: 100, want: classPermanent},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyGraphFailure(tt.status, tt.code); got != tt.want {
				t.Errorf("classifyGraphFailure(%d, %d) = %q, want %q", tt.status, tt.code, got, tt.want)
			}
			class := classifyGraphFailure(tt.status, tt.code)
			if got := retryRateLimitedOnly(class); got != tt.wantSendRetry {
				t.Errorf("retryRateLimitedOnly(%q) = %v, want %v — a send must never repeat a request Meta may have accepted", class, got, tt.wantSendRetry)
			}
			if got := retryTransient(class); got != tt.wantTransientRetry {
				t.Errorf("retryTransient(%q) = %v, want %v", class, got, tt.wantTransientRetry)
			}
		})
	}
}

func TestProbeAndRuntimeAgreeOnTheCredentialFamily(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		status     int
		code       int
		subcode    int
		wantGraph  graphFailureClass
		wantProbe  ProbeErrorClass
		hintQuotes string
	}{
		{status: 401, code: 190, wantGraph: classCredentials, wantProbe: ProbeClassTokenInvalid, hintQuotes: "System User token"},
		{status: 401, code: 190, subcode: 463, wantGraph: classCredentials, wantProbe: ProbeClassTokenExpired, hintQuotes: "expired"},
		{status: 401, code: 190, subcode: 467, wantGraph: classCredentials, wantProbe: ProbeClassTokenInvalid, hintQuotes: "revoked"},
		{status: 400, code: 102, wantGraph: classCredentials, wantProbe: ProbeClassTokenInvalid, hintQuotes: "System User token"},
		{status: 403, code: 200, wantGraph: classCredentials, wantProbe: ProbeClassPermissionDenied, hintQuotes: "whatsapp_business_management"},
		{status: 403, code: 294, wantGraph: classCredentials, wantProbe: ProbeClassPermissionDenied, hintQuotes: "whatsapp_business_management"},
		{status: 403, code: 299, wantGraph: classCredentials, wantProbe: ProbeClassPermissionDenied, hintQuotes: "whatsapp_business_management"},
		{status: 403, code: 10, wantGraph: classCredentials, wantProbe: ProbeClassPermissionDenied, hintQuotes: "whatsapp_business_management"},
		{status: 403, code: 3, wantGraph: classCredentials, wantProbe: ProbeClassTokenInvalid, hintQuotes: "Access Token"},
		{status: 401, code: 0, wantGraph: classUnverified, wantProbe: ProbeClassUnknown, hintQuotes: "egress proxy"},
		{status: 403, code: 0, wantGraph: classUnverified, wantProbe: ProbeClassUnknown, hintQuotes: "egress proxy"},
		{status: 403, code: 4, wantGraph: classRateLimited, wantProbe: ProbeClassRateLimited, hintQuotes: "rate limiting"},
		{status: 503, code: 0, wantGraph: classTransient, wantProbe: ProbeClassNetworkUnreachable, hintQuotes: "server error"},
	} {
		t.Run(fmt.Sprintf("%d/%d/%d", tt.status, tt.code, tt.subcode), func(t *testing.T) {
			t.Parallel()
			if got := classifyGraphFailure(tt.status, tt.code); got != tt.wantGraph {
				t.Errorf("classifyGraphFailure(%d, %d) = %q, want %q", tt.status, tt.code, got, tt.wantGraph)
			}
			class, hint := classifyProbeFailure(tt.status, graphError{Code: tt.code, Subcode: tt.subcode})
			if class != tt.wantProbe {
				t.Errorf("classifyProbeFailure(%d, %d/%d) = %q, want %q", tt.status, tt.code, tt.subcode, class, tt.wantProbe)
			}
			if !strings.Contains(hint, tt.hintQuotes) {
				t.Errorf("hint = %q, want it to name %q", hint, tt.hintQuotes)
			}
		})
	}
}

func TestSend_DoesNotRetryRejectedCredentials(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		status int
		body   string
	}{
		{"5xx wrapping an expired token", http.StatusServiceUnavailable, graphErrorBody(codeOAuthException, subcodeTokenExpired, "Error validating access token")},
		{"429 wrapping a revoked token", http.StatusTooManyRequests, graphErrorBody(codeOAuthException, subcodeTokenInvalid, "Error validating access token")},
		{"401 with a plain 190", http.StatusUnauthorized, graphErrorBody(codeOAuthException, 0, "Error validating access token")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			c, sleeps := newRetryTestClient(t, srv.URL)
			_, err := c.SendText(t.Context(), "15551234567", "hello")
			if err == nil {
				t.Fatal("expected an error for rejected credentials")
			}
			if calls.Load() != 1 {
				t.Errorf("HTTP calls = %d, want 1: a rejected credential must not be retried", calls.Load())
			}
			if len(*sleeps) != 0 {
				t.Errorf("backoff waits = %v, want none", *sleeps)
			}
			if !strings.Contains(err.Error(), "code 190") {
				t.Errorf("error should carry the numeric Meta code: %v", err)
			}
		})
	}
}

func TestSend_RetriesAppRequestLimitOutside429(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden} {
		t.Run(fmt.Sprintf("status %d", status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(graphErrorBody(4, 0, "Application request limit reached")))
					return
				}
				_, _ = w.Write([]byte(`{"messages":[{"id":"wamid.THROTTLE_OK"}]}`))
			}))
			defer srv.Close()

			a := newAdapterRig(t, newWAMock(t), "Builder")
			cfg := *a.cfg
			cfg.GraphAPIBaseURL = srv.URL
			a.cfg = &cfg
			a.client = NewClient(&cfg, discardLogger())

			target := outboundTarget{
				recipient: "15551234567", taskID: taskT1,
				userID: "user", agentName: "Builder", sessionID: "sess",
			}
			id := a.enqueue(kindText, target, outboundPayload{Text: "hello"}, "")
			if id == "" {
				t.Fatal("the text row was not queued")
			}
			waitQueueDrained(t, a)

			row, err := a.outbound.Get(context.Background(), id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if row.State != store.WhatsAppOutboundSent || row.Wamid != "wamid.THROTTLE_OK" {
				t.Errorf("row = %s/%q; want sent carrying the retry's wamid", row.State, row.Wamid)
			}
			if calls.Load() != 2 {
				t.Errorf("HTTP calls = %d, want 2: the rejection must be retried on its code alone", calls.Load())
			}
		})
	}
}

func TestSendMedia_CredentialFailureLogCarriesAuthClass(t *testing.T) {
	t.Parallel()
	const metaText = "Error validating access token: session for 15551234567 is invalid"
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(graphErrorBody(0, 0, metaText)))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(graphErrorBody(codeOAuthException, subcodeTokenExpired, metaText)))
	}))
	t.Cleanup(srv.Close)

	logs := &recordCollector{}
	logger := slog.New(logs)
	a := newAdapterRig(t, newWAMock(t), "Builder", logger)
	cfg := *a.cfg
	cfg.GraphAPIBaseURL = srv.URL
	cfg.MaxMediaSizeMB = 25
	a.cfg = &cfg
	a.client = NewClient(&cfg, logger)
	a.client.sleep = func(context.Context, time.Duration) error { return nil }

	const pdfMIME = "application/pdf"
	target := outboundTarget{
		recipient: "15551230001", taskID: taskT1,
		userID: "user", agentName: "Builder", sessionID: "sess",
	}
	if a.enqueue(kindMedia, target, outboundPayload{Media: &mediaPayload{
		Filename:   "report.pdf",
		MIMEType:   pdfMIME,
		MediaType:  mediaTypeForMIME(pdfMIME),
		InlineText: "%PDF-1.4 x",
	}}, "") == "" {
		t.Fatal("the media row was not queued")
	}
	waitQueueDrained(t, a)

	records := logs.all()
	var classified bool
	for _, r := range records {
		fields := map[string]string{}
		r.Attrs(func(at slog.Attr) bool {
			fields[at.Key] = at.Value.String()
			return true
		})
		for k, v := range fields {
			if strings.Contains(v, "15551234567") || strings.Contains(v, metaText) {
				t.Errorf("%s field %q echoes Meta's free-text message: %q", r.Level, k, v)
			}
		}
		if r.Level != slog.LevelError || fields["errorClass"] == "" {
			continue
		}
		classified = true
		if fields["errorClass"] != string(classCredentials) {
			t.Errorf("errorClass = %q, want %q", fields["errorClass"], classCredentials)
		}
		if fields["metaErrorCode"] != "190" {
			t.Errorf("metaErrorCode = %q, want 190", fields["metaErrorCode"])
		}
		if fields["metaErrorSubcode"] != "463" {
			t.Errorf("metaErrorSubcode = %q, want 463", fields["metaErrorSubcode"])
		}
	}
	if !classified {
		t.Fatalf("no Error record carried an errorClass field; got %d records", len(records))
	}
	var sawWarn bool
	for _, r := range records {
		if r.Level == slog.LevelWarn {
			sawWarn = true
		}
	}
	if !sawWarn {
		t.Error("the retry loop's per-attempt Warn never fired, so half the assertions above ran against nothing")
	}
}

func TestSafeGraphErrFields_TransportFailureIsClassified(t *testing.T) {
	t.Parallel()
	fields := safeGraphErrFields(errors.New("dial tcp 10.0.0.1:443: connect: connection refused"))
	if len(fields) < 2 || fields[0] != "errorClass" || fields[1] != string(classTransport) {
		t.Errorf("fields = %v, want them to lead with errorClass=%s", fields, classTransport)
	}
}
