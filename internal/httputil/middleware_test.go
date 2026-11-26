// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httputil //nolint:revive // internal package, no conflict risk

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestRequestIDMiddleware(t *testing.T) {
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := RequestIDFromContext(r.Context())
		if id == "" {
			t.Error("expected request ID in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("expected X-Request-ID header")
	}
}

func TestCORSMiddleware_MatchingOrigin(t *testing.T) {
	cors := CORSMiddleware([]string{"http://localhost:3000"}, nil)
	handler := cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Errorf("expected matching origin, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
	if rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("expected Allow-Credentials: true")
	}
}

func TestCORSMiddleware_NonMatchingOrigin(t *testing.T) {
	cors := CORSMiddleware([]string{"http://localhost:3000"}, nil)
	handler := cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://evil.com")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("expected no CORS headers for non-matching origin, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSMiddleware_Wildcard(t *testing.T) {
	cors := CORSMiddleware(nil, nil)
	handler := cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://anything.com")
	handler.ServeHTTP(rec, req)

	if rec.Header().Get("Access-Control-Allow-Origin") != "http://anything.com" {
		t.Error("expected wildcard to allow any origin")
	}
}

func TestCORSMiddleware_Preflight(t *testing.T) {
	cors := CORSMiddleware(nil, nil)
	handler := cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/test", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 for preflight, got %d", rec.Code)
	}
	methods := rec.Header().Get("Access-Control-Allow-Methods")
	if !strings.Contains(methods, "POST") {
		t.Errorf("expected Access-Control-Allow-Methods to contain POST, got %q", methods)
	}
	headers := rec.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(headers, "Authorization") {
		t.Errorf("expected Access-Control-Allow-Headers to contain Authorization, got %q", headers)
	}
}

func TestCORSMiddleware_PreflightMinimal(t *testing.T) {
	cors := CORSMiddleware(nil, nil)
	handler := cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/test", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "GET")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 for preflight, got %d", rec.Code)
	}
	methods := rec.Header().Get("Access-Control-Allow-Methods")
	if !strings.Contains(methods, "GET") {
		t.Errorf("expected Access-Control-Allow-Methods to contain GET, got %q", methods)
	}
}

func TestCORSMiddleware_RegexMatch(t *testing.T) {
	re := regexp.MustCompile(`^http://localhost:\d+$`)
	cors := CORSMiddleware([]string{"http://example.com"}, re)
	handler := cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://example.com")
	handler.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://example.com" {
		t.Error("expected static origin match")
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://localhost:54321")
	handler.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:54321" {
		t.Errorf("expected regex match, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://evil.com")
	handler.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("expected no CORS headers for non-matching origin")
	}
}

func TestCORSMiddleware_PreflightEchoesCustomHeaders(t *testing.T) {
	cors := CORSMiddleware(nil, nil)
	handler := cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/test", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "X-Custom-Header")
	handler.ServeHTTP(rec, req)

	headers := rec.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(headers, "X-Custom-Header") {
		t.Errorf("expected echoed X-Custom-Header in Allow-Headers, got %q", headers)
	}
}

func TestMaxBytesHandler_OversizedBody(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1024)
		_, err := r.Body.Read(buf)
		if err != nil {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	handler := Chain(
		RequestIDMiddleware,
		CORSMiddleware(nil, nil),
	)(http.MaxBytesHandler(inner, 100))

	rec := httptest.NewRecorder()
	body := strings.NewReader(strings.Repeat("x", 200))
	req := httptest.NewRequest("POST", "/test", body)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413 for oversized body, got %d", rec.Code)
	}
}

func TestRequestIDMiddleware_HonorsCallerHeader(t *testing.T) {
	var captured string
	handler := RequestIDMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = RequestIDFromContext(r.Context())
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-Id", "upstream-trace-abc")
	handler.ServeHTTP(rec, req)

	if captured != "upstream-trace-abc" {
		t.Errorf("expected ctx ID from header, got %q", captured)
	}
	if rec.Header().Get("X-Request-ID") != "upstream-trace-abc" {
		t.Errorf("expected response header echoed back, got %q", rec.Header().Get("X-Request-ID"))
	}
}

func TestLoggerFromContext_IncludesRequestID(t *testing.T) {
	var buf bytes.Buffer
	originalDefault := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(originalDefault) })

	handler := RequestIDMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		LoggerFromContext(r.Context()).Info("handled request")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-Id", "trace-xyz")
	handler.ServeHTTP(rec, req)

	if !strings.Contains(buf.String(), "requestID=trace-xyz") {
		t.Errorf("expected log to carry requestID, got %q", buf.String())
	}
}

func TestLoggerFromContext_FallbackWhenEmpty(t *testing.T) {
	logger := LoggerFromContext(t.Context())
	if logger == nil {
		t.Fatal("expected fallback logger, got nil")
	}
}

func TestMaxBytesMiddleware_PrecedesDownstream(t *testing.T) {
	var authObserved bool
	authMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authObserved = true
			_, _ = http.MaxBytesReader(w, r.Body, 1024).Read(make([]byte, 1024))
			next.ServeHTTP(w, r)
		})
	}

	innerCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := Chain(
		MaxBytesMiddleware(10),
		authMW,
	)(inner)

	rec := httptest.NewRecorder()
	body := strings.NewReader(strings.Repeat("x", 1000))
	req := httptest.NewRequest("POST", "/test", body)
	handler.ServeHTTP(rec, req)

	if authObserved {
		t.Errorf("auth middleware observed an oversize request — MaxBytes should have short-circuited first")
	}
	if innerCalled {
		t.Errorf("inner handler was called on an oversize request")
	}
}

func TestMaxBytesMiddleware_AllowsSmallBody(t *testing.T) {
	innerCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	})

	handler := MaxBytesMiddleware(100)(inner)

	rec := httptest.NewRecorder()
	body := strings.NewReader("hello")
	req := httptest.NewRequest("POST", "/test", body)
	handler.ServeHTTP(rec, req)

	if !innerCalled {
		t.Errorf("inner handler not called for in-cap request")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestMiddlewareErrors_UseStandardShape(t *testing.T) {
	cases := []struct {
		name string
		exec func() *httptest.ResponseRecorder
	}{
		{
			name: "MaxBytes 413",
			exec: func() *httptest.ResponseRecorder {
				h := MaxBytesMiddleware(10)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				}))
				rec := httptest.NewRecorder()
				req := httptest.NewRequest("POST", "/x", strings.NewReader(strings.Repeat("a", 100)))
				h.ServeHTTP(rec, req)
				return rec
			},
		},
		{
			name: "RequireJSON 415",
			exec: func() *httptest.ResponseRecorder {
				h := RequireJSONMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				}))
				rec := httptest.NewRecorder()
				req := httptest.NewRequest("POST", "/x", strings.NewReader("hello"))
				req.Header.Set("Content-Type", "text/plain")
				h.ServeHTTP(rec, req)
				return rec
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := c.exec()
			if rec.Header().Get("Content-Type") != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", rec.Header().Get("Content-Type"))
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("response body is not JSON: %v (raw: %q)", err, rec.Body.String())
			}
			for _, key := range []string{"detail", "message", "validationDetails"} {
				if _, ok := body[key]; !ok {
					t.Errorf("response body missing key %q (got %v)", key, body)
				}
			}
		})
	}
}

func TestRequireJSONMiddleware_TextPlainBodyReturns415(t *testing.T) {
	innerCalled := false
	handler := RequireJSONMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions", strings.NewReader(`{"x":1}`))
	req.Header.Set("Content-Type", "text/plain")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected 415, got %d", rec.Code)
	}
	if innerCalled {
		t.Error("handler should not have been called for non-JSON body")
	}
}

func TestRequireJSONMiddleware_ApplicationJSONPasses(t *testing.T) {
	innerCalled := false
	handler := RequireJSONMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/v1/sessions", strings.NewReader(`{"x":1}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if !innerCalled {
		t.Error("handler should have been called for application/json body")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestRequireJSONMiddleware_ApplicationJSONWithCharsetPasses(t *testing.T) {
	innerCalled := false
	handler := RequireJSONMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	for _, ct := range []string{
		"application/json; charset=utf-8",
		"application/json;charset=UTF-8",
		"APPLICATION/JSON",
		"  application/json  ",
	} {
		innerCalled = false
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/api/v1/sessions", strings.NewReader(`{"x":1}`))
		req.Header.Set("Content-Type", ct)
		handler.ServeHTTP(rec, req)
		if !innerCalled || rec.Code != http.StatusOK {
			t.Errorf("Content-Type %q: expected handler invoked + 200, got called=%v code=%d",
				ct, innerCalled, rec.Code)
		}
	}
}

func TestRequireJSONMiddleware_GETBypassed(t *testing.T) {
	innerCalled := false
	handler := RequireJSONMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/sessions", nil)
	handler.ServeHTTP(rec, req)

	if !innerCalled {
		t.Error("GET request should not be filtered by RequireJSONMiddleware")
	}
}

func TestRequireJSONMiddleware_DeleteWithoutBodyPasses(t *testing.T) {
	innerCalled := false
	handler := RequireJSONMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/sessions/abc", nil)
	handler.ServeHTTP(rec, req)

	if !innerCalled {
		t.Error("DELETE without a body should not be filtered")
	}
}

func TestRequireJSONMiddleware_DeleteWithJSONBodyPasses(t *testing.T) {
	innerCalled := false
	handler := RequireJSONMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/v1/share/abc/users", strings.NewReader(`{"users":["a"]}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if !innerCalled {
		t.Error("DELETE with JSON body should pass")
	}
}

func TestCompileCORSOriginRegex_AnchoredEquivalent(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	re, err := CompileCORSOriginRegex(`^https://.*\.example\.test$`, logger)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !re.MatchString("https://x.example.test") {
		t.Error("expected match for https://x.example.test")
	}
	if re.MatchString("https://x.example.test.attacker.com") {
		t.Error("anchored regex must NOT match a confused-suffix origin")
	}
}

func TestCompileCORSOriginRegex_AutoAnchorsUnanchored(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	re, err := CompileCORSOriginRegex(`https://.*\.example\.test`, logger)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !re.MatchString("https://x.example.test") {
		t.Error("expected match for https://x.example.test after auto-anchor")
	}
	if re.MatchString("https://x.example.test.attacker.com") {
		t.Error("auto-anchored regex must reject a confused-suffix origin")
	}
	if !strings.Contains(buf.String(), "auto-anchoring") {
		t.Errorf("expected WARN log about auto-anchoring, got %q", buf.String())
	}
}

func TestCompileCORSOriginRegex_NoLogWhenAnchored(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	if _, err := CompileCORSOriginRegex(`^https://x\.example\.test$`, logger); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "auto-anchoring") {
		t.Errorf("expected no WARN for fully anchored pattern, got %q", buf.String())
	}
}

func TestCompileCORSOriginRegex_EmptyReturnsNil(t *testing.T) {
	re, err := CompileCORSOriginRegex("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if re != nil {
		t.Error("expected nil regex for empty input")
	}
}

func TestCompileCORSOriginRegex_InvalidRejected(t *testing.T) {
	if _, err := CompileCORSOriginRegex(`(unbalanced`, nil); err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestCORSMiddleware_AutoAnchoredRegexRejectsSubstringMatch(t *testing.T) {
	re, err := CompileCORSOriginRegex(`https://.*\.example\.test`, nil)
	if err != nil {
		t.Fatal(err)
	}
	cors := CORSMiddleware([]string{"http://example.com"}, re)
	handler := cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://x.example.test")
	handler.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://x.example.test" {
		t.Errorf("expected match for https://x.example.test, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "https://x.example.test.attacker.com")
	handler.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("expected substring-match attacker origin to be rejected, got %q", rec.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestChain_ExecutionOrder(t *testing.T) {
	var order []string
	mw := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	handler := Chain(mw("first"), mw("second"), mw("third"))(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(rec, req)

	expected := []string{"first", "second", "third"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d middleware calls, got %d", len(expected), len(order))
	}
	for i, name := range expected {
		if order[i] != name {
			t.Errorf("middleware %d: expected %q, got %q", i, name, order[i])
		}
	}
}

func TestMaxBytesMiddleware_OnRejectHook(t *testing.T) {
	t.Run("fires on oversize 413", func(t *testing.T) {
		var gotStatus int
		var gotReq *http.Request
		h := MaxBytesMiddleware(10, func(r *http.Request, status int) {
			gotReq = r
			gotStatus = status
		})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			t.Error("downstream handler must not run on oversize body")
		}))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/x", strings.NewReader(strings.Repeat("a", 100)))
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413", rec.Code)
		}
		if gotStatus != http.StatusRequestEntityTooLarge {
			t.Errorf("onReject status = %d, want 413", gotStatus)
		}
		if gotReq == nil || gotReq.Method != "POST" {
			t.Errorf("onReject did not receive the request")
		}
	})

	t.Run("silent for in-cap request", func(t *testing.T) {
		called := false
		h := MaxBytesMiddleware(100, func(_ *http.Request, _ int) { called = true })(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusOK)
			}))

		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/x", strings.NewReader("hello"))
		h.ServeHTTP(rec, req)

		if called {
			t.Error("onReject fired for an in-cap request")
		}
	})
}
