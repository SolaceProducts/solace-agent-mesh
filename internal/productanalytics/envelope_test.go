// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

const clientUUID = "123e4567-e89b-12d3-a456-426614174000"

var testBindingKey = []byte("0123456789abcdef0123456789abcdef")

func testIdentity(marker string) Identity {
	return Identity{Marker: marker, BindingKey: testBindingKey}
}

func fixedIdentity(marker string) IdentityFunc {
	return IdentityFunc(func(context.Context) Identity { return testIdentity(marker) })
}

func mustBindingSecret() []byte {
	secret, err := newBindingSecret()
	if err != nil {
		panic(err)
	}
	return []byte(secret)
}

func sessionEvent(sessionID string) RawEvent {
	return RawEvent{Name: "page_view", SessionID: sessionID, Properties: map[string]any{"path": "/agents"}}
}

func newEnvelopeService(cfg ServiceConfig) *Service {
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 1000
	}
	return NewService(Options{
		Consent:  allowConsent,
		Identity: fixedIdentity("install-1"),
		Config:   cfg,
	})
}

func TestEnqueueStampsTheEnvelope(t *testing.T) {
	s := newEnvelopeService(ServiceConfig{})
	before := time.Now().UTC()
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{{
		EventID:    clientUUID,
		SessionID:  clientUUID,
		OccurredAt: "2026-08-06T00:00:00.000Z",
		Name:       "page_view",
	}})
	if len(buffered(s)) != 1 {
		t.Fatalf("buffered %d events, want 1", len(buffered(s)))
	}
	ev := buffered(s)[0]
	if want := boundID(testIdentity("install-1"), idDomainEvent, clientUUID); ev.EventID != want {
		t.Errorf("eventID = %q, want the client id bound to the install as %q", ev.EventID, want)
	}
	if want := boundID(testIdentity("install-1"), idDomainSession, clientUUID); ev.SessionID != want {
		t.Errorf("sessionID = %q, want the client id bound to the install as %q", ev.SessionID, want)
	}
	for name, got := range map[string]string{"eventID": ev.EventID, "sessionID": ev.SessionID} {
		if got == clientUUID {
			t.Errorf("%s = %q, the client's own value must not reach a sink", name, got)
		}
		if _, err := uuid.Parse(got); err != nil {
			t.Errorf("%s = %q, want a UUID a vendor can take as one: %v", name, got, err)
		}
	}
	if ev.DistinctID != "install-1" {
		t.Errorf("installID = %q, want install-1", ev.DistinctID)
	}
	if ev.PublisherID != s.publisherID {
		t.Errorf("publisherID = %q, want the service's %q", ev.PublisherID, s.publisherID)
	}
	if ev.Seq != 1 {
		t.Errorf("seq = %d, want 1 (the counter starts at 1)", ev.Seq)
	}
	if ev.SchemaVersion != EventSchemaVersion {
		t.Errorf("schemaVersion = %d, want %d", ev.SchemaVersion, EventSchemaVersion)
	}
	if want := time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC); !ev.OccurredAt.Equal(want) {
		t.Errorf("occurredAt = %v, want the client clock %v", ev.OccurredAt, want)
	}
	if ev.ReceivedAt.Before(before) {
		t.Errorf("receivedAt = %v, want the server clock at or after %v", ev.ReceivedAt, before)
	}
}

func TestClientClockSkewIsPreservedNotCorrected(t *testing.T) {
	s := newEnvelopeService(ServiceConfig{})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{{
		OccurredAt: "1999-12-31T23:59:59.000Z",
		Name:       "page_view",
	}})
	ev := buffered(s)[0]
	if ev.OccurredAt.Year() != 1999 {
		t.Errorf("occurredAt = %v, want the client's 1999 instant preserved", ev.OccurredAt)
	}
	if ev.ReceivedAt.Year() == 1999 {
		t.Error("receivedAt took the client clock; it must be the server's")
	}
}

func TestMalformedClientEnvelopeIsReplaced(t *testing.T) {
	s := newEnvelopeService(ServiceConfig{})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{{
		EventID:    "not-a-uuid; drop table events",
		SessionID:  "john.doe@acme.com",
		OccurredAt: "yesterday afternoon",
		Name:       "page_view",
	}})
	ev := buffered(s)[0]
	if _, err := uuid.Parse(ev.EventID); err != nil {
		t.Errorf("eventID = %q, want a server-minted UUID: %v", ev.EventID, err)
	}
	if ev.SessionID != "" {
		t.Errorf("sessionID = %q, want it dropped rather than propagated", ev.SessionID)
	}
	if !ev.OccurredAt.Equal(ev.ReceivedAt) {
		t.Errorf("occurredAt = %v, want the receive-time fallback %v", ev.OccurredAt, ev.ReceivedAt)
	}
}

func TestMissingClientTimeFallsBackToReceiveTime(t *testing.T) {
	s := newEnvelopeService(ServiceConfig{})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{{Name: "page_view"}})
	ev := buffered(s)[0]
	if !ev.OccurredAt.Equal(ev.ReceivedAt) {
		t.Errorf("occurredAt = %v, want the receive time %v", ev.OccurredAt, ev.ReceivedAt)
	}
}

func TestOccurredAtAcceptsEveryRFC3339Form(t *testing.T) {
	for _, clientTime := range []string{
		"2026-08-06T00:00:00Z",
		"2026-08-06T00:00:00.123Z",
		"2026-08-06T00:00:00.123456Z",
		"2026-08-06T00:00:00+05:00",
	} {
		t.Run(clientTime, func(t *testing.T) {
			parsed, err := time.Parse(time.RFC3339, clientTime)
			if err != nil {
				t.Fatalf("test input is not RFC 3339: %v", err)
			}
			want := parsed.Truncate(time.Millisecond)
			s := newEnvelopeService(ServiceConfig{})
			s.Enqueue(context.Background(), SurfaceUI, []RawEvent{{Name: "page_view", OccurredAt: clientTime}})
			ev := buffered(s)[0]
			if !ev.OccurredAt.Equal(want) {
				t.Errorf("occurredAt = %v, want the client time %v", ev.OccurredAt, want)
			}
			if ev.OccurredAt.Equal(ev.ReceivedAt) {
				t.Error("occurredAt fell back to the receive time, so the client value was rejected")
			}
		})
	}
}

func TestReceivedAtKeepsItsMonotonicReading(t *testing.T) {
	s := newEnvelopeService(ServiceConfig{})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	ev := buffered(s)[0]

	if !strings.Contains(ev.ReceivedAt.String(), " m=") {
		t.Errorf("receivedAt = %s, want a monotonic reading rather than wall clock alone", ev.ReceivedAt)
	}
	if !strings.Contains(ev.OccurredAt.String(), " m=") {
		t.Errorf("occurredAt = %s, want the receive-time fallback to keep the reading too", ev.OccurredAt)
	}
	rec := recordFor(ev)
	if !strings.HasSuffix(rec.ReceivedAt, "Z") || !strings.HasSuffix(rec.OccurredAt, "Z") {
		t.Errorf("rendered occurredAt/receivedAt = %q/%q, want UTC instants", rec.OccurredAt, rec.ReceivedAt)
	}
}

func TestEventIDsAreUniquePerEvent(t *testing.T) {
	s := newEnvelopeService(ServiceConfig{})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent(), knownEvent(), knownEvent()})
	seen := make(map[string]bool, len(buffered(s)))
	for _, ev := range buffered(s) {
		if seen[ev.EventID] {
			t.Fatalf("duplicate eventID %q, the idempotency key must be per event", ev.EventID)
		}
		seen[ev.EventID] = true
	}
}

func TestSeqIncrementsMonotonicallyAcrossCalls(t *testing.T) {
	s := newEnvelopeService(ServiceConfig{BufferSize: 100, RateCapPerMin: 100000})
	for i := 0; i < 3; i++ {
		s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	}
	for i, ev := range buffered(s) {
		if want := uint64(i + 1); ev.Seq != want {
			t.Errorf("buf[%d].Seq = %d, want %d", i, ev.Seq, want)
		}
	}
}

func TestRateCapDropLeavesAGapInTheDeliveredSequence(t *testing.T) {
	s := newEnvelopeService(ServiceConfig{BufferSize: 100, RateCapPerMin: 2})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent(), knownEvent(), knownEvent(), knownEvent()})

	budgetOf(s, SurfaceUI, EventPageView).tokens = 1
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})

	got := make([]uint64, 0, len(buffered(s)))
	for _, ev := range buffered(s) {
		got = append(got, ev.Seq)
	}
	want := []uint64{1, 2, 5}
	if len(got) != len(want) {
		t.Fatalf("delivered seqs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("delivered seqs = %v, want %v (3 and 4 were dropped and must stay missing)", got, want)
		}
	}
}

func TestBufferOverflowLeavesAGapInTheDeliveredSequence(t *testing.T) {
	s := newEnvelopeService(ServiceConfig{BufferSize: 2, RateCapPerMin: 100000})
	for i := 0; i < 5; i++ {
		s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	}
	if len(buffered(s)) != 2 {
		t.Fatalf("buffer len = %d, want 2", len(buffered(s)))
	}
	if buffered(s)[0].Seq != 4 || buffered(s)[1].Seq != 5 {
		t.Errorf("surviving seqs = [%d %d], want [4 5]", buffered(s)[0].Seq, buffered(s)[1].Seq)
	}
}

func TestScrubbedOutEventConsumesNoSeq(t *testing.T) {
	s := newEnvelopeService(ServiceConfig{BufferSize: 100, RateCapPerMin: 100000})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{{Name: "made:up"}, knownEvent()})
	if len(buffered(s)) != 1 {
		t.Fatalf("buffered %d, want 1", len(buffered(s)))
	}
	if buffered(s)[0].Seq != 1 {
		t.Errorf("seq = %d, want 1: an unapproved event must not burn a number", buffered(s)[0].Seq)
	}
}

func TestPublisherIDIsStablePerServiceAndUniqueAcross(t *testing.T) {
	a, b := newEnvelopeService(ServiceConfig{}), newEnvelopeService(ServiceConfig{})
	if _, err := uuid.Parse(a.publisherID); err != nil {
		t.Fatalf("publisherID is not a UUID: %v", err)
	}
	if a.publisherID == b.publisherID {
		t.Error("two services share a publisherID, so their sequences cannot be told apart")
	}

	a.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	a.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	if buffered(a)[0].PublisherID != buffered(a)[1].PublisherID {
		t.Error("publisherID changed between events of one service")
	}
}

type rotatingIdentity struct {
	mu       sync.Mutex
	identity Identity
}

func (r *rotatingIdentity) Identity(context.Context) Identity {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.identity
}

func (r *rotatingIdentity) set(marker string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.identity = Identity{Marker: marker, BindingKey: mustBindingSecret()}
}

func newRotatingService(sink Sink) (*Service, *rotatingIdentity) {
	identity := &rotatingIdentity{identity: testIdentity("install-a")}
	return NewService(Options{
		Sink:     sink,
		Consent:  allowConsent,
		Identity: identity,
		Config:   ServiceConfig{FlushInterval: time.Hour, BufferSize: 100, BatchSize: 1000, RateCapPerMin: 100000},
	}), identity
}

func TestARotatedMarkerRotatesThePublisherAndRestartsTheSequence(t *testing.T) {
	s, identity := newRotatingService(nil)
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent(), knownEvent()})
	before := buffered(s)[1]

	identity.set("install-b")
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})

	if len(buffered(s)) != 1 {
		t.Fatalf("buffered %d events after the rotation, want only the one the new marker enqueued", len(buffered(s)))
	}
	after := buffered(s)[0]
	if after.PublisherID == before.PublisherID {
		t.Errorf("publisherID %q survived the rotation, so the two periods are linkable", after.PublisherID)
	}
	if _, err := uuid.Parse(after.PublisherID); err != nil {
		t.Errorf("rotated publisherID is not a UUID: %v", err)
	}
	if after.Seq != 1 {
		t.Errorf("seq = %d, want the counter restarted at 1; a contiguous count spans the rotation", after.Seq)
	}
	if s.discarded != 2 {
		t.Errorf("discarded = %d, want the 2 events of the abandoned marker accounted for", s.discarded)
	}
}

func TestAnUnchangedMarkerRotatesNothing(t *testing.T) {
	s, _ := newRotatingService(nil)
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	if len(buffered(s)) != 2 {
		t.Fatalf("buffered %d events, want 2", len(buffered(s)))
	}
	if buffered(s)[0].PublisherID != buffered(s)[1].PublisherID {
		t.Error("publisherID changed while the marker did not")
	}
	if buffered(s)[1].Seq != 2 {
		t.Errorf("seq = %d, want 2", buffered(s)[1].Seq)
	}
}

func TestNoDeliveredBatchSpansTwoInstallMarkers(t *testing.T) {
	sink := &fakeSink{}
	s, identity := newRotatingService(sink)

	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	identity.set("install-b")
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	s.flush(context.Background())

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.batches) == 0 {
		t.Fatal("nothing was delivered, so the assertion below would hold vacuously")
	}
	for i, batch := range sink.batches {
		markers := make(map[string]bool)
		publishers := make(map[string]bool)
		for _, ev := range batch {
			markers[ev.DistinctID] = true
			publishers[ev.PublisherID] = true
			if ev.DistinctID == "install-a" {
				t.Errorf("batch %d delivered an event of the abandoned marker", i)
			}
		}
		if len(markers) != 1 {
			t.Errorf("batch %d spans %d install markers, want 1", i, len(markers))
		}
		if len(publishers) != 1 {
			t.Errorf("batch %d spans %d publishers, want 1", i, len(publishers))
		}
	}
}

func TestOneClientSessionYieldsUnrelatedValuesEitherSideOfARotation(t *testing.T) {
	const clientSession = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	s, identity := newRotatingService(nil)
	identity.set("11111111-1111-4111-8111-111111111111")
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{sessionEvent(clientSession)})
	before := buffered(s)[0]

	identity.set("22222222-2222-4222-8222-222222222222")
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{sessionEvent(clientSession)})
	after := buffered(s)[0]

	if before.SessionID == after.SessionID {
		t.Errorf("sessionID %q spans both markers, so the two consented periods are joined by it", before.SessionID)
	}
	for _, ev := range []Event{before, after} {
		if ev.SessionID == clientSession {
			t.Errorf("sessionID = %q, the client's own value must not reach a sink", ev.SessionID)
		}
	}
}

func TestOneClientEventIDYieldsUnrelatedValuesEitherSideOfARotation(t *testing.T) {
	s, identity := newRotatingService(nil)
	identity.set("11111111-1111-4111-8111-111111111111")
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{{Name: "page_view", EventID: clientUUID}})
	before := buffered(s)[0]

	identity.set("22222222-2222-4222-8222-222222222222")
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{{Name: "page_view", EventID: clientUUID}})
	after := buffered(s)[0]

	if before.EventID == after.EventID {
		t.Errorf("eventID %q spans both markers, so a client can join the two periods with it", before.EventID)
	}
}

func TestASessionStillGroupsItsEventsWithinOnePeriod(t *testing.T) {
	const other = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	s, _ := newRotatingService(nil)
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{
		sessionEvent(clientUUID), sessionEvent(clientUUID), sessionEvent(other),
	})
	if buffered(s)[0].SessionID != buffered(s)[1].SessionID {
		t.Errorf("one session produced %q and %q, so its own events no longer group", buffered(s)[0].SessionID, buffered(s)[1].SessionID)
	}
	if buffered(s)[0].SessionID == buffered(s)[2].SessionID {
		t.Error("two sessions collapsed onto one value, so per-session funnels merge")
	}
}

func TestTwoServicesSharingAMarkerAgreeOnTheSameSession(t *testing.T) {
	a, b := newEnvelopeService(ServiceConfig{}), newEnvelopeService(ServiceConfig{})
	a.Enqueue(context.Background(), SurfaceUI, []RawEvent{sessionEvent(clientUUID)})
	b.Enqueue(context.Background(), SurfaceUI, []RawEvent{sessionEvent(clientUUID)})
	if buffered(a)[0].SessionID != buffered(b)[0].SessionID {
		t.Errorf("two services produced %q and %q for one session under one marker", buffered(a)[0].SessionID, buffered(b)[0].SessionID)
	}
}

func TestBoundIDsAreDomainSeparated(t *testing.T) {
	identity := testIdentity("11111111-1111-4111-8111-111111111111")
	asEvent := boundID(identity, idDomainEvent, clientUUID)
	asSession := boundID(identity, idDomainSession, clientUUID)
	if asEvent == asSession {
		t.Errorf("one UUID maps to %q as both an event and a session id", asEvent)
	}
	if strings.Contains(asEvent, clientUUID) || strings.Contains(asSession, clientUUID) {
		t.Error("the bound value quotes the client value it was derived from")
	}
}

func TestBothHalvesOfTheIdentityChangeTheMapping(t *testing.T) {
	identity := testIdentity("11111111-1111-4111-8111-111111111111")
	base := boundID(identity, idDomainSession, clientUUID)

	otherKey := identity
	otherKey.BindingKey = mustBindingSecret()
	if boundID(otherKey, idDomainSession, clientUUID) == base {
		t.Error("the binding key does not key the mapping, so any holder of the marker reproduces it")
	}

	otherMarker := identity
	otherMarker.Marker = "22222222-2222-4222-8222-222222222222"
	if boundID(otherMarker, idDomainSession, clientUUID) == base {
		t.Error("the marker does not reach the mapping, so a key outliving a rotation links the two periods")
	}
}

func TestSubMillisecondClientPrecisionIsNotForwarded(t *testing.T) {
	s := newEnvelopeService(ServiceConfig{})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{
		{Name: "page_view", OccurredAt: "2026-08-06T00:00:00.123456789Z"},
	})
	ev := buffered(s)[0]
	if want := time.Date(2026, 8, 6, 0, 0, 0, 123000000, time.UTC); !ev.OccurredAt.Equal(want) {
		t.Errorf("occurredAt = %v, want %v: the digits below the emitted precision must not survive", ev.OccurredAt, want)
	}
}

func TestConsentWithdrawalAndRegrantLeavesNothingToLinkThePeriodsBy(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted)}
	state := NewPersistedState(store, slog.New(slog.DiscardHandler))
	sink := &fakeSink{}
	s := NewService(Options{
		Sink:     sink,
		Consent:  state,
		Identity: state,
		Config:   ServiceConfig{FlushInterval: time.Hour, BufferSize: 100, BatchSize: 1000, RateCapPerMin: 100000},
	})

	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{sessionEvent(clientUUID)})
	s.flush(context.Background())
	if err := state.SetConsent(context.Background(), ConsentDenied); err != nil {
		t.Fatal(err)
	}
	if err := state.SetConsent(context.Background(), ConsentGranted); err != nil {
		t.Fatal(err)
	}
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{sessionEvent(clientUUID)})
	s.flush(context.Background())

	delivered := sink.all()
	if len(delivered) != 2 {
		t.Fatalf("delivered %d events, want one from each consented period", len(delivered))
	}
	periodA, periodB := delivered[0], delivered[1]
	if periodA.DistinctID == periodB.DistinctID {
		t.Fatalf("precondition: the marker did not rotate, both periods are %q", periodA.DistinctID)
	}
	if periodA.PublisherID == periodB.PublisherID {
		t.Errorf("publisherID %q ties the two periods together", periodA.PublisherID)
	}
	if periodB.Seq > periodA.Seq {
		t.Errorf("seq ran on from %d to %d across the withdrawal", periodA.Seq, periodB.Seq)
	}
	if periodA.SessionID == periodB.SessionID {
		t.Errorf("sessionID %q ties the two periods together", periodA.SessionID)
	}
	if periodA.SessionID == clientUUID || periodB.SessionID == clientUUID {
		t.Error("the client's own session id reached the sink, so the browser alone decides whether the periods link")
	}
}
