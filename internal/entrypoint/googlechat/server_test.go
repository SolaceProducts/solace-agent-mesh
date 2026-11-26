// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type stubVerifier struct{ accept string }

func (v stubVerifier) Verify(_ context.Context, bearer string) error {
	if bearer == v.accept {
		return nil
	}
	return errUnauthenticated
}

const dmEventJSON = `{"type":"MESSAGE","space":{"name":"spaces/D","type":"DM"},"user":{"name":"users/1","email":"ada@example.com","type":"HUMAN"},"message":{"name":"spaces/D/messages/M","text":"hi"}}`

func newHTTPSAdapter(t *testing.T) *Adapter {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	a := NewAdapter(nil)
	a.logger = slog.New(slog.DiscardHandler)
	a.ctx, a.cancel = context.WithCancel(context.Background())
	t.Cleanup(a.cancel)
	spool, err := openBrokerSpool(a.ctx, brk, "ns", "gw-https", a.logger)
	if err != nil {
		t.Fatal(err)
	}
	a.spool, a.sub = spool, spool
	a.verifier = stubVerifier{accept: "good-token"}
	return a
}

func postEvent(t *testing.T, a *Adapter, bearer, body string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, eventsPath, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	a.eventsHandler().ServeHTTP(rec, req)
	return rec.Code
}

func TestEventsHandler_RejectsUnsignedAndForgedRequests(t *testing.T) {
	a := newHTTPSAdapter(t)
	if code := postEvent(t, a, "", dmEventJSON); code != http.StatusUnauthorized {
		t.Errorf("no bearer = %d, want 401", code)
	}
	if code := postEvent(t, a, "forged", dmEventJSON); code != http.StatusUnauthorized {
		t.Errorf("forged bearer = %d, want 401", code)
	}
	if msgs, _ := a.spool.Pull(shortCtx(t), 1); len(msgs) != 0 {
		t.Errorf("a rejected request reached the spool: %d messages", len(msgs))
	}
}

func TestEventsHandler_SpoolsBeforeAnswering200(t *testing.T) {
	a := newHTTPSAdapter(t)
	if code := postEvent(t, a, "good-token", dmEventJSON); code != http.StatusOK {
		t.Fatalf("signed event = %d, want 200", code)
	}
	msgs, err := a.spool.Pull(shortCtx(t), 1)
	if err != nil || len(msgs) != 1 || string(msgs[0].Data) != dmEventJSON {
		t.Fatalf("spooled = (%v, %v), want the event on the queue before the 200", msgs, err)
	}
	if err := a.spool.Ack(t.Context(), []string{msgs[0].AckID}); err != nil {
		t.Fatal(err)
	}
}

func TestEventsHandler_BadBodyAndSpoolDown(t *testing.T) {
	a := newHTTPSAdapter(t)
	if code := postEvent(t, a, "good-token", "{not json"); code != http.StatusBadRequest {
		t.Errorf("bad json = %d, want 400", code)
	}
	a.spool.down.Store(true)
	if code := postEvent(t, a, "good-token", dmEventJSON); code != http.StatusServiceUnavailable {
		t.Errorf("spool down = %d, want 503 so Chat redelivers", code)
	}
}

type verifierFunc func(ctx context.Context, bearer string) error

func (f verifierFunc) Verify(ctx context.Context, bearer string) error { return f(ctx, bearer) }

type countingMetrics struct {
	runtime.NoopMetrics
	mu     sync.Mutex
	counts map[string]int64
}

func (m *countingMetrics) Counter(name string, delta int64, labels ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counts == nil {
		m.counts = map[string]int64{}
	}
	m.counts[name+"|"+strings.Join(labels, ",")] += delta
}

func (m *countingMetrics) count(name string, labels ...string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[name+"|"+strings.Join(labels, ",")]
}

func TestEventsHandler_SignerCertOutageAnswers503(t *testing.T) {
	a := newHTTPSAdapter(t)
	logs := &logCapture{}
	a.logger = slog.New(logs)
	a.verifier = verifierFunc(func(context.Context, string) error {
		return fmt.Errorf("%w: fetch: status 500", errSignerCertsUnavailable)
	})
	if code := postEvent(t, a, "good-token", dmEventJSON); code != http.StatusServiceUnavailable {
		t.Errorf("cert outage = %d, want 503", code)
	}
	if logs.count(slog.LevelWarn, "google chat signer certs unavailable") != 1 {
		t.Error("a cert outage must be logged as one, once")
	}
	if logs.count(slog.LevelWarn, "google chat HTTPS request rejected: not signed by Google Chat") != 0 {
		t.Error("a cert outage was reported as a forged request")
	}
}

func TestEventsHandler_UndecodableEventIsLoggedAndCounted(t *testing.T) {
	a := newHTTPSAdapter(t)
	a.gatewayID = "gw-https"
	logs := &logCapture{}
	a.logger = slog.New(logs)
	metrics := &countingMetrics{}
	a.metrics = metrics
	for _, body := range []string{"{not json", `{"type":["MESSAGE"]}`} {
		if code := postEvent(t, a, "good-token", body); code != http.StatusBadRequest {
			t.Errorf("undecodable %q = %d, want 400", body, code)
		}
	}
	if n := logs.count(slog.LevelWarn, "google chat HTTPS event could not be decoded"); n != 1 {
		t.Errorf("undecodable warnings = %d, want 1 (throttled)", n)
	}
	if n := metrics.count(metricAckedWithoutTask, "gatewayId", "gw-https", "reason", "undecodable"); n != 2 {
		t.Errorf("undecodable count = %d, want 2", n)
	}
}

func TestEventsHandler_HealthFollowsTheSpool(t *testing.T) {
	a := newHTTPSAdapter(t)
	health := func() int {
		rec := httptest.NewRecorder()
		a.eventsHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
		return rec.Code
	}
	if code := health(); code != http.StatusOK {
		t.Errorf("health = %d, want 200", code)
	}
	a.spool.down.Store(true)
	if code := health(); code != http.StatusServiceUnavailable {
		t.Errorf("health with the spool down = %d, want 503", code)
	}
}

func TestParseConfig_HTTPSAudience(t *testing.T) {
	cfg, err := ParseConfig(map[string]any{"delivery": "https", "auth_audience_type": "project_number", "project_number": 123456789012})
	if err != nil || cfg.AudienceType != AudienceProjectNumber {
		t.Fatalf("project-number audience = (%+v, %v)", cfg, err)
	}
	if _, err := ParseConfig(map[string]any{"delivery": "https", "auth_audience_type": "project_number"}); err == nil {
		t.Error("the project-number audience without a project number must be refused")
	}
	if _, err := ParseConfig(map[string]any{"delivery": "https"}); err == nil {
		t.Error("HTTPS delivery without an endpoint URL must be refused")
	}
	if _, err := ParseConfig(map[string]any{"delivery": "https", "auth_audience_type": "endpoint_url", "endpoint_url": "http://insecure.example.com/events"}); err == nil {
		t.Error("a non-https endpoint_url must be refused")
	}
	if _, err := ParseConfig(map[string]any{"delivery": "https", "auth_audience_type": "project_number", "project_number": "1", "signer_certs_url": "https://evil.example.com"}); err == nil {
		t.Error("signer_certs_url off loopback must be refused")
	}
}

func TestMountIntentFollowsDelivery(t *testing.T) {
	if got := NewAdapter(map[string]any{"delivery": "https"}).MountIntent().Kind; got == 0 {
		t.Errorf("https mount kind = %v, want a named mount", got)
	}
	pubsub := NewAdapter(map[string]any{"delivery": "pubsub"}).MountIntent().Kind
	if pubsub == NewAdapter(map[string]any{"delivery": "https"}).MountIntent().Kind {
		t.Error("pubsub delivery must not mount a route")
	}
}

func shortCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	t.Cleanup(cancel)
	return ctx
}
