// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package httpsse

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	pastore "github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

type captureSink struct {
	mu     sync.Mutex
	events []productanalytics.Event
}

func (c *captureSink) Send(_ context.Context, evs []productanalytics.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, evs...)
	return nil
}

func (c *captureSink) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.events)
}

var testIngestIdentity = productanalytics.Identity{
	Marker:     "11111111-1111-4111-8111-111111111111",
	BindingKey: []byte("0123456789abcdef0123456789abcdef"),
}

func newTestIngestHandler(sink productanalytics.Sink) (*productAnalyticsHandler, *productanalytics.Service) {
	svc := productanalytics.NewService(productanalytics.Options{
		Sink:     sink,
		Consent:  productanalytics.ConsentFunc(func(context.Context) bool { return true }),
		Identity: productanalytics.IdentityFunc(func(context.Context) productanalytics.Identity { return testIngestIdentity }),
		Config:   productanalytics.ServiceConfig{FlushInterval: time.Hour, BatchSize: 1000},
		Logger:   slog.Default(),
	})
	return &productAnalyticsHandler{svc: svc, logger: slog.Default()}, svc
}

func postIngest(h *productAnalyticsHandler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/productAnalytics/ingest", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.handleIngest(rec, req)
	return rec
}

func TestIngestAcceptsAndDelivers(t *testing.T) {
	sink := &captureSink{}
	h, svc := newTestIngestHandler(sink)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	body := `{"events":[{"event":"page_view","properties":{"path":"/agents","interface":"ui","timestamp":"2026-08-06T00:00:00.000Z"}}]}`
	rec := postIngest(h, body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if sink.count() != 1 {
		t.Fatalf("sink received %d events, want 1", sink.count())
	}
	got := sink.events[0]
	if got.Name != "page_view" {
		t.Errorf("event name = %q, want page_view", got.Name)
	}
	if _, ok := got.Properties["appVersion"]; !ok {
		t.Error("delivered event missing backend segmentation (appVersion)")
	}
}

func TestIngestForcesUISurface(t *testing.T) {
	sink := &captureSink{}
	h, svc := newTestIngestHandler(sink)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	rec := postIngest(h, `{"events":[{"event":"page_view","properties":{"interface":"cli","path":"/agents"}}]}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if sink.count() != 1 {
		t.Fatalf("sink received %d events, want 1", sink.count())
	}
	if got := sink.events[0].Properties[productanalytics.PropInterface]; got != productanalytics.SurfaceUI {
		t.Errorf("interface = %v, want ui (client value must not be trusted)", got)
	}
}

func TestIngestDeliversMultipleEventsDroppingUnknown(t *testing.T) {
	sink := &captureSink{}
	h, svc := newTestIngestHandler(sink)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	body := `{"events":[{"event":"page_view","properties":{"path":"/agents"}},{"event":"builder:opened"},{"event":"made:up"}]}`
	if rec := postIngest(h, body); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if sink.count() != 2 {
		t.Fatalf("sink received %d events, want 2 (unknown event dropped)", sink.count())
	}
}

func TestIngestEmptyBatchDeliversNothing(t *testing.T) {
	sink := &captureSink{}
	h, svc := newTestIngestHandler(sink)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if rec := postIngest(h, `{"events":[]}`); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := svc.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if sink.count() != 0 {
		t.Fatalf("empty batch delivered %d events, want 0", sink.count())
	}
}

func TestIngestRejectsTooManyEvents(t *testing.T) {
	h, _ := newTestIngestHandler(&captureSink{})
	var b strings.Builder
	b.WriteString(`{"events":[`)
	for i := 0; i <= maxIngestEvents; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"event":"app:launched"}`)
	}
	b.WriteString(`]}`)
	if rec := postIngest(h, b.String()); rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an over-cap batch", rec.Code)
	}
}

func TestIngestRejectsUnknownField(t *testing.T) {
	h, _ := newTestIngestHandler(&captureSink{})
	rec := postIngest(h, `{"events":[],"sneaky":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown field", rec.Code)
	}
}

func TestIngestRejectsAnUnknownEnvelopeField(t *testing.T) {
	h, _ := newTestIngestHandler(&captureSink{})
	rec := postIngest(h, `{"events":[{"event":"page_view","timestamp":"2026-08-06T00:00:00.000Z"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: time moved onto occurredAt and decode is strict", rec.Code)
	}
}

func TestIngestForwardsTheClientEnvelope(t *testing.T) {
	sink := &captureSink{}
	h, svc := newTestIngestHandler(sink)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	const clientUUID = "123e4567-e89b-12d3-a456-426614174000"
	body := `{"events":[{"eventId":"` + clientUUID + `","sessionId":"` + clientUUID +
		`","occurredAt":"2026-08-06T00:00:00.000Z","event":"page_view","properties":{"path":"/agents"}}]}`
	for range 2 {
		if rec := postIngest(h, body); rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202", rec.Code)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if sink.count() != 2 {
		t.Fatalf("sink received %d events, want 2", sink.count())
	}
	got, again := sink.events[0], sink.events[1]
	for field, ids := range map[string][2]string{
		"eventId":   {got.EventID, again.EventID},
		"sessionId": {got.SessionID, again.SessionID},
	} {
		if ids[0] == clientUUID {
			t.Errorf("%s = %q, the client's own value must not reach a sink", field, ids[0])
		}
		if ids[0] != ids[1] {
			t.Errorf("%s = %q then %q, so one client value did not drive one delivered value", field, ids[0], ids[1])
		}
		if _, err := uuid.Parse(ids[0]); err != nil {
			t.Errorf("%s = %q, want a UUID: %v", field, ids[0], err)
		}
	}
	if got.EventID == got.SessionID {
		t.Errorf("eventId and sessionId both = %q, so one client id collapsed onto one value", got.EventID)
	}
	if got.OccurredAt.Year() != 2026 || got.OccurredAt.Month() != time.August {
		t.Errorf("occurredAt = %v, want the client clock forwarded", got.OccurredAt)
	}
	if got.ReceivedAt.IsZero() {
		t.Error("receivedAt is unset; the server clock must be stamped")
	}
	if got.Seq == 0 {
		t.Error("seq is unset; a consumer cannot detect a drop without it")
	}
}

func TestIngestEventMatchesTheWebUIEnvelope(t *testing.T) {
	feFields := webUIQueuedEventFields(t)
	goFields := jsonFieldNames(reflect.TypeOf(IngestEvent{}))

	for name := range feFields {
		if !goFields[name] {
			t.Errorf("the web UI sends %q but IngestEvent has no such json tag, so every batch would 400", name)
		}
	}
	for name := range goFields {
		if !feFields[name] {
			t.Errorf("IngestEvent declares %q but the web UI never sends it; drop it or start sending it", name)
		}
	}
}

func jsonFieldNames(t reflect.Type) map[string]bool {
	out := make(map[string]bool, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}

var reTSField = regexp.MustCompile(`(?m)^\s*(\w+)\s*[?]?:`)

func webUIQueuedEventFields(t *testing.T) map[string]bool {
	t.Helper()
	path := filepath.Join(repoRoot(t), "webui", "src", "common", "product-analytics", "client.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read web UI analytics client: %v", err)
	}
	const marker = "interface QueuedEvent {"
	body := string(data)
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("QueuedEvent not found in %s", path)
	}
	body = body[i+len(marker):]
	end := strings.Index(body, "}")
	if end < 0 {
		t.Fatalf("QueuedEvent block is unterminated in %s", path)
	}
	fields := make(map[string]bool)
	for _, m := range reTSField.FindAllStringSubmatch(body[:end], -1) {
		fields[m[1]] = true
	}
	if len(fields) == 0 {
		t.Fatalf("no fields parsed from QueuedEvent in %s", path)
	}
	return fields
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from the test working dir")
		}
		dir = parent
	}
}

func TestIngestRejectsMalformedJSON(t *testing.T) {
	h, _ := newTestIngestHandler(&captureSink{})
	rec := postIngest(h, `{"events": `)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for malformed body", rec.Code)
	}
}

func TestIngestRejectsOversizeBody(t *testing.T) {
	h, _ := newTestIngestHandler(&captureSink{})
	big := strings.Repeat("a", (1<<20)+1)
	body := `{"events":[{"event":"page_view","properties":{"path":"` + big + `"}}]}`
	rec := postIngest(h, body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 for oversize body", rec.Code)
	}
}

func TestIngestRouteAbsentWhenFlagOff(t *testing.T) {
	adapter := newProductAnalyticsAdapter(t, false)
	if adapter.analytics.svc != nil {
		t.Error("productAnalytics service should be nil when flag is off")
	}
	for _, r := range adapter.routes {
		if strings.Contains(r.Path, "productAnalytics") {
			t.Errorf("ingest route should not be wired when flag is off: %s", r.Path)
		}
	}
}

func newProductAnalyticsAdapter(t *testing.T, enabled bool) *HTTPSSEAdapter {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	reg := features.NewRegistry()

	adapter := NewHTTPSSEAdapter()
	appConfig := map[string]any{
		"namespace":             "test-ns",
		"session_secret_key":    "test-secret",
		"gateway_id":            "test-gw",
		"fastapi_host":          "127.0.0.1",
		"fastapi_port":          0,
		"default_user_identity": "sam_dev_user",
	}
	core := entrypoint.NewGatewayCore(adapter, appConfig)
	rtOpts := []runtime.Option{runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(reg)}
	if enabled {
		rtOpts = append(rtOpts, testProductAnalyticsOption(t, ""))
	}
	rt := runtime.NewRuntime(brk, nil, rtOpts...)
	core.SetServices(rt.Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Stop(context.Background()) })
	return adapter
}

func TestIngestRouteWiredWhenFlagOn(t *testing.T) {
	adapter := newProductAnalyticsAdapter(t, true)
	if adapter.analytics.svc == nil {
		t.Fatal("productAnalytics service should be constructed when flag is on")
	}
	found := false
	for _, r := range adapter.routes {
		if r.Path == "/api/v1/productAnalytics/ingest" {
			found = true
		}
	}
	if !found {
		t.Error("ingest route should be wired when flag is on")
	}
}

func newFlagOnAdapterWithDB(t *testing.T) *HTTPSSEAdapter {
	t.Helper()
	brk := broker.NewDevBroker()
	if err := brk.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })

	reg := features.NewRegistry()

	paTestDBURL := testDBURL(t, "product-analytics")
	adapter := NewHTTPSSEAdapter()
	appConfig := map[string]any{
		"namespace":             "test-ns",
		"session_secret_key":    "test-secret",
		"gateway_id":            "test-gw",
		"fastapi_host":          "127.0.0.1",
		"fastapi_port":          0,
		"default_user_identity": "sam_dev_user",
		"session_service": map[string]any{
			"type":         "sql",
			"database_url": paTestDBURL,
		},
	}
	core := entrypoint.NewGatewayCore(adapter, appConfig)
	rt := runtime.NewRuntime(brk, nil, runtime.WithLogger(slog.Default()), runtime.WithFeatureRegistry(reg), testProductAnalyticsOption(t, paTestDBURL))
	core.SetServices(rt.Services())
	if err := core.Init(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	db := core.Store()
	if db == nil {
		t.Fatal("expected DB to be initialized")
	}
	t.Cleanup(func() { _ = db.Close() })
	t.Cleanup(func() { _ = adapter.Stop(context.Background()) })
	return adapter
}

func testProductAnalyticsOption(t *testing.T, dbURL string) runtime.Option {
	t.Helper()
	opts := productanalytics.Options{
		Sink:   productanalytics.NewLoggingSink(slog.Default()),
		Logger: slog.Default(),
	}
	if dbURL != "" {
		db, err := pastore.Open(dbURL, slog.Default())
		if err != nil {
			t.Fatalf("open analytics store: %v", err)
		}
		if err := db.Migrate(context.Background()); err != nil {
			t.Fatalf("migrate analytics store: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		state := productanalytics.NewPersistedState(pastore.NewProductAnalyticsRepo(db), slog.Default())
		opts.Consent = state
		opts.Identity = state
	}
	return runtime.WithUsageReporter(productanalytics.NewService(opts))
}

func consentRoutePaths(adapter *HTTPSSEAdapter) map[string]bool {
	found := make(map[string]bool)
	for _, r := range adapter.routes {
		switch r.Method + " " + r.Path {
		case "GET " + pathProductAnalyticsConsent,
			"PUT " + pathProductAnalyticsConsent:
			found[r.Method+" "+r.Path] = true
		}
	}
	return found
}

func TestConsentSurfaceWiredWhenFlagOnAndDBPresent(t *testing.T) {
	adapter := newFlagOnAdapterWithDB(t)
	if adapter.analytics.consent == nil {
		t.Fatal("productAnalyticsConsent should be constructed when a DB is configured")
	}
	found := consentRoutePaths(adapter)
	for _, want := range []string{
		"GET " + pathProductAnalyticsConsent,
		"PUT " + pathProductAnalyticsConsent,
	} {
		if !found[want] {
			t.Errorf("route %s should be wired when the flag is on and a DB is configured", want)
		}
	}
}

func TestConsentSurfaceAbsentWhenNoDB(t *testing.T) {
	adapter := newProductAnalyticsAdapter(t, true)
	if adapter.analytics.consent != nil {
		t.Error("productAnalyticsConsent should be nil with no DB configured")
	}
	if len(consentRoutePaths(adapter)) != 0 {
		t.Errorf("consent routes should not be wired without a DB: %v", consentRoutePaths(adapter))
	}
	if adapter.analytics.svc == nil {
		t.Error("the ingest service should still be constructed so the endpoint 202s and drops")
	}
}

func TestConsentRoutesDispatchThroughMux(t *testing.T) {
	adapter := newFlagOnAdapterWithDB(t)

	getReq := httptest.NewRequest(http.MethodGet, pathProductAnalyticsConsent, nil)
	rec := httptest.NewRecorder()
	adapter.Mux().ServeHTTP(rec, getReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET consent: status = %d, want 200", rec.Code)
	}

	nonJSON := httptest.NewRequest(http.MethodPut, pathProductAnalyticsConsent, strings.NewReader(`{"granted":true}`))
	nonJSON.Header.Set("Content-Type", "text/plain")
	rec = httptest.NewRecorder()
	adapter.Mux().ServeHTTP(rec, nonJSON)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("PUT consent with non-JSON content type: status = %d, want 415", rec.Code)
	}

	jsonReq := httptest.NewRequest(http.MethodPut, pathProductAnalyticsConsent, strings.NewReader(`{"granted":true}`))
	jsonReq.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	adapter.Mux().ServeHTTP(rec, jsonReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT consent: status = %d, want 200", rec.Code)
	}
}

func TestIngestRouteDispatchThroughMux(t *testing.T) {
	adapter := newProductAnalyticsAdapter(t, true)
	const path = "/api/v1/productAnalytics/ingest"

	nonJSON := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"events":[]}`))
	nonJSON.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	adapter.Mux().ServeHTTP(rec, nonJSON)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("non-JSON content type: status = %d, want 415", rec.Code)
	}

	jsonReq := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"events":[]}`))
	jsonReq.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	adapter.Mux().ServeHTTP(rec, jsonReq)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("valid JSON: status = %d, want 202", rec.Code)
	}
}

func deliverOne(t *testing.T, body string) productanalytics.Event {
	t.Helper()
	sink := &captureSink{}
	h, svc := newTestIngestHandler(sink)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec := postIngest(h, body); rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if sink.count() != 1 {
		t.Fatalf("sink received %d events, want 1", sink.count())
	}
	return sink.events[0]
}

func ingestOneEvent(name string, properties map[string]any) string {
	body, err := json.Marshal(IngestRequest{Events: []IngestEvent{{Event: name, Properties: properties}}})
	if err != nil {
		panic(err)
	}
	return string(body)
}

func TestIngestRefusesOperatorAuthoredPaths(t *testing.T) {
	for _, path := range []string{
		"/workflows/refund-bot",
		"/agents/hr-policy-bot",
		"/connectors/acme-payroll",
		"/toolsets/mergers",
	} {
		t.Run(path, func(t *testing.T) {
			ev := deliverOne(t, ingestOneEvent("page_view", map[string]any{"path": path}))
			if got, kept := ev.Properties["path"]; kept {
				t.Errorf("path was delivered as %v, want the field dropped", got)
			}
			assertNoFragmentDelivered(t, ev, "refund-bot", "hr-policy-bot", "acme-payroll", "mergers")
		})
	}
}

func TestIngestRefusesABulkTextPath(t *testing.T) {
	var b strings.Builder
	for range 24 {
		b.WriteString("/")
		b.WriteString(strings.Repeat("z", 20))
	}
	ev := deliverOne(t, ingestOneEvent("page_view", map[string]any{"path": b.String()}))
	if got, kept := ev.Properties["path"]; kept {
		t.Errorf("a %d byte path was delivered as %v, want the field dropped", b.Len(), got)
	}
	assertNoFragmentDelivered(t, ev, strings.Repeat("z", 20))
}

func TestIngestRefusesAnOperatorAuthoredErrorPage(t *testing.T) {
	ev := deliverOne(t, ingestOneEvent("error:displayed", map[string]any{
		"errorCategory": "auth_failure",
		"page":          "/agents/hr-policy-bot",
	}))
	if got, kept := ev.Properties["page"]; kept {
		t.Errorf("page was delivered as %v, want the field dropped", got)
	}
	if ev.Properties[productanalytics.PropErrorCategory] != "auth_failure" {
		t.Errorf("errorCategory = %v, want the approved field kept", ev.Properties[productanalytics.PropErrorCategory])
	}
	assertNoFragmentDelivered(t, ev, "hr-policy-bot")
}

func TestIngestDeliversARealRoutePattern(t *testing.T) {
	ev := deliverOne(t, ingestOneEvent("page_view", map[string]any{"path": "/agents/:id"}))
	if ev.Properties["path"] != "/agents/:id" {
		t.Errorf("path = %v, want the router's own pattern delivered", ev.Properties["path"])
	}
}

func TestIngestRefusesFreeTextFormFields(t *testing.T) {
	const (
		apiKey     = "sk-ant-api03-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
		secretPath = "/home/jane/secrets/prod.env"
		customer   = "CUSTOMER=Acme Financial Holdings; DEAL=Project Falcon"
	)
	ev := deliverOne(t, ingestOneEvent("form:validation_error", map[string]any{
		"errorType": apiKey,
		"field":     secretPath,
		"form":      customer,
	}))
	for _, field := range []string{"errorType", "field", "form"} {
		if got, kept := ev.Properties[field]; kept {
			t.Errorf("%s was delivered as %v, want the field dropped", field, got)
		}
	}
	assertNoFragmentDelivered(t, ev, "sk-ant-api03", "jane", "prod.env", "Acme Financial Holdings", "Project Falcon")
}

func TestIngestDeliversTheRealFormVocabulary(t *testing.T) {
	ev := deliverOne(t, ingestOneEvent("form:validation_error", map[string]any{
		"form":      "connector",
		"field":     "name",
		"errorType": "too_small",
	}))
	for field, want := range map[string]string{"form": "connector", "field": "name", "errorType": "too_small"} {
		if ev.Properties[field] != want {
			t.Errorf("%s = %v, want %q", field, ev.Properties[field], want)
		}
	}
}

func assertNoFragmentDelivered(t *testing.T, ev productanalytics.Event, fragments ...string) {
	t.Helper()
	encoded, err := json.Marshal(ev.Properties)
	if err != nil {
		t.Fatalf("marshal delivered properties: %v", err)
	}
	for _, fragment := range fragments {
		if strings.Contains(string(encoded), fragment) {
			t.Errorf("the delivered event leaked %q: %s", fragment, encoded)
		}
	}
}
