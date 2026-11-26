// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package httpsse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	coreauth "github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

type memStateStore struct {
	mu            sync.Mutex
	installID     string
	bindingSecret string
	consent       string
	minted        int
	readErr       error
	writeErr      error
}

func (m *memStateStore) EnsureIdentity(context.Context) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.installID == "" {
		m.minted++
		m.installID = fmt.Sprintf("11111111-1111-1111-1111-%012d", m.minted)
	}
	return m.installID, m.bindingSecret, nil
}

func (m *memStateStore) ReplaceBindingSecret(_ context.Context, observed, replacement string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writeErr != nil {
		return "", m.writeErr
	}
	if m.consent == string(productanalytics.ConsentDenied) {
		return "", nil
	}
	if m.bindingSecret == observed {
		m.bindingSecret = replacement
	}
	return m.bindingSecret, nil
}

func (m *memStateStore) ConsentState(context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.readErr != nil {
		return "", m.readErr
	}
	return m.consent, nil
}

func (m *memStateStore) SetConsent(_ context.Context, consentState string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.writeErr != nil {
		return m.writeErr
	}
	m.consent = consentState
	if consentState == string(productanalytics.ConsentDenied) {
		m.installID = ""
		m.bindingSecret = ""
	}
	return nil
}

func (m *memStateStore) currentInstallID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.installID
}

func (m *memStateStore) currentConsent() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.consent
}

func newTestConsentHandler() (*productAnalyticsConsentHandler, *productanalytics.PersistedState, *memStateStore) {
	store := &memStateStore{}
	state := productanalytics.NewPersistedState(store, slog.Default())
	return &productAnalyticsConsentHandler{state: state, logger: slog.Default()}, state, store
}

func doConsent(h *productAnalyticsConsentHandler, method, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, pathProductAnalyticsConsent, nil)
	} else {
		req = httptest.NewRequest(method, pathProductAnalyticsConsent, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	switch method {
	case http.MethodGet:
		h.handleGet(rec, req)
	case http.MethodPut:
		h.handleSet(rec, req)
	}
	return rec
}

func TestConsentGetDefaultsToUnset(t *testing.T) {
	h, _, _ := newTestConsentHandler()
	rec := doConsent(h, http.MethodGet, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp ConsentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.State != string(productanalytics.ConsentUnset) {
		t.Errorf("state = %q, want unset", resp.State)
	}
}

func TestConsentGetReportsUnavailableOnReadFailure(t *testing.T) {
	h, _, store := newTestConsentHandler()
	store.readErr = errors.New("db down")

	rec := doConsent(h, http.MethodGet, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 for an unreadable consent store", rec.Code)
	}
	if strings.Contains(rec.Body.String(), string(productanalytics.ConsentUnset)) {
		t.Errorf("body %q reports a decision the store never gave", rec.Body.String())
	}
}

func TestConsentSetSucceedsAndAuditsUnknownWhenPriorReadFails(t *testing.T) {
	h, _, store := newTestConsentHandler()
	audit := &recordingConsentAudit{}
	h.audit = audit
	store.readErr = errors.New("db down")

	rec := doConsent(h, http.MethodPut, `{"granted":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a failed prior read must not block the write", rec.Code)
	}
	if got := store.currentConsent(); got != string(productanalytics.ConsentDenied) {
		t.Errorf("persisted = %q, want denied", got)
	}
	events := audit.snapshot()
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	if events[0].From != consentStateUnknown {
		t.Errorf("audited From = %q, want %q — a failed read must not name a decision", events[0].From, consentStateUnknown)
	}
}

func TestConsentSetFailedWriteIsNotReportedOrAudited(t *testing.T) {
	h, _, store := newTestConsentHandler()
	audit := &recordingConsentAudit{}
	h.audit = audit
	store.writeErr = errors.New("db down")

	rec := doConsent(h, http.MethodPut, `{"granted":false}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when the decision could not be persisted", rec.Code)
	}
	if strings.Contains(rec.Body.String(), string(productanalytics.ConsentDenied)) {
		t.Errorf("body %q reports a decision that was never persisted", rec.Body.String())
	}
	if got := store.currentConsent(); got != "" {
		t.Errorf("persisted = %q, want nothing written", got)
	}
	if events := audit.snapshot(); len(events) != 0 {
		t.Errorf("audit events = %d, want 0 — nothing changed", len(events))
	}
}

func TestConsentSetGrantAndDeny(t *testing.T) {
	h, state, store := newTestConsentHandler()

	rec := doConsent(h, http.MethodPut, `{"granted":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !state.Enabled(context.Background()) {
		t.Error("granting consent must enable collection")
	}
	if got := store.currentConsent(); got != string(productanalytics.ConsentGranted) {
		t.Errorf("persisted = %q, want granted", got)
	}

	rec = doConsent(h, http.MethodPut, `{"granted":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if state.Enabled(context.Background()) {
		t.Error("denying consent must disable collection")
	}
	if got := store.currentConsent(); got != string(productanalytics.ConsentDenied) {
		t.Errorf("persisted = %q, want denied", got)
	}
}

func TestConsentSetRequiresGrantedField(t *testing.T) {
	h, _, store := newTestConsentHandler()
	rec := doConsent(h, http.MethodPut, `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 when granted is omitted", rec.Code)
	}
	var errResp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if errResp.ErrorID == "" {
		t.Error("error response missing errorId")
	}
	if len(errResp.ValidationDetails) == 0 || len(errResp.ValidationDetails["granted"]) == 0 {
		t.Error("error response missing validationDetails for granted field")
	}
	if store.currentConsent() != "" {
		t.Error("a rejected request must not persist a decision")
	}
}

func TestConsentSetRejectsUnknownField(t *testing.T) {
	h, _, _ := newTestConsentHandler()
	rec := doConsent(h, http.MethodPut, `{"granted":true,"sneaky":1}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown field", rec.Code)
	}
	var errResp ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if errResp.ErrorID == "" {
		t.Error("error response missing errorId")
	}
}

func TestConsentResponseNeverLeaksInstallID(t *testing.T) {
	h, state, _ := newTestConsentHandler()

	original := state.Identity(context.Background()).Marker
	if original == "" {
		t.Fatal("precondition: a collecting install should hold a marker")
	}

	grantRec := doConsent(h, http.MethodPut, `{"granted":true}`)
	denyRec := doConsent(h, http.MethodPut, `{"granted":false}`)
	regrantRec := doConsent(h, http.MethodPut, `{"granted":true}`)
	rotated := state.Identity(context.Background()).Marker
	if rotated == "" || rotated == original {
		t.Fatalf("precondition: opting back in should have issued a fresh marker, got %q", rotated)
	}

	responses := map[string]*httptest.ResponseRecorder{
		"GET":            doConsent(h, http.MethodGet, ""),
		"PUT granted":    grantRec,
		"PUT denied":     denyRec,
		"PUT re-granted": regrantRec,
	}
	for name, rec := range responses {
		body := rec.Body.String()
		for _, marker := range []string{original, rotated} {
			if strings.Contains(body, marker) {
				t.Errorf("%s leaked the install marker: %s", name, body)
			}
		}

		var fields map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 1 {
			t.Errorf("%s response carries %d fields, want only state: %v", name, len(fields), fields)
		}
		if _, ok := fields["state"]; !ok {
			t.Errorf("%s response is missing state: %v", name, fields)
		}
	}
}

func TestConsentMarkerFollowsTheDecision(t *testing.T) {
	h, state, store := newTestConsentHandler()

	first := state.Identity(context.Background()).Marker
	if first == "" {
		t.Fatal("a collecting install must hold a marker")
	}
	doConsent(h, http.MethodPut, `{"granted":true}`)
	if got := store.currentInstallID(); got != first {
		t.Errorf("persisted marker after leaving collection on = %q, want the held %q", got, first)
	}
	if got := state.Identity(context.Background()).Marker; got != first {
		t.Errorf("in-memory marker = %q, want the persisted %q", got, first)
	}

	doConsent(h, http.MethodPut, `{"granted":false}`)
	if got := store.currentInstallID(); got != "" {
		t.Errorf("persisted marker after opting out = %q, want empty", got)
	}
	if got := state.Identity(context.Background()).Marker; got != "" {
		t.Errorf("in-memory marker after opting out = %q, want empty", got)
	}
	if state.Enabled(context.Background()) {
		t.Error("collection must stop after opting out")
	}

	doConsent(h, http.MethodPut, `{"granted":true}`)
	second := state.Identity(context.Background()).Marker
	if second == "" || second == first {
		t.Errorf("marker after opting back in = %q, want a fresh one (was %q)", second, first)
	}
	if got := store.currentInstallID(); got != second {
		t.Errorf("persisted marker = %q, want the minted %q", got, second)
	}
}

func TestConsentUnsetDeliversIngest(t *testing.T) {
	_, state, _ := newTestConsentHandler()
	sink := &captureSink{}
	svc := productanalytics.NewService(productanalytics.Options{
		Sink:     sink,
		Consent:  state,
		Identity: state,
		Config:   productanalytics.ServiceConfig{FlushInterval: time.Hour, BatchSize: 1000},
		Logger:   slog.Default(),
	})
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ingestH := &productAnalyticsHandler{svc: svc, logger: slog.Default()}
	body := `{"events":[{"event":"page_view","properties":{"path":"/agents"}}]}`

	if rec := postIngest(ingestH, body); rec.Code != http.StatusAccepted {
		t.Fatalf("ingest status = %d, want 202", rec.Code)
	}
	collectedUnder := state.Identity(context.Background()).Marker

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	if sink.count() != 1 {
		t.Fatalf("sink received %d events, want 1 for an install with no decision recorded", sink.count())
	}
	if sink.events[0].DistinctID != collectedUnder || collectedUnder == "" {
		t.Errorf("delivered DistinctID = %q, want the marker the install held %q", sink.events[0].DistinctID, collectedUnder)
	}
}

func TestConsentDenialDiscardsBufferedIngest(t *testing.T) {
	consentH, state, store := newTestConsentHandler()
	sink := &captureSink{}
	svc := productanalytics.NewService(productanalytics.Options{
		Sink:     sink,
		Consent:  state,
		Identity: state,
		Config:   productanalytics.ServiceConfig{FlushInterval: time.Hour, BatchSize: 1000},
		Logger:   slog.Default(),
	})
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ingestH := &productAnalyticsHandler{svc: svc, logger: slog.Default()}
	body := `{"events":[{"event":"page_view","properties":{"path":"/agents"}}]}`

	if rec := postIngest(ingestH, body); rec.Code != http.StatusAccepted {
		t.Fatalf("ingest status = %d, want 202", rec.Code)
	}
	if rec := doConsent(consentH, http.MethodPut, `{"granted":false}`); rec.Code != http.StatusOK {
		t.Fatalf("consent status = %d, want 200", rec.Code)
	}
	if rec := postIngest(ingestH, body); rec.Code != http.StatusAccepted {
		t.Fatalf("ingest status = %d, want 202", rec.Code)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	if sink.count() != 0 {
		t.Errorf("sink received %d events, want 0 — the denial discards the buffer too", sink.count())
	}
	if got := store.currentInstallID(); got != "" {
		t.Errorf("persisted marker after the denial = %q, want empty", got)
	}
}

type recordingConsentAudit struct {
	mu     sync.Mutex
	events []coreauth.ConsentChangeEvent
}

func (r *recordingConsentAudit) LogActivationChange(_ context.Context, _ coreauth.ActivationChangeEvent) {
}
func (r *recordingConsentAudit) LogConsentChange(_ context.Context, e coreauth.ConsentChangeEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recordingConsentAudit) snapshot() []coreauth.ConsentChangeEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]coreauth.ConsentChangeEvent, len(r.events))
	copy(out, r.events)
	return out
}

func newAuditingConsentHandler() (*productAnalyticsConsentHandler, *recordingConsentAudit) {
	audit := &recordingConsentAudit{}
	state := productanalytics.NewPersistedState(&memStateStore{}, slog.Default())
	return &productAnalyticsConsentHandler{state: state, audit: audit, logger: slog.Default()}, audit
}

func doConsentAs(h *productAnalyticsConsentHandler, userID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, pathProductAnalyticsConsent, strings.NewReader(body))
	if userID != "" {
		req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{
			UserID:        userID,
			Authenticated: true,
		})))
	}
	rec := httptest.NewRecorder()
	h.handleSet(rec, req)
	return rec
}

func TestConsentChangeIsAudited(t *testing.T) {
	h, audit := newAuditingConsentHandler()

	if rec := doConsentAs(h, "alice", `{"granted":true}`); rec.Code != http.StatusOK {
		t.Fatalf("grant status = %d, want 200", rec.Code)
	}
	if rec := doConsentAs(h, "bob", `{"granted":false}`); rec.Code != http.StatusOK {
		t.Fatalf("deny status = %d, want 200", rec.Code)
	}

	events := audit.snapshot()
	if len(events) != 2 {
		t.Fatalf("recorded %d audit events, want 2", len(events))
	}

	grant := events[0]
	if grant.Actor != "alice" {
		t.Errorf("grant actor = %q, want alice", grant.Actor)
	}
	if grant.From != string(productanalytics.ConsentUnset) {
		t.Errorf("grant from = %q, want unset", grant.From)
	}
	if grant.To != string(productanalytics.ConsentGranted) {
		t.Errorf("grant to = %q, want granted", grant.To)
	}
	if grant.Scope != consentScopeInstall {
		t.Errorf("grant scope = %q, want %q", grant.Scope, consentScopeInstall)
	}

	deny := events[1]
	if deny.Actor != "bob" {
		t.Errorf("deny actor = %q, want bob", deny.Actor)
	}
	if deny.From != string(productanalytics.ConsentGranted) {
		t.Errorf("deny from = %q, want granted — the prior decision must be read before the write", deny.From)
	}
	if deny.To != string(productanalytics.ConsentDenied) {
		t.Errorf("deny to = %q, want denied", deny.To)
	}
}

func TestConsentRejectedRequestIsNotAudited(t *testing.T) {
	for name, body := range map[string]string{
		"missing field": `{}`,
		"unknown field": `{"granted":true,"extra":1}`,
		"malformed":     `{`,
	} {
		t.Run(name, func(t *testing.T) {
			h, audit := newAuditingConsentHandler()
			rec := doConsentAs(h, "alice", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if got := audit.snapshot(); len(got) != 0 {
				t.Errorf("recorded %d audit events for a rejected request, want 0", len(got))
			}
		})
	}
}

func TestConsentAuditNamesAnonymousActorWithoutIdentity(t *testing.T) {
	h, audit := newAuditingConsentHandler()
	if rec := doConsentAs(h, "", `{"granted":true}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	events := audit.snapshot()
	if len(events) != 1 {
		t.Fatalf("recorded %d audit events, want 1", len(events))
	}
	if events[0].Actor != "anonymous" {
		t.Errorf("actor = %q, want anonymous", events[0].Actor)
	}
}

func TestConsentAuditNeverCarriesInstallID(t *testing.T) {
	h, audit := newAuditingConsentHandler()
	if rec := doConsentAs(h, "alice", `{"granted":true}`); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	marker := h.state.(*productanalytics.PersistedState).Identity(context.Background()).Marker
	if marker == "" {
		t.Fatal("precondition: a grant should have minted a marker")
	}
	for _, e := range audit.snapshot() {
		if e.Actor == marker || e.From == marker || e.To == marker || e.Scope == marker {
			t.Errorf("audit event carries the install marker %q: %+v", marker, e)
		}
	}
}

func TestConsentWriteGateRejectsCallerWithoutScope(t *testing.T) {
	h, audit := newAuditingConsentHandler()
	gated := scoped(rbac.ScopeProductAnalyticsConsentUpdate, h.handleSet)

	cases := map[string]struct {
		scopes   []string
		wantCode int
	}{
		"no scopes":      {scopes: []string{}, wantCode: http.StatusForbidden},
		"unrelated only": {scopes: []string{"analytics:_:read"}, wantCode: http.StatusForbidden},
		"exact scope":    {scopes: []string{rbac.ScopeProductAnalyticsConsentUpdate}, wantCode: http.StatusOK},
		"wildcard":       {scopes: []string{"*"}, wantCode: http.StatusOK},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, pathProductAnalyticsConsent, strings.NewReader(`{"granted":true}`))
			req = req.WithContext(auth.ContextWithSubject(req.Context(), auth.NewTestSubject(auth.SubjectFields{
				UserID: "alice", Authenticated: true, Scopes: tc.scopes,
			})))
			rec := httptest.NewRecorder()
			gated(rec, req)
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}

	if got := len(audit.snapshot()); got != 2 {
		t.Errorf("recorded %d audit events, want 2 — a refused write must not be audited", got)
	}
}

func TestConsentWriteGateAllowsNoIdentity(t *testing.T) {
	h, _ := newAuditingConsentHandler()
	gated := scoped(rbac.ScopeProductAnalyticsConsentUpdate, h.handleSet)

	req := httptest.NewRequest(http.MethodPut, pathProductAnalyticsConsent, strings.NewReader(`{"granted":true}`))
	rec := httptest.NewRecorder()
	gated(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 — the no-auth desktop path must still be able to answer", rec.Code)
	}
}
