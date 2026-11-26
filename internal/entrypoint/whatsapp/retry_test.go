// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newRetryTestClient(t *testing.T, url string) (*Client, *[]time.Duration) {
	t.Helper()
	c := NewClient(&WhatsAppConfig{
		AccessToken:     "tok",
		PhoneNumberID:   "555",
		GraphAPIVersion: "v26.0",
		GraphAPIBaseURL: url,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var sleeps []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	}
	return c, &sleeps
}

func TestSend_IsAlwaysOneAttempt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		wantIn string
	}{
		{"permanent 4xx", http.StatusBadRequest, `{"error":{"message":"invalid parameter","code":100}}`, "code 100"},
		{"rate limited", http.StatusTooManyRequests, `{"error":{"message":"still limited","code":130429}}`, "code 130429"},
		{"transient 5xx", http.StatusServiceUnavailable, `{"error":{"message":"unavailable"}}`, "status 503"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c, sleeps := newRetryTestClient(t, srv.URL)
			_, err := c.SendText(t.Context(), "15551234567", "hello")
			if err == nil {
				t.Fatal("expected an error, never a silent drop")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("error %q should carry %q", err, tc.wantIn)
			}
			if calls.Load() != 1 || len(*sleeps) != 0 {
				t.Errorf("calls = %d, waits = %v; want a single attempt and no backoff", calls.Load(), *sleeps)
			}
		})
	}
}

func TestUploadMedia_RetriesWithSameBody(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, b)
		mu.Unlock()
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":131056}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"media-123"}`))
	}))
	defer srv.Close()

	c, sleeps := newRetryTestClient(t, srv.URL)
	id, err := c.UploadMedia(t.Context(), []byte("file-bytes"), "text/plain", "a.txt")
	if err != nil {
		t.Fatalf("UploadMedia: %v", err)
	}
	if id != "media-123" {
		t.Errorf("media id = %q, want media-123", id)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || string(bodies[0]) != string(bodies[1]) {
		t.Errorf("retry must resend the identical multipart body (got %d bodies, equal=%v)",
			len(bodies), len(bodies) == 2 && string(bodies[0]) == string(bodies[1]))
	}
	if len(*sleeps) != 1 {
		t.Errorf("waits = %v, want one backoff", *sleeps)
	}
}

func TestParseGraphError_ToleratesGarbage(t *testing.T) {
	for _, body := range []string{"", "<html>502 Bad Gateway</html>", `{"error":"flat string"}`, `[1,2]`} {
		ge := parseGraphError([]byte(body))
		if ge.Code != 0 || retryTransient(classifyGraphFailure(http.StatusBadRequest, ge.Code)) {
			t.Errorf("body %q: got code %d; garbage must classify as not retryable", body, ge.Code)
		}
	}
	if ge := parseGraphError([]byte(`{"error":{"code":80007,"error_data":{"details":"throughput"}}}`)); ge.Code != 80007 || ge.ErrorData.Details != "throughput" {
		t.Errorf("well-formed envelope not parsed: %+v", ge)
	}
}

func TestRegainDelay(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   time.Duration
		ok     bool
	}{
		{"empty header", "", 0, false},
		{"malformed json", "{not json", 0, false},
		{"no regain field", `{"b":[{"call_count":10}]}`, 0, false},
		{"single entry minutes", `{"b":[{"estimated_time_to_regain_access":5}]}`, 5 * time.Minute, true},
		{"max across entries", `{"a":[{"estimated_time_to_regain_access":2}],"b":[{"estimated_time_to_regain_access":7},{"estimated_time_to_regain_access":3}]}`, 7 * time.Minute, true},
		{"zero regain", `{"b":[{"estimated_time_to_regain_access":0}]}`, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := regainDelay(tt.header)
			if got != tt.want || ok != tt.ok {
				t.Errorf("regainDelay(%q) = (%v, %v), want (%v, %v)", tt.header, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestRetryDelay_CapsLongHintAndFallback(t *testing.T) {
	if got := retryDelay(`{"b":[{"estimated_time_to_regain_access":60}]}`, 0, 0); got != maxRetryWait {
		t.Errorf("60-minute hint = %v, want capped at %v", got, maxRetryWait)
	}
	if got := retryDelay("", 0, 10); got != maxRetryWait {
		t.Errorf("4^10 fallback = %v, want capped at %v", got, maxRetryWait)
	}
}

func TestRetryDelay_PairRateLimitFloorsAtSixSeconds(t *testing.T) {
	want := []time.Duration{6 * time.Second, 6 * time.Second, 16 * time.Second}
	for attempt, w := range want {
		if got := retryDelay("", codePairRateLimit, attempt); got != w {
			t.Errorf("attempt %d = %v, want %v (1-per-6s pair limit)", attempt, got, w)
		}
	}
	if got := retryDelay("", codeBusinessRateLimit, 0); got != time.Second {
		t.Errorf("throughput code floored to %v; the 6s floor must apply only to the pair limit", got)
	}
}

func TestParseWhatsAppConfig_DisableRateLimiting(t *testing.T) {
	base := map[string]any{
		"access_token": "t", "phone_number_id": "p", "app_secret": "s", "verify_token": "v",
	}
	cfg, err := ParseWhatsAppConfig(base)
	if err != nil || cfg.DisableRateLimiting {
		t.Fatalf("default = %+v, %v; want rate limiting enabled", cfg, err)
	}
	base["disable_rate_limiting"] = true
	cfg, err = ParseWhatsAppConfig(base)
	if err != nil || !cfg.DisableRateLimiting {
		t.Fatalf("explicit disable = %+v, %v; want DisableRateLimiting true", cfg, err)
	}
}

type recordCollector struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *recordCollector) Enabled(context.Context, slog.Level) bool { return true }
func (c *recordCollector) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r)
	return nil
}
func (c *recordCollector) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *recordCollector) WithGroup(string) slog.Handler      { return c }

func (c *recordCollector) all() []slog.Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.records)
}

func TestSend_DoesNotRetry5xxSoAReplyIsNeverDeliveredTwice(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGatewayTimeout)
		_, _ = w.Write([]byte(`{"error":{"message":"Gateway timeout"}}`))
	}))
	defer srv.Close()

	c, sleeps := newRetryTestClient(t, srv.URL)
	if _, err := c.SendText(context.Background(), "15551230000", "hello"); err == nil {
		t.Fatal("a 504 send must surface an error, not be retried into a duplicate")
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 (a send Meta may already have accepted must not repeat)", got)
	}
	if len(*sleeps) != 0 {
		t.Errorf("waits = %v, want none", *sleeps)
	}
}

func TestMediaLegs_Retry5xx(t *testing.T) {
	var uploads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if uploads.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"Service temporarily unavailable"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"media-9"}`))
	}))
	defer srv.Close()

	c, _ := newRetryTestClient(t, srv.URL)
	id, err := c.UploadMedia(context.Background(), []byte("bytes"), "text/plain", "a.txt")
	if err != nil {
		t.Fatalf("UploadMedia should have retried the 503: %v", err)
	}
	if id != "media-9" || uploads.Load() != 2 {
		t.Errorf("id = %q after %d attempts, want media-9 after 2", id, uploads.Load())
	}
}

func TestClassifySendError_MetaCodeFourIsRetryable(t *testing.T) {
	ge := &graphAPIError{
		Op: "send", HTTPStatus: http.StatusBadRequest, Attempts: 1,
		GraphErr: graphError{Code: codeAPITooManyCalls, Message: "API Too Many Calls"},
	}
	code, _, retryable, _ := classifySendError(ge)
	if code != codeAPITooManyCalls || !retryable {
		t.Errorf("code = %d retryable = %v, want %d true", code, retryable, codeAPITooManyCalls)
	}
}

func TestClassifySendError_ServerErrorIsTerminal(t *testing.T) {
	ge := &graphAPIError{
		Op: "send", HTTPStatus: http.StatusGatewayTimeout, Attempts: 1,
		GraphErr: graphError{Message: "Gateway timeout"},
	}
	if _, _, retryable, _ := classifySendError(ge); retryable {
		t.Error("a 5xx send was classified retryable; that is a reply delivered twice")
	}
}

func TestSend_FailsFastOnPermanent4xx(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":131047,"message":"Re-engagement message required"}}`))
	}))
	defer srv.Close()

	c, _ := newRetryTestClient(t, srv.URL)
	if _, err := c.SendText(context.Background(), "15551230000", "hello"); err == nil {
		t.Fatal("expected a permanent failure")
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 (permanent 4xx must not retry)", got)
	}
}

func TestRetry_RegainHintIgnoredForNonRateLimitFailure(t *testing.T) {
	var attempts atomic.Int32
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/v26.0/MEDIA1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(headerBusinessUsage, `{"b":[{"estimated_time_to_regain_access":30}]}`)
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"unavailable"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"url":"` + srv.URL + `/media-bytes/MEDIA1","mime_type":"image/jpeg","file_size":4,"id":"MEDIA1"}`))
	})
	mux.HandleFunc("/media-bytes/MEDIA1", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("JPEG"))
	})

	c, sleeps := newRetryTestClient(t, srv.URL)
	if _, _, err := c.FetchMedia(context.Background(), "MEDIA1", 0); err != nil {
		t.Fatalf("FetchMedia: %v", err)
	}
	if want := []time.Duration{time.Second}; !slices.Equal(*sleeps, want) {
		t.Errorf("waits = %v, want %v — a 5xx must not inherit the throttled bucket's 30-minute hint", *sleeps, want)
	}
}

func TestDownloadMedia_RetriesMidStreamReset(t *testing.T) {
	var dlAttempts atomic.Int32
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/v26.0/MEDIA1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"` + srv.URL + `/media-bytes/MEDIA1","mime_type":"application/pdf","file_size":8,"id":"MEDIA1"}`))
	})
	mux.HandleFunc("/media-bytes/MEDIA1", func(w http.ResponseWriter, _ *http.Request) {
		if dlAttempts.Add(1) == 1 {
			w.Header().Set("Content-Length", "8")
			_, _ = w.Write([]byte("PDF-"))
			panic(http.ErrAbortHandler)
		}
		_, _ = w.Write([]byte("PDF-DATA"))
	})

	c, _ := newRetryTestClient(t, srv.URL)
	data, _, err := c.FetchMedia(context.Background(), "MEDIA1", 0)
	if err != nil {
		t.Fatalf("a mid-stream reset must be retried: %v", err)
	}
	if string(data) != "PDF-DATA" {
		t.Errorf("data = %q, want PDF-DATA", data)
	}
	if got := dlAttempts.Load(); got != 2 {
		t.Errorf("download attempts = %d, want 2", got)
	}
}

func TestDownloadMedia_CapExceededIsNotRetried(t *testing.T) {
	var dlAttempts atomic.Int32
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/v26.0/BIG", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"` + srv.URL + `/media-bytes/BIG","mime_type":"application/pdf","id":"BIG"}`))
	})
	mux.HandleFunc("/media-bytes/BIG", func(w http.ResponseWriter, _ *http.Request) {
		dlAttempts.Add(1)
		_, _ = w.Write(bytes.Repeat([]byte("x"), 64))
	})

	c, _ := newRetryTestClient(t, srv.URL)
	_, _, err := c.FetchMedia(context.Background(), "BIG", 8)
	if !errors.Is(err, ErrMediaTooLarge) {
		t.Fatalf("err = %v, want ErrMediaTooLarge", err)
	}
	if got := dlAttempts.Load(); got != 1 {
		t.Errorf("download attempts = %d, want 1", got)
	}
}

func TestFetchMedia_RetriesBothLegs(t *testing.T) {
	var metaAttempts, dlAttempts atomic.Int32
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	mux.HandleFunc("/media-bytes/MEDIA1", func(w http.ResponseWriter, _ *http.Request) {
		if dlAttempts.Add(1) < 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte("JPEGDATA"))
	})
	mux.HandleFunc("/v26.0/MEDIA1", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if metaAttempts.Add(1) < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":{"message":"internal"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"url":"` + srv.URL + `/media-bytes/MEDIA1","mime_type":"image/jpeg","file_size":8,"id":"MEDIA1"}`))
	})

	c, _ := newRetryTestClient(t, srv.URL)
	data, mime, err := c.FetchMedia(context.Background(), "MEDIA1", 0)
	if err != nil {
		t.Fatalf("FetchMedia should have retried both legs: %v", err)
	}
	if string(data) != "JPEGDATA" || mime != "image/jpeg" {
		t.Errorf("got %q/%q, want JPEGDATA/image/jpeg", data, mime)
	}
	if metaAttempts.Load() != 2 || dlAttempts.Load() != 2 {
		t.Errorf("attempts = metadata %d, download %d; want 2 and 2", metaAttempts.Load(), dlAttempts.Load())
	}
}

func TestDownloadMedia_NotTruncatedByRetryPath(t *testing.T) {
	const size = 3 << 20
	blob := bytes.Repeat([]byte("x"), size)

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/media-bytes/BIG", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(blob) })
	mux.HandleFunc("/v26.0/BIG", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"` + srv.URL + `/media-bytes/BIG","mime_type":"application/pdf","file_size":` + strconv.Itoa(size) + `,"id":"BIG"}`))
	})

	c, _ := newRetryTestClient(t, srv.URL)
	data, _, err := c.FetchMedia(context.Background(), "BIG", 0)
	if err != nil {
		t.Fatalf("FetchMedia: %v", err)
	}
	if len(data) != size {
		t.Errorf("downloaded %d bytes, want %d — the retry path must not truncate", len(data), size)
	}
}

func TestRandomJitter_AddsAtMostAQuarterAndNeverSubtracts(t *testing.T) {
	for _, base := range []time.Duration{0, -time.Second, time.Second, 4 * time.Second, maxRetryWait} {
		for range 200 {
			got := randomJitter(base)
			if got < base {
				t.Fatalf("randomJitter(%v) = %v; the floor is what retryDelay computed and jitter only ever adds", base, got)
			}
			if ceiling := base + base/4; base > 0 && got > ceiling {
				t.Fatalf("randomJitter(%v) = %v, want at most %v", base, got, ceiling)
			}
		}
	}
	seen := map[time.Duration]bool{}
	for range 200 {
		seen[randomJitter(4*time.Second)] = true
	}
	if len(seen) < 2 {
		t.Errorf("randomJitter returned one value across 200 calls (%v); it is meant to spread", seen)
	}
}

func TestClassifySendError_TransportFailures(t *testing.T) {
	messagesPOST := func(cause error) error {
		return fmt.Errorf("%w: whatsapp send: %w", errUserFacingPOST, cause)
	}
	dial := &net.OpError{Op: "dial", Err: errors.New("connection refused")}
	reset := &net.OpError{Op: "read", Err: errors.New("connection reset by peer")}

	for _, tc := range []struct {
		name      string
		err       error
		retryable bool
	}{
		{"dial failure on the POST never reached the wire", messagesPOST(dial), true},
		{"TLS handshake failure carried no application data",
			messagesPOST(tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}), true},
		{"reset after the request was written may have been accepted", messagesPOST(reset), false},
		{"client timeout leaves Meta's side unknowable", messagesPOST(context.DeadlineExceeded), false},
		{"the same reset on the upload leg is free to repeat",
			fmt.Errorf("whatsapp media upload: %w", reset), true},
		{"an upload timeout is free to repeat too",
			fmt.Errorf("whatsapp media upload: %w", context.DeadlineExceeded), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, retryable, _ := classifySendError(tc.err); retryable != tc.retryable {
				t.Errorf("retryable = %v, want %v", retryable, tc.retryable)
			}
		})
	}
}

func TestSendText_TransportFailureIsMarkedAsTheUserFacingPOST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("test server does not support hijacking")
			return
		}
		conn, _, hijackErr := hijacker.Hijack()
		if hijackErr != nil {
			t.Errorf("hijack: %v", hijackErr)
			return
		}
		_ = conn.Close()
	}))
	defer srv.Close()

	c, _ := newRetryTestClient(t, srv.URL)
	_, err := c.SendText(context.Background(), "15551230000", "hello")
	if err == nil {
		t.Fatal("a dropped connection must surface an error")
	}
	if !errors.Is(err, errUserFacingPOST) {
		t.Errorf("error = %v, want it marked as the user-facing POST so the terminal rule applies", err)
	}
	if _, _, retryable, _ := classifySendError(err); retryable {
		t.Error("a lost /messages response was classified retryable; that is a reply delivered twice")
	}
}
