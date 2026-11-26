// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const (
	rawSessionID = "6f9619ff-8b86-d011-b42d-00cf4fc964ff"
	rawEventID   = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"
)

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func grantedState(t *testing.T, store StateStore, logger *slog.Logger) *PersistedState {
	t.Helper()
	state := NewPersistedState(store, logger)
	if err := state.SetConsent(context.Background(), ConsentGranted); err != nil {
		t.Fatalf("grant consent: %v", err)
	}
	return state
}

func newStoreBackedService(t *testing.T, state *PersistedState) *Service {
	t.Helper()
	return NewService(Options{
		Consent:  state,
		Identity: state,
		Logger:   discardLogger(),
		Config:   ServiceConfig{FlushInterval: time.Hour, BufferSize: 100, BatchSize: 1000, RateCapPerMin: 100000},
	})
}

func tracedUIEvent() RawEvent {
	return RawEvent{
		Name:      EventChatFirstResponse,
		EventID:   rawEventID,
		SessionID: rawSessionID,
		Properties: map[string]any{
			propPath:    "/agents",
			PropTraceID: rawTraceID,
		},
	}
}

func shapeAsUUID(sum []byte) string {
	var id uuid.UUID
	copy(id[:], sum)
	id[6] = id[6]&0x0f | 0x80
	id[8] = id[8]&0x3f | 0x80
	return id.String()
}

func candidateValues(keys []string, domain, marker, raw string) []string {
	messages := [][]byte{
		[]byte(raw),
		[]byte(domain + "\x00" + raw),
		[]byte(domain + "\x00" + marker + "\x00" + raw),
		[]byte(marker + "\x00" + raw),
		[]byte(raw + "\x00" + marker),
	}
	var out []string
	for _, key := range keys {
		for _, msg := range messages {
			mac := hmac.New(sha256.New, []byte(key))
			mac.Write(msg)
			out = append(out, shapeAsUUID(mac.Sum(nil)))
		}
	}
	for _, msg := range messages {
		sum := sha256.Sum256(msg)
		out = append(out, shapeAsUUID(sum[:]))
	}
	return out
}

func TestAVendorHoldingTheExportCannotConfirmARawIdentifier(t *testing.T) {
	state := grantedState(t, &fakeStateStore{}, discardLogger())
	s := newStoreBackedService(t, state)
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{tracedUIEvent()})

	events := buffered(s)
	if len(events) != 1 {
		t.Fatalf("buffered %d events, want 1", len(events))
	}
	ev := events[0]
	boundTrace, ok := ev.Properties[PropTraceID].(string)
	if !ok {
		t.Fatalf("event carries no trace id: %#v", ev.Properties)
	}

	keys := []string{
		"",
		ev.DistinctID,
		ev.EventID,
		ev.SessionID,
		ev.PublisherID,
		ev.Name,
		boundTrace,
		rawTraceID,
		rawEventID,
		rawSessionID,
	}

	for _, c := range []struct {
		field   string
		domain  string
		raw     string
		emitted string
	}{
		{"traceId", idDomainTrace, rawTraceID, boundTrace},
		{"sessionId", idDomainSession, rawSessionID, ev.SessionID},
		{"eventId", idDomainEvent, rawEventID, ev.EventID},
	} {
		t.Run(c.field, func(t *testing.T) {
			if c.emitted == "" || c.emitted == c.raw {
				t.Fatalf("precondition: %s was emitted as %q, so there is nothing to confirm", c.field, c.emitted)
			}
			for _, candidate := range candidateValues(keys, c.domain, ev.DistinctID, c.raw) {
				if candidate == c.emitted {
					t.Errorf("the emitted %s %q is reproducible from the export and the raw value, so the install joins onto named users",
						c.field, c.emitted)
					return
				}
			}
		})
	}
}

func TestTheBindingKeyReachesNoEventAndNoLogLine(t *testing.T) {
	for _, c := range []struct {
		name  string
		store *fakeStateStore
		grant bool
	}{
		{"minted at the grant", &fakeStateStore{}, true},
		{
			name:  "adopted on first read after an upgrade",
			store: &fakeStateStore{consent: string(ConsentGranted), installID: "11111111-1111-1111-1111-111111111111"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			var logged bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

			state := NewPersistedState(c.store, logger)
			if c.grant {
				if err := state.SetConsent(context.Background(), ConsentGranted); err != nil {
					t.Fatal(err)
				}
			}
			s := newStoreBackedService(t, state)
			s.Enqueue(context.Background(), SurfaceUI, []RawEvent{tracedUIEvent()})

			secret := c.store.bindingSecret
			if !validBindingSecret(secret) {
				t.Fatalf("precondition: the install holds no binding key, so this proves nothing (%q)", secret)
			}

			events := buffered(s)
			if len(events) != 1 {
				t.Fatalf("buffered %d events, want 1", len(events))
			}
			encoded, err := json.Marshal(recordFor(events[0]))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), secret) {
				t.Errorf("the binding key reached the sink on the event: %s", encoded)
			}
			if strings.Contains(logged.String(), secret) {
				t.Errorf("the binding key reached a log line: %s", logged.String())
			}
		})
	}
}

func TestAnInstallWithNoBindingKeyEmitsNothing(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))

	store := &fakeStateStore{consent: string(ConsentGranted), installID: "11111111-1111-1111-1111-111111111111"}
	store.secretErr = errors.New("db down")
	state := NewPersistedState(store, logger)

	if identity := state.Identity(context.Background()); identity.Marker != "" || len(identity.BindingKey) != 0 {
		t.Errorf("identity = %+v, want none: a marker without a key binds nothing", identity)
	}

	s := newStoreBackedService(t, state)
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{tracedUIEvent()})
	if got := len(buffered(s)); got != 0 {
		t.Errorf("buffered %d events with no binding key, want 0", got)
	}
}

func TestAnUpgradedInstallAdoptsAKeyAndKeepsItsMarker(t *testing.T) {
	const existing = "11111111-1111-1111-1111-111111111111"
	store := &fakeStateStore{consent: string(ConsentGranted), installID: existing}
	state := NewPersistedState(store, discardLogger())

	identity := state.Identity(context.Background())
	if identity.Marker != existing {
		t.Errorf("marker = %q, want the one the install already had, %q", identity.Marker, existing)
	}
	if !validBindingSecret(string(identity.BindingKey)) {
		t.Errorf("binding key = %q, want one adopted on first use", identity.BindingKey)
	}
	if store.bindingSecret != string(identity.BindingKey) {
		t.Errorf("adopted key was not persisted: stored %q, in use %q", store.bindingSecret, identity.BindingKey)
	}
}

func TestAnUpgradedInstallKeepsItsMarkerAndTakesAKeyOnTheNextRead(t *testing.T) {
	const existing = "11111111-1111-1111-1111-111111111111"
	store := &fakeStateStore{consent: string(ConsentGranted), installID: existing}
	state := NewPersistedState(store, discardLogger())

	if err := state.SetConsent(context.Background(), ConsentGranted); err != nil {
		t.Fatal(err)
	}
	if store.installID != existing {
		t.Errorf("marker after re-affirming = %q, want the existing %q", store.installID, existing)
	}

	identity := state.Identity(context.Background())

	if identity.Marker != existing {
		t.Errorf("marker = %q, want the existing %q", identity.Marker, existing)
	}
	if !validBindingSecret(string(identity.BindingKey)) {
		t.Errorf("binding key = %q, want one adopted for the install", identity.BindingKey)
	}
	if !validBindingSecret(store.bindingSecret) {
		t.Errorf("persisted binding key = %q, want the adopted one written through", store.bindingSecret)
	}
}

func TestTwoAdoptersConvergeOnOneKey(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted), installID: "11111111-1111-1111-1111-111111111111"}
	first := NewPersistedState(store, discardLogger()).Identity(context.Background())
	second := NewPersistedState(store, discardLogger()).Identity(context.Background())

	if !validBindingSecret(string(first.BindingKey)) {
		t.Fatalf("precondition: the first adopter settled on %q", first.BindingKey)
	}
	if string(first.BindingKey) != string(second.BindingKey) {
		t.Errorf("two processes hold different keys for one install: %q and %q", first.BindingKey, second.BindingKey)
	}
}

func TestAWithdrawalDiscardsTheBindingKey(t *testing.T) {
	store := &fakeStateStore{}
	state := grantedState(t, store, discardLogger())
	before := state.Identity(context.Background())
	if !validBindingSecret(string(before.BindingKey)) {
		t.Fatalf("precondition: the grant issued no key (%q)", before.BindingKey)
	}

	if err := state.SetConsent(context.Background(), ConsentDenied); err != nil {
		t.Fatal(err)
	}
	if store.bindingSecret != "" {
		t.Errorf("stored binding key after withdrawal = %q, want it discarded", store.bindingSecret)
	}
	if got := state.Identity(context.Background()); got.Marker != "" || len(got.BindingKey) != 0 {
		t.Errorf("identity after withdrawal = %+v, want none", got)
	}
}

func TestARealRotationChangesTheEmittedTraceValue(t *testing.T) {
	ctx := context.Background()
	store := &fakeStateStore{}
	state := grantedState(t, store, discardLogger())

	first := newStoreBackedService(t, state)
	first.Enqueue(ctx, SurfaceServer, []RawEvent{tracedRunEvent()})
	before := traceOf(t, buffered(first)[0])

	for _, decision := range []ConsentState{ConsentDenied, ConsentGranted} {
		if err := state.SetConsent(ctx, decision); err != nil {
			t.Fatal(err)
		}
	}

	second := newStoreBackedService(t, state)
	second.Enqueue(ctx, SurfaceServer, []RawEvent{tracedRunEvent()})
	events := buffered(second)
	if len(events) != 1 {
		t.Fatalf("buffered %d events in the second period, want 1", len(events))
	}
	if after := traceOf(t, events[0]); after == before {
		t.Errorf("one task's trace maps to %q in both periods, so the withdrawal did not unlink them", before)
	}
}

func TestBindingPreservesPerTaskCorrelationThroughTheStore(t *testing.T) {
	other := tracedRunEvent()
	other.Properties[PropTraceID] = "019a1c2d-3e4f-7abc-8def-ffffffffffff"

	state := grantedState(t, &fakeStateStore{}, discardLogger())
	s := newStoreBackedService(t, state)
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{tracedRunEvent(), tracedRunEvent(), other})

	events := buffered(s)
	if len(events) != 3 {
		t.Fatalf("buffered %d events, want 3", len(events))
	}
	first, second, third := traceOf(t, events[0]), traceOf(t, events[1]), traceOf(t, events[2])
	if first != second {
		t.Errorf("one task produced %q and %q, so its events no longer reconstruct into one trace", first, second)
	}
	if third == first {
		t.Error("two tasks collapsed onto one trace value, so per-task correlation merges")
	}
}

func TestAMalformedStoredKeyIsReplaced(t *testing.T) {
	store := &fakeStateStore{
		consent:       string(ConsentGranted),
		installID:     "11111111-1111-1111-1111-111111111111",
		bindingSecret: "short",
	}
	identity := NewPersistedState(store, discardLogger()).Identity(context.Background())
	if !validBindingSecret(string(identity.BindingKey)) {
		t.Errorf("binding key = %q, want a well-formed replacement", identity.BindingKey)
	}
	if store.bindingSecret == "short" {
		t.Error("the malformed key is still stored, so the next process keys with it")
	}
}
