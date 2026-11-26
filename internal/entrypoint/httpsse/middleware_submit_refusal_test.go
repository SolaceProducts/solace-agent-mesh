// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package httpsse

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type recordedRefusal struct {
	traceID   string
	surface   string
	reason    string
	sessionID string
}

type fakeRefusalReporter struct {
	mu      sync.Mutex
	reports []recordedRefusal
}

func (f *fakeRefusalReporter) ReportSubmitRejected(rejection entrypoint.SubmitRejection) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reports = append(f.reports, recordedRefusal{
		traceID:   rejection.TraceID,
		surface:   rejection.Surface,
		reason:    rejection.Reason,
		sessionID: rejection.SessionID,
	})
}

func (f *fakeRefusalReporter) all() []recordedRefusal {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedRefusal(nil), f.reports...)
}

func refusingMiddleware(status int) func(http.Handler) http.Handler {
	return func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"detail":"Not authenticated"}`))
		})
	}
}

func acceptingMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return next }
}

func testRefusalLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRefusedSubmitsAreReported(t *testing.T) {
	cases := []struct {
		name          string
		handlerStatus int
		guard         func(http.Handler) http.Handler
		method        string
		path          string
		wantReport    bool
	}{
		{"an unauthenticated submit", http.StatusOK, refusingMiddleware(http.StatusUnauthorized), http.MethodPost, "/api/v1/message:stream", true},
		{"a submit refused for scope", http.StatusOK, refusingMiddleware(http.StatusForbidden), http.MethodPost, "/api/v1/message:send", true},
		{"a submit refused with a trailing slash", http.StatusOK, refusingMiddleware(http.StatusUnauthorized), http.MethodPost, "/api/v1/message:send/", true},
		{"a refusal on another endpoint", http.StatusOK, refusingMiddleware(http.StatusUnauthorized), http.MethodPost, "/api/v1/sessions", false},
		{"a refusal on a read of the submit path", http.StatusOK, refusingMiddleware(http.StatusUnauthorized), http.MethodGet, "/api/v1/message:stream", false},
		{"a middleware answering a submit for some other reason", http.StatusOK, refusingMiddleware(http.StatusFound), http.MethodPost, "/api/v1/message:send", false},
		{"a submit the handler itself refused", http.StatusForbidden, acceptingMiddleware(), http.MethodPost, "/api/v1/message:stream", false},
		{"a submit that ran", http.StatusOK, acceptingMiddleware(), http.MethodPost, "/api/v1/message:stream", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reporter := &fakeRefusalReporter{}
			handlerCalls := 0
			handler := reportRefusedSubmits(reporter, testRefusalLogger(), c.guard)(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					handlerCalls++
					w.WriteHeader(c.handlerStatus)
				}))

			r := httptest.NewRequest(c.method, c.path, strings.NewReader("{}"))
			r.Header.Set(httputil.HeaderClientSurface, httputil.SurfaceCLI)
			handler.ServeHTTP(httptest.NewRecorder(), r)

			reports := reporter.all()
			if !c.wantReport {
				if len(reports) != 0 {
					t.Fatalf("reported %+v, want nothing", reports)
				}
				return
			}
			if len(reports) != 1 {
				t.Fatalf("reported %d times, want exactly 1: %+v", len(reports), reports)
			}
			if handlerCalls != 0 {
				t.Errorf("the handler ran %d times, so this refusal was not one the middleware decided", handlerCalls)
			}
			if reports[0].reason != productanalytics.RejectForbidden {
				t.Errorf("reason = %q, want %q", reports[0].reason, productanalytics.RejectForbidden)
			}
			if reports[0].surface != httputil.SurfaceCLI {
				t.Errorf("surface = %q, want %q", reports[0].surface, httputil.SurfaceCLI)
			}
			if reports[0].traceID == "" {
				t.Error("traceId is empty: an auth-refused submit has no request-minted trace, but it should still get one for correlation")
			}
		})
	}
}

func TestReportingARefusalLeavesTheResponseAlone(t *testing.T) {
	reporter := &fakeRefusalReporter{}
	handler := reportRefusedSubmits(reporter, testRefusalLogger(), refusingMiddleware(http.StatusUnauthorized))(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("the handler must not run for a refused request")
		}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/message:stream", strings.NewReader("{}")))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if got := rec.Body.String(); got != `{"detail":"Not authenticated"}` {
		t.Errorf("body = %q, want the refusal the auth middleware wrote", got)
	}
}

type captureRefusalSink struct {
	mu     sync.Mutex
	events []productanalytics.Event
}

func (s *captureRefusalSink) Send(_ context.Context, events []productanalytics.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, events...)
	return nil
}

func (s *captureRefusalSink) named(name productanalytics.EventName) []productanalytics.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []productanalytics.Event
	for _, ev := range s.events {
		if ev.Name == name {
			out = append(out, ev)
		}
	}
	return out
}

type refusingAuthProvider struct{}

func (refusingAuthProvider) Middleware() func(http.Handler) http.Handler {
	return refusingMiddleware(http.StatusUnauthorized)
}
func (refusingAuthProvider) RegisterRoutes(*http.ServeMux) {}
func (refusingAuthProvider) ProviderType() string          { return auth.ProviderTypeEnterprise }

func TestTheAdapterWiresRefusedSubmitReporting(t *testing.T) {
	const providerName = "refusing-fake"
	auth.RegisterProviderFactory("test-gw:"+providerName, func(map[string]any) (auth.AuthProvider, error) {
		return refusingAuthProvider{}, nil
	})
	t.Cleanup(func() { auth.UnregisterProviderFactory("test-gw:" + providerName) })

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	sink := &captureRefusalSink{}
	analytics := productanalytics.NewService(productanalytics.Options{
		Config:  productanalytics.ServiceConfig{FlushInterval: 10 * time.Millisecond},
		Sink:    sink,
		Consent: productanalytics.ConsentFunc(func(context.Context) bool { return true }),
		Identity: productanalytics.IdentityFunc(func(context.Context) productanalytics.Identity {
			return productanalytics.Identity{Marker: "install-marker", BindingKey: []byte("binding-key")}
		}),
		Logger: testRefusalLogger(),
	})

	adapter := NewHTTPSSEAdapter()
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":                  "test-ns",
		"session_secret_key":         "test-secret",
		"gateway_id":                 "test-gw",
		"fastapi_host":               "127.0.0.1",
		"fastapi_port":               0,
		"frontend_use_authorization": true,
		"external_auth_provider":     providerName,
	})
	core.SetServices(runtime.NewRuntime(brk, nil,
		runtime.WithLogger(testRefusalLogger()),
		runtime.WithUsageReporter(analytics),
	).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := analytics.Start(context.Background()); err != nil {
		t.Fatalf("start analytics: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = analytics.Stop(ctx)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/message:stream", strings.NewReader(`{"jsonrpc":"2.0"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httputil.HeaderClientSurface, httputil.SurfaceCLI)
	rec := httptest.NewRecorder()
	adapter.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d — the request never reached the auth provider", rec.Code, http.StatusUnauthorized)
	}

	var delivered []productanalytics.Event
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		delivered = sink.named(productanalytics.EventTaskSubmitRejected)
		if len(delivered) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(delivered) != 1 {
		t.Fatalf("task:submit_rejected delivered %d times, want 1", len(delivered))
	}
	if got := delivered[0].Properties[productanalytics.PropRejectReason]; got != productanalytics.RejectForbidden {
		t.Errorf("rejectReason = %v, want %q", got, productanalytics.RejectForbidden)
	}
	if got := delivered[0].Properties[productanalytics.PropSubmittedFrom]; got != httputil.SurfaceCLI {
		t.Errorf("submittedFrom = %v, want %q", got, httputil.SurfaceCLI)
	}
}

func TestTheAdapterReportsASubmitTheKeyGateRefuses(t *testing.T) {
	const providerName = "key-stamping-fake"
	identity := auth.NewTestSubject(auth.SubjectFields{UserID: "system:ci", Authenticated: true, AuthMethod: auth.APIKeyAuthMethod, Scopes: []string{"*"}})
	auth.RegisterProviderFactory("test-gw:"+providerName, func(map[string]any) (auth.AuthProvider, error) {
		return stampingAuthProvider{identity: identity}, nil
	})
	t.Cleanup(func() { auth.UnregisterProviderFactory("test-gw:" + providerName) })

	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	sink := &captureRefusalSink{}
	analytics := productanalytics.NewService(productanalytics.Options{
		Config:  productanalytics.ServiceConfig{FlushInterval: 10 * time.Millisecond},
		Sink:    sink,
		Consent: productanalytics.ConsentFunc(func(context.Context) bool { return true }),
		Identity: productanalytics.IdentityFunc(func(context.Context) productanalytics.Identity {
			return productanalytics.Identity{Marker: "install-marker", BindingKey: []byte("binding-key")}
		}),
		Logger: testRefusalLogger(),
	})

	adapter := NewHTTPSSEAdapter()
	core := entrypoint.NewGatewayCore(adapter, map[string]any{
		"namespace":                  "test-ns",
		"session_secret_key":         "test-secret",
		"gateway_id":                 "test-gw",
		"fastapi_host":               "127.0.0.1",
		"fastapi_port":               0,
		"frontend_use_authorization": true,
		"external_auth_provider":     providerName,
	})
	core.SetServices(runtime.NewRuntime(brk, nil,
		runtime.WithLogger(testRefusalLogger()),
		runtime.WithUsageReporter(analytics),
	).Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := analytics.Start(context.Background()); err != nil {
		t.Fatalf("start analytics: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = analytics.Stop(ctx)
	})

	read := httptest.NewRecorder()
	adapter.handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/v1/agentCards", nil))
	if read.Code != http.StatusForbidden {
		t.Fatalf("GET /api/v1/agentCards status = %d, want %d — the key gate did not refuse the read", read.Code, http.StatusForbidden)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/message:stream", strings.NewReader(`{"jsonrpc":"2.0"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(httputil.HeaderClientSurface, httputil.SurfaceCLI)
	rec := httptest.NewRecorder()
	adapter.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d — the key gate did not refuse the submit", rec.Code, http.StatusForbidden)
	}

	var delivered []productanalytics.Event
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		delivered = sink.named(productanalytics.EventTaskSubmitRejected)
		if len(delivered) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(delivered) != 1 {
		t.Fatalf("task:submit_rejected delivered %d times, want 1 — the refused read must not count as a submit", len(delivered))
	}
	if got := delivered[0].Properties[productanalytics.PropRejectReason]; got != productanalytics.RejectForbidden {
		t.Errorf("rejectReason = %v, want %q", got, productanalytics.RejectForbidden)
	}
	if got := delivered[0].Properties[productanalytics.PropSubmittedFrom]; got != httputil.SurfaceCLI {
		t.Errorf("submittedFrom = %v, want %q", got, httputil.SurfaceCLI)
	}
}
