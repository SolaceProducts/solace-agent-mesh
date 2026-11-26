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
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

const (
	journeySessionID      = "6f9619ff-8b86-d011-b42d-00cf4fc964ff"
	journeyOtherSessionID = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"
	journeyClientEventID  = "9f8a1b2c-3d4e-4f50-9a1b-2c3d4e5f6071"
	journeyBadEventID     = "agent-created; drop table events"
	journeySkewPast       = "1999-12-31T23:59:59.000Z"
	journeySkewFuture     = "2099-01-01T00:00:00.000Z"
)

type journeyEvent struct {
	EventID    string         `json:"eventId,omitempty"`
	SessionID  string         `json:"sessionId,omitempty"`
	OccurredAt string         `json:"occurredAt,omitempty"`
	Event      string         `json:"event"`
	Properties map[string]any `json:"properties,omitempty"`
}

type journeyPage struct {
	label  string
	events []journeyEvent
}

func activationJourney(sessionID string) []journeyPage {
	clientStart := time.Now().UTC().Add(-time.Minute)
	at := func(d time.Duration) string {
		return clientStart.Add(d).Format(productanalytics.TimestampLayout)
	}
	page := func(label string, events ...journeyEvent) journeyPage {
		for i := range events {
			events[i].SessionID = sessionID
		}
		return journeyPage{label: label, events: events}
	}
	return []journeyPage{
		page("first launch",
			journeyEvent{Event: productanalytics.EventAppLaunched, OccurredAt: at(0)},
			journeyEvent{Event: productanalytics.EventPageView, OccurredAt: at(time.Second),
				Properties: map[string]any{"path": "/"}},
		),
		page("model setup",
			journeyEvent{Event: productanalytics.EventPageView, OccurredAt: at(5 * time.Second),
				Properties: map[string]any{"path": "/models/:id"}},
			journeyEvent{Event: productanalytics.EventErrorDisplayed, OccurredAt: at(9 * time.Second),
				EventID:    journeyClientEventID,
				Properties: map[string]any{"errorCategory": "auth_failure", "page": "/models/:id"}},
		),
		page("agent builder",
			journeyEvent{Event: productanalytics.EventPageView, OccurredAt: at(14 * time.Second),
				Properties: map[string]any{"path": "/agents/new/edit"}},
			journeyEvent{Event: productanalytics.EventAgentCreationStart, OccurredAt: journeySkewPast},
			journeyEvent{Event: productanalytics.EventBuilderOpened, OccurredAt: at(21 * time.Second),
				EventID: journeyBadEventID},
			journeyEvent{Event: productanalytics.EventBuilderCompleted, OccurredAt: at(22 * time.Second)},
		),
		page("first chat",
			journeyEvent{Event: productanalytics.EventPageView, OccurredAt: at(25 * time.Second),
				Properties: map[string]any{"path": "/chat"}},
			journeyEvent{Event: productanalytics.EventChatFirstResponse, OccurredAt: journeySkewFuture},
		),
	}
}

func journeySent(pages []journeyPage) []journeyEvent {
	var sent []journeyEvent
	for _, page := range pages {
		sent = append(sent, page.events...)
	}
	return sent
}

type journeySink struct {
	mu      sync.Mutex
	events  []productanalytics.Event
	flushed chan struct{}
}

func newJourneySink() *journeySink {
	return &journeySink{flushed: make(chan struct{}, 64)}
}

func (s *journeySink) Send(_ context.Context, evs []productanalytics.Event) error {
	s.mu.Lock()
	s.events = append(s.events, evs...)
	s.mu.Unlock()
	select {
	case s.flushed <- struct{}{}:
	default:
	}
	return nil
}

func (s *journeySink) all() []productanalytics.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]productanalytics.Event(nil), s.events...)
}

func (s *journeySink) awaitFlush(t *testing.T) {
	t.Helper()
	select {
	case <-s.flushed:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the flush loop to deliver a batch")
	}
}

type journeyPipeline struct {
	ingest *productAnalyticsHandler
	svc    *productanalytics.Service
	sink   *journeySink
	store  *memStateStore
}

func newJourneyPipeline(t *testing.T, cfg productanalytics.ServiceConfig) *journeyPipeline {
	t.Helper()
	consentH, state, store := newTestConsentHandler()
	sink := newJourneySink()
	if cfg.FlushInterval == 0 {
		cfg.FlushInterval = time.Hour
	}
	svc := productanalytics.NewService(productanalytics.Options{
		Sink:     sink,
		Consent:  state,
		Identity: state,
		Config:   cfg,
		Logger:   slog.Default(),
	})
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = svc.Stop(ctx)
	})

	if rec := doConsent(consentH, http.MethodPut, `{"granted":true}`); rec.Code != http.StatusOK {
		t.Fatalf("consent status = %d, want 200", rec.Code)
	}
	return &journeyPipeline{
		ingest: &productAnalyticsHandler{svc: svc, logger: slog.Default()},
		svc:    svc,
		sink:   sink,
		store:  store,
	}
}

func (p *journeyPipeline) postPage(t *testing.T, page journeyPage) {
	t.Helper()
	body, err := json.Marshal(struct {
		Events []journeyEvent `json:"events"`
	}{page.events})
	if err != nil {
		t.Fatal(err)
	}
	if rec := postIngest(p.ingest, string(body)); rec.Code != http.StatusAccepted {
		t.Fatalf("%s: ingest status = %d, want 202", page.label, rec.Code)
	}
}

func (p *journeyPipeline) walk(t *testing.T, pages []journeyPage) {
	t.Helper()
	for _, page := range pages {
		p.postPage(t, page)
	}
}

func (p *journeyPipeline) stop(t *testing.T) []productanalytics.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.svc.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	return p.sink.all()
}

func deliveredJourney(t *testing.T) (*journeyPipeline, []journeyPage, []productanalytics.Event) {
	t.Helper()
	p := newJourneyPipeline(t, productanalytics.ServiceConfig{BatchSize: 1000})
	pages := activationJourney(journeySessionID)
	p.walk(t, pages)
	delivered := p.stop(t)
	return p, pages, delivered
}

func findJourneyEvent(t *testing.T, events []productanalytics.Event, name string) productanalytics.Event {
	t.Helper()
	for _, ev := range events {
		if ev.Name == name {
			return ev
		}
	}
	t.Fatalf("the journey delivered no %q event", name)
	return productanalytics.Event{}
}

func TestActivationJourneyDeliversEveryEventInOrder(t *testing.T) {
	_, pages, got := deliveredJourney(t)

	sent := journeySent(pages)
	if len(got) != len(sent) {
		t.Fatalf("delivered %d events, want the %d the journey sent", len(got), len(sent))
	}
	for i, want := range sent {
		if got[i].Name != want.Event {
			t.Errorf("delivered event %d = %q, want %q", i, got[i].Name, want.Event)
		}
	}
}

func TestActivationJourneyNumbersEveryEventOnceFromOne(t *testing.T) {
	_, _, got := deliveredJourney(t)

	for i, ev := range got {
		if want := uint64(i + 1); ev.Seq != want {
			t.Fatalf("delivered event %d (%s) has seq %d, want %d", i, ev.Name, ev.Seq, want)
		}
	}
}

func TestActivationJourneySessionsAreStableAndDistinguishable(t *testing.T) {
	p := newJourneyPipeline(t, productanalytics.ServiceConfig{BatchSize: 1000})
	first := activationJourney(journeySessionID)
	second := activationJourney(journeyOtherSessionID)
	p.walk(t, first)
	p.walk(t, second)
	got := p.stop(t)

	split := len(journeySent(first))
	if want := split + len(journeySent(second)); len(got) != want {
		t.Fatalf("delivered %d events, want %d", len(got), want)
	}
	firstSession, secondSession := got[0].SessionID, got[split].SessionID
	if firstSession == secondSession {
		t.Fatalf("both journeys delivered session %q, so per-session funnels merge into nonsense", firstSession)
	}
	for i, ev := range got[:split] {
		if ev.SessionID != firstSession {
			t.Errorf("first journey event %d (%s) has session %q, want %q", i, ev.Name, ev.SessionID, firstSession)
		}
	}
	for i, ev := range got[split:] {
		if ev.SessionID != secondSession {
			t.Errorf("second journey event %d (%s) has session %q, want %q", i, ev.Name, ev.SessionID, secondSession)
		}
	}
	for _, sent := range []string{journeySessionID, journeyOtherSessionID} {
		if firstSession == sent || secondSession == sent {
			t.Errorf("the client's own session %q reached the sink", sent)
		}
	}
}

func TestActivationJourneyMintsAUniqueIDPerEvent(t *testing.T) {
	_, _, got := deliveredJourney(t)

	seen := make(map[string]int, len(got))
	for i, ev := range got {
		if prev, ok := seen[ev.EventID]; ok {
			t.Fatalf("events %d (%s) and %d (%s) share eventID %q, so the vendor would deduplicate one away",
				prev, got[prev].Name, i, ev.Name, ev.EventID)
		}
		seen[ev.EventID] = i
		if _, err := uuid.Parse(ev.EventID); err != nil {
			t.Errorf("event %d (%s) eventID = %q, which is not a UUID: %v", i, ev.Name, ev.EventID, err)
		}
	}
}

func TestActivationJourneyBindsAValidClientEventIDAndReplacesAMalformedOne(t *testing.T) {
	p := newJourneyPipeline(t, productanalytics.ServiceConfig{BatchSize: 1000})
	p.walk(t, activationJourney(journeySessionID))
	p.walk(t, activationJourney(journeySessionID))
	got := p.stop(t)

	bound := journeyEventIDs(t, got, productanalytics.EventErrorDisplayed)
	if bound[0] == journeyClientEventID {
		t.Errorf("eventID = %q, the client's own key must not reach a sink", bound[0])
	}
	if bound[0] != bound[1] {
		t.Errorf("one client key produced %q and %q, so a re-sent event would be counted twice", bound[0], bound[1])
	}
	if _, err := uuid.Parse(bound[0]); err != nil {
		t.Errorf("eventID = %q, want a UUID a vendor can deduplicate on: %v", bound[0], err)
	}

	replaced := journeyEventIDs(t, got, productanalytics.EventBuilderOpened)
	if replaced[0] == journeyBadEventID {
		t.Fatalf("eventID = %q, want a malformed client id replaced rather than forwarded", replaced[0])
	}
	if replaced[0] == replaced[1] {
		t.Errorf("a malformed key mapped to %q both times, want a fresh id minted per event", replaced[0])
	}
	if _, err := uuid.Parse(replaced[0]); err != nil {
		t.Errorf("eventID = %q, want a server-minted UUID in its place: %v", replaced[0], err)
	}
}

func journeyEventIDs(t *testing.T, events []productanalytics.Event, name string) [2]string {
	t.Helper()
	var ids []string
	for _, ev := range events {
		if ev.Name == name {
			ids = append(ids, ev.EventID)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("the two journeys delivered %d %q events, want 2", len(ids), name)
	}
	return [2]string{ids[0], ids[1]}
}

func TestActivationJourneyCarriesBothClocksOnEveryEvent(t *testing.T) {
	p := newJourneyPipeline(t, productanalytics.ServiceConfig{BatchSize: 1000})
	pages := activationJourney(journeySessionID)
	before := time.Now().UTC()
	p.walk(t, pages)
	got := p.stop(t)
	after := time.Now().UTC()

	sent := journeySent(pages)
	if len(got) != len(sent) {
		t.Fatalf("delivered %d events, want the %d the journey sent", len(got), len(sent))
	}
	for i, ev := range got {
		want, err := time.Parse(time.RFC3339, sent[i].OccurredAt)
		if err != nil {
			t.Fatalf("event %d: unparseable client time %q: %v", i, sent[i].OccurredAt, err)
		}
		if !ev.OccurredAt.Equal(want) {
			t.Errorf("event %d (%s) occurredAt = %v, want the client's %v", i, ev.Name, ev.OccurredAt, want)
		}
		if ev.ReceivedAt.Before(before) || ev.ReceivedAt.After(after) {
			t.Errorf("event %d (%s) receivedAt = %v, want a server stamp within [%v, %v]", i, ev.Name, ev.ReceivedAt, before, after)
		}
	}
}

func TestActivationJourneySkewedClientClocksLeaveTheServerClockAlone(t *testing.T) {
	p := newJourneyPipeline(t, productanalytics.ServiceConfig{BatchSize: 1000})
	before := time.Now().UTC()
	p.walk(t, activationJourney(journeySessionID))
	got := p.stop(t)
	after := time.Now().UTC()

	skewed := map[string]string{
		productanalytics.EventAgentCreationStart: journeySkewPast,
		productanalytics.EventChatFirstResponse:  journeySkewFuture,
	}
	for name, clientTime := range skewed {
		t.Run(name, func(t *testing.T) {
			want, err := time.Parse(time.RFC3339, clientTime)
			if err != nil {
				t.Fatal(err)
			}
			ev := findJourneyEvent(t, got, name)
			if !ev.OccurredAt.Equal(want) {
				t.Errorf("occurredAt = %v, want the client's %v preserved as sent", ev.OccurredAt, want)
			}
			if ev.ReceivedAt.Before(before) || ev.ReceivedAt.After(after) {
				t.Errorf("receivedAt = %v, want a server stamp within [%v, %v]", ev.ReceivedAt, before, after)
			}
			if ev.OccurredAt.Equal(ev.ReceivedAt) {
				t.Errorf("both clocks read %v, so the skew is no longer measurable", ev.ReceivedAt)
			}
		})
	}
}

func TestActivationJourneyStampsThePersistedInstallMarker(t *testing.T) {
	p, _, got := deliveredJourney(t)

	want := p.store.currentInstallID()
	if want == "" {
		t.Fatal("precondition: granting consent should have issued a marker")
	}
	for i, ev := range got {
		if ev.DistinctID != want {
			t.Errorf("event %d (%s) installID = %q, want the persisted marker %q", i, ev.Name, ev.DistinctID, want)
		}
	}
}

func TestActivationJourneyAttributesEveryEventToOnePublisher(t *testing.T) {
	_, _, first := deliveredJourney(t)
	_, _, second := deliveredJourney(t)
	if len(first) == 0 || len(second) == 0 {
		t.Fatalf("journeys delivered %d and %d events, want both non-empty", len(first), len(second))
	}

	publisher := first[0].PublisherID
	if publisher == "" {
		t.Fatal("publisherID is unset, so a gap cannot be pinned to a process")
	}
	for i, ev := range first {
		if ev.PublisherID != publisher {
			t.Errorf("event %d (%s) publisherID = %q, want the journey's %q", i, ev.Name, ev.PublisherID, publisher)
		}
	}
	if second[0].PublisherID == publisher {
		t.Fatal("two services share a publisherID, so their sequences cannot be told apart")
	}
	for i, ev := range second {
		if ev.PublisherID != second[0].PublisherID {
			t.Errorf("second journey event %d (%s) publisherID = %q, want %q", i, ev.Name, ev.PublisherID, second[0].PublisherID)
		}
	}
}

func TestActivationJourneyStampsTheSchemaVersion(t *testing.T) {
	_, _, got := deliveredJourney(t)

	for i, ev := range got {
		if ev.SchemaVersion != productanalytics.EventSchemaVersion {
			t.Errorf("event %d (%s) schemaVersion = %d, want %d", i, ev.Name, ev.SchemaVersion, productanalytics.EventSchemaVersion)
		}
	}
}

func dropJourneyPages() []journeyPage {
	page := func(label string, paths ...string) journeyPage {
		events := make([]journeyEvent, 0, len(paths))
		for _, path := range paths {
			events = append(events, journeyEvent{
				SessionID:  journeySessionID,
				Event:      productanalytics.EventPageView,
				Properties: map[string]any{"path": path},
			})
		}
		return journeyPage{label: label, events: events}
	}
	return []journeyPage{
		page("browsing", "/", "/agents", "/chat"),
		page("clicking through", "/agents/new/edit", "/models", "/models/:id", "/entrypoints", "/connectors"),
	}
}

func TestJourneyDropsLeaveTheirNumbersMissing(t *testing.T) {
	cases := []struct {
		name     string
		cfg      productanalytics.ServiceConfig
		wantSeqs []uint64
	}{
		{
			name:     "room for the whole journey",
			cfg:      productanalytics.ServiceConfig{BufferSize: 100, BatchSize: 3, RateCapPerMin: 10000},
			wantSeqs: []uint64{1, 2, 3, 4, 5, 6, 7, 8},
		},
		{
			name:     "buffer overflow holes the middle of the journey",
			cfg:      productanalytics.ServiceConfig{BufferSize: 3, BatchSize: 3, RateCapPerMin: 10000},
			wantSeqs: []uint64{1, 2, 3, 6, 7, 8},
		},
		{
			name:     "rate cap truncates the journey",
			cfg:      productanalytics.ServiceConfig{BufferSize: 100, BatchSize: 3, RateCapPerMin: 4},
			wantSeqs: []uint64{1, 2, 3, 4},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newJourneyPipeline(t, tc.cfg)
			pages := dropJourneyPages()

			p.postPage(t, pages[0])
			p.sink.awaitFlush(t)
			p.postPage(t, pages[1])

			var got []uint64
			for _, ev := range p.stop(t) {
				got = append(got, ev.Seq)
			}
			if !slices.Equal(got, tc.wantSeqs) {
				t.Errorf("delivered seqs = %v, want %v — a dropped event's number must stay missing", got, tc.wantSeqs)
			}
		})
	}
}
