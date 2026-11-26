// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package productanalytics

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

const testVendorToken = "test-vendor-token"

type capturedRequest struct {
	path     string
	query    url.Values
	username string
	password string
	hasAuth  bool
	encoding string
	body     []byte
}

type vendorRecorder struct {
	mu       sync.Mutex
	requests []capturedRequest

	status   int
	response string
}

func (r *vendorRecorder) handle(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	username, password, hasAuth := req.BasicAuth()

	r.mu.Lock()
	r.requests = append(r.requests, capturedRequest{
		path:     req.URL.Path,
		query:    req.URL.Query(),
		username: username,
		password: password,
		hasAuth:  hasAuth,
		encoding: req.Header.Get("Content-Encoding"),
		body:     body,
	})
	r.mu.Unlock()

	w.WriteHeader(r.status)
	_, _ = io.WriteString(w, r.response)
}

func (r *vendorRecorder) captured() []capturedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]capturedRequest(nil), r.requests...)
}

func newVendorRecorder(t *testing.T, status int, response string) (*vendorRecorder, *vendorSink) {
	t.Helper()
	recorder := &vendorRecorder{status: status, response: response}
	server := httptest.NewServer(http.HandlerFunc(recorder.handle))
	t.Cleanup(server.Close)
	return recorder, newVendorSink(testVendorToken, server.URL, slog.New(slog.DiscardHandler))
}

func (r *vendorRecorder) only(t *testing.T) capturedRequest {
	t.Helper()
	got := r.captured()
	if len(got) != 1 {
		t.Fatalf("captured %d requests, want exactly 1", len(got))
	}
	return got[0]
}

func decodeEvents(t *testing.T, req capturedRequest) []map[string]any {
	t.Helper()
	payload := req.body
	if req.encoding == "gzip" {
		reader, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			t.Fatalf("open the gzip body: %v", err)
		}
		defer func() { _ = reader.Close() }()
		if payload, err = io.ReadAll(reader); err != nil {
			t.Fatalf("read the gzip body: %v", err)
		}
	}
	var events []map[string]any
	if err := json.Unmarshal(payload, &events); err != nil {
		t.Fatalf("decode the body as an event array: %v", err)
	}
	return events
}

func properties(t *testing.T, event map[string]any) map[string]any {
	t.Helper()
	props, ok := event["properties"].(map[string]any)
	if !ok {
		t.Fatalf("event carries no properties object: %#v", event)
	}
	return props
}

const importAccepted = `{"code":200,"num_records_imported":1,"status":"OK"}`

func testEvent() Event {
	return Event{
		EventID:       "6f1b6a1e-2c4d-7e8f-9a0b-1c2d3e4f5a6b",
		DistinctID:    "11111111-2222-7333-8444-555555555555",
		SessionID:     "99999999-8888-7777-6666-555555555555",
		PublisherID:   "publisher-1",
		Seq:           7,
		Name:          EventAppLaunched,
		OccurredAt:    time.Date(2026, 8, 20, 10, 30, 0, 0, time.UTC),
		ReceivedAt:    time.Date(2026, 8, 20, 10, 30, 1, 500_000_000, time.UTC),
		SchemaVersion: EventSchemaVersion,
		Properties:    map[string]any{PropInterface: SurfaceUI, propOS: "linux"},
	}
}

func TestVendorSendMapsTheEnvelopeOntoTheImportRequest(t *testing.T) {
	recorder, sink := newVendorRecorder(t, http.StatusOK, importAccepted)

	if err := sink.Send(context.Background(), []Event{testEvent()}); err != nil {
		t.Fatalf("Send returned %v, want nil", err)
	}

	req := recorder.only(t)
	if req.path != "/import" {
		t.Errorf("path = %q, want /import", req.path)
	}
	if !req.hasAuth || req.username != testVendorToken || req.password != "" {
		t.Errorf("basic auth = (%q, %q, present=%v), want the token as username with an empty password",
			req.username, req.password, req.hasAuth)
	}
	if got := req.query.Get("strict"); got != "1" {
		t.Errorf("strict = %q, want 1: without it the vendor accepts records it cannot describe", got)
	}
	if req.query.Has("project_id") {
		t.Error("project_id was sent, but token auth does not carry one")
	}
	if req.encoding != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", req.encoding)
	}

	events := decodeEvents(t, req)
	if len(events) != 1 {
		t.Fatalf("imported %d events, want 1", len(events))
	}
	if got := events[0]["event"]; got != EventAppLaunched {
		t.Errorf("event = %v, want %v", got, EventAppLaunched)
	}

	want := map[string]any{
		"distinct_id":           "11111111-2222-7333-8444-555555555555",
		"$insert_id":            "6f1b6a1e-2c4d-7e8f-9a0b-1c2d3e4f5a6b",
		"time":                  float64(time.Date(2026, 8, 20, 10, 30, 0, 0, time.UTC).UnixMilli()),
		vendorPropIP:            vendorGeoOptOut,
		vendorPropSessionID:     "99999999-8888-7777-6666-555555555555",
		vendorPropPublisherID:   "publisher-1",
		vendorPropSeq:           float64(7),
		vendorPropReceivedAt:    "2026-08-20T10:30:01.500Z",
		vendorPropSchemaVersion: float64(EventSchemaVersion),
		PropInterface:           SurfaceUI,
		propOS:                  "linux",
	}
	props := properties(t, events[0])
	for key, expected := range want {
		if got := props[key]; got != expected {
			t.Errorf("property %q = %#v, want %#v", key, got, expected)
		}
	}
}

func TestVendorSendOptsOutOfGeolocationOnEveryEvent(t *testing.T) {
	recorder, sink := newVendorRecorder(t, http.StatusOK, importAccepted)

	first, second := testEvent(), testEvent()
	second.EventID = "7a2c7b2f-3d5e-7f90-8b1c-2d3e4f5a6b7c"
	if err := sink.Send(context.Background(), []Event{first, second}); err != nil {
		t.Fatalf("Send returned %v, want nil", err)
	}

	for i, event := range decodeEvents(t, recorder.only(t)) {
		if got := properties(t, event)[vendorPropIP]; got != vendorGeoOptOut {
			t.Errorf("event %d property %q = %#v, want %q", i, vendorPropIP, got, vendorGeoOptOut)
		}
	}
}

func TestVendorSendLeavesTheCallersPropertiesUntouched(t *testing.T) {
	_, sink := newVendorRecorder(t, http.StatusOK, importAccepted)

	event := testEvent()
	before := make(map[string]any, len(event.Properties))
	for key, value := range event.Properties {
		before[key] = value
	}

	if err := sink.Send(context.Background(), []Event{event}); err != nil {
		t.Fatalf("Send returned %v, want nil", err)
	}

	if len(event.Properties) != len(before) {
		t.Fatalf("properties grew from %d to %d keys: %#v", len(before), len(event.Properties), event.Properties)
	}
	for key, want := range before {
		if got := event.Properties[key]; got != want {
			t.Errorf("property %q = %#v, want %#v", key, got, want)
		}
	}
	for _, leaked := range []string{"token", "mp_lib", "$lib_version", vendorPropIP} {
		if value, ok := event.Properties[leaked]; ok {
			t.Errorf("the caller's map gained %q = %#v", leaked, value)
		}
	}
}

func TestVendorSendDropsOnlyTheEventsWithNoInstallMarker(t *testing.T) {
	recorder, sink := newVendorRecorder(t, http.StatusOK, importAccepted)

	marked, unmarked := testEvent(), testEvent()
	unmarked.DistinctID = ""
	err := sink.Send(context.Background(), []Event{unmarked, marked})
	if got := PartialDropCount(err); got != 1 {
		t.Fatalf("PartialDropCount(err) = %d, want 1: the marked event was delivered, the other excluded", got)
	}
	if IsRetryable(err) {
		t.Errorf("error %v is retryable, but the excluded event cannot be resolved by resending", err)
	}

	events := decodeEvents(t, recorder.only(t))
	if len(events) != 1 {
		t.Fatalf("imported %d events, want only the marked one", len(events))
	}
	if got := properties(t, events[0])["distinct_id"]; got != marked.DistinctID {
		t.Errorf("distinct_id = %#v, want %q", got, marked.DistinctID)
	}
}

func TestVendorSendReportsABatchWithNoInstallMarkerAsLost(t *testing.T) {
	recorder, sink := newVendorRecorder(t, http.StatusOK, importAccepted)

	unmarked := testEvent()
	unmarked.DistinctID = ""
	err := sink.Send(context.Background(), []Event{unmarked})
	if err == nil {
		t.Fatal("Send returned nil, but the events were dropped rather than delivered")
	}
	if IsRetryable(err) {
		t.Errorf("error %v is retryable, but a missing marker cannot be resolved by resending", err)
	}
	if got := len(recorder.captured()); got != 0 {
		t.Errorf("made %d requests, want none: there was nothing the vendor would accept", got)
	}
}

func TestVendorSendMakesNoRequestForAnEmptyBatch(t *testing.T) {
	recorder, sink := newVendorRecorder(t, http.StatusOK, importAccepted)

	if err := sink.Send(context.Background(), nil); err != nil {
		t.Fatalf("Send returned %v, want nil", err)
	}
	if got := len(recorder.captured()); got != 0 {
		t.Errorf("made %d requests, want none", got)
	}
}

func TestVendorSendTreatsRateLimitingAsRetryable(t *testing.T) {
	_, sink := newVendorRecorder(t, http.StatusTooManyRequests,
		`{"code":429,"error":"too many requests","status":"error"}`)

	err := sink.Send(context.Background(), []Event{testEvent()})
	if err == nil {
		t.Fatal("Send returned nil for a rate-limited batch")
	}
	if !IsRetryable(err) {
		t.Errorf("error %v is terminal, but a rate limit clears on its own", err)
	}
}

func TestVendorSendTreatsAValidationFailureAsTerminal(t *testing.T) {
	_, sink := newVendorRecorder(t, http.StatusBadRequest,
		`{"code":400,"error":"some data points failed validation","status":"error",`+
			`"num_records_imported":0,`+
			`"failed_records":[{"index":0,"insert_id":"abc","field":"properties.time","message":"out of range"}]}`)

	err := sink.Send(context.Background(), []Event{testEvent()})
	if err == nil {
		t.Fatal("Send returned nil for a rejected batch")
	}
	if IsRetryable(err) {
		t.Errorf("error %v is retryable, but the vendor rejects the same record identically every time", err)
	}
}

func TestVendorSendTreatsAPartialValidationFailureAsPartialDrop(t *testing.T) {
	_, sink := newVendorRecorder(t, http.StatusBadRequest,
		`{"code":400,"error":"some data points failed validation","status":"error",`+
			`"num_records_imported":1,`+
			`"failed_records":[{"index":1,"insert_id":"abc","field":"properties.time","message":"out of range"}]}`)

	err := sink.Send(context.Background(), []Event{testEvent(), testEvent()})
	if err == nil {
		t.Fatal("Send returned nil for a partially rejected batch")
	}
	if IsRetryable(err) {
		t.Errorf("error %v is retryable, but the vendor rejects the same record identically every time", err)
	}
	if got := PartialDropCount(err); got != 1 {
		t.Errorf("PartialDropCount(err) = %d, want 1 — one of two records imported means one was lost, not both", got)
	}
}

func TestVendorSendTreatsBadCredentialsAsTerminal(t *testing.T) {
	_, sink := newVendorRecorder(t, http.StatusUnauthorized,
		`{"code":401,"error":"invalid credentials","status":"error"}`)

	err := sink.Send(context.Background(), []Event{testEvent()})
	if err == nil {
		t.Fatal("Send returned nil for a refused batch")
	}
	if IsRetryable(err) {
		t.Errorf("error %v is retryable, but a bad token needs a new build, not a resend", err)
	}
}

func TestVendorSendTreatsAServerErrorAsRetryable(t *testing.T) {
	_, sink := newVendorRecorder(t, http.StatusServiceUnavailable, `service unavailable`)

	err := sink.Send(context.Background(), []Event{testEvent()})
	if err == nil {
		t.Fatal("Send returned nil for a failed batch")
	}
	if !IsRetryable(err) {
		t.Errorf("error %v is terminal, but the vendor being briefly down is the retry's whole purpose", err)
	}
}

func TestVendorMaxBatchSizeMatchesTheVendorImportLimit(t *testing.T) {
	_, sink := newVendorRecorder(t, http.StatusOK, importAccepted)

	limiter, ok := any(sink).(BatchLimiter)
	if !ok {
		t.Fatal("the vendor sink is not a BatchLimiter, so an oversized batch would be refused whole")
	}
	if got := limiter.MaxBatchSize(); got != 2000 {
		t.Errorf("MaxBatchSize = %d, want 2000", got)
	}
}

func TestVendorSinkIsNotAFlusher(t *testing.T) {
	_, sink := newVendorRecorder(t, http.StatusOK, importAccepted)

	if _, ok := any(sink).(Flusher); ok {
		t.Error("the vendor sink claims Flusher, but nothing it does is buffered")
	}
}

func TestVendorIdentifySetsTheProfileWithGeolocationSuppressed(t *testing.T) {
	recorder, sink := newVendorRecorder(t, http.StatusOK, `1`)

	traits := installTraits()
	if err := sink.Identify(context.Background(), "11111111-2222-7333-8444-555555555555", traits); err != nil {
		t.Fatalf("Identify returned %v, want nil", err)
	}

	req := recorder.only(t)
	if req.path != "/engage" {
		t.Errorf("path = %q, want /engage", req.path)
	}

	var profiles []map[string]any
	if err := json.Unmarshal(req.body, &profiles); err != nil {
		t.Fatalf("decode the profile body: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("set %d profiles, want 1", len(profiles))
	}
	profile := profiles[0]
	if got := profile["$token"]; got != testVendorToken {
		t.Errorf("$token = %#v, want the project token", got)
	}
	if got := profile["$distinct_id"]; got != "11111111-2222-7333-8444-555555555555" {
		t.Errorf("$distinct_id = %#v, want the install marker", got)
	}
	if got := profile["$ip"]; got != vendorGeoOptOut {
		t.Errorf("$ip = %#v, want %q so the vendor does not locate the install", got, vendorGeoOptOut)
	}
	set, ok := profile["$set"].(map[string]any)
	if !ok {
		t.Fatalf("profile carries no $set object: %#v", profile)
	}
	for key, want := range traits {
		if got := set[key]; got != want {
			t.Errorf("trait %q = %#v, want %#v", key, got, want)
		}
	}
}

func TestVendorIdentifyLeavesTheCallersTraitsUntouched(t *testing.T) {
	_, sink := newVendorRecorder(t, http.StatusOK, `1`)

	traits := installTraits()
	want := len(traits)
	if err := sink.Identify(context.Background(), "11111111-2222-7333-8444-555555555555", traits); err != nil {
		t.Fatalf("Identify returned %v, want nil", err)
	}
	if got := len(traits); got != want {
		t.Errorf("traits grew from %d to %d keys: %#v", want, got, traits)
	}
}

func TestNewVendorSinkDeclinesWithoutAToken(t *testing.T) {
	sink, ok := NewVendorSink(slog.New(slog.DiscardHandler))
	if ok {
		t.Errorf("NewVendorSink reported a usable sink %#v without a linked token", sink)
	}
	if sink != nil {
		t.Errorf("NewVendorSink returned %#v, want nil", sink)
	}
}
