// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/samversion"
)

type fakeSink struct {
	mu      sync.Mutex
	batches [][]Event
}

func (f *fakeSink) Send(_ context.Context, events []Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]Event, len(events))
	copy(cp, events)
	f.batches = append(f.batches, cp)
	return nil
}

func (f *fakeSink) all() []Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Event
	for _, b := range f.batches {
		out = append(out, b...)
	}
	return out
}

var allowConsent = ConsentFunc(func(context.Context) bool { return true })

func knownEvent() RawEvent {
	return RawEvent{Name: "page_view", Properties: map[string]any{"path": "/agents"}}
}

func TestConsentClosedByDefaultDropsEverything(t *testing.T) {
	sink := &fakeSink{}
	s := NewService(Options{Identity: fixedIdentity("install-1"), Sink: sink})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	if len(buffered(s)) != 0 {
		t.Fatalf("buffer should be empty when consent is closed, got %d", len(buffered(s)))
	}
}

func TestEnqueueBuffersAndEnrichesWhenConsented(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BatchSize: 1000}})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	if len(buffered(s)) != 1 {
		t.Fatalf("expected 1 buffered event, got %d", len(buffered(s)))
	}
	props := buffered(s)[0].Properties
	for _, key := range []string{propAppVersion, propOS, propArch, PropInterface} {
		if _, ok := props[key]; !ok {
			t.Errorf("enriched event missing segmentation key %q", key)
		}
	}
	if props[PropInterface] != SurfaceUI {
		t.Errorf("interface default = %v, want ui", props[PropInterface])
	}
}

func TestBufferDropsOldestWhenFull(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BufferSize: 2, BatchSize: 1000, RateCapPerMin: 100000}})
	for i := 0; i < 5; i++ {
		s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	}
	if len(buffered(s)) != 2 {
		t.Fatalf("buffer len = %d, want 2 (bounded)", len(buffered(s)))
	}
	if droppedTotal(s) != 3 {
		t.Fatalf("dropped = %d, want 3", droppedTotal(s))
	}
}

func TestRateCapDropsExcess(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BufferSize: 1000, BatchSize: 1000, RateCapPerMin: 2}})
	batch := []RawEvent{knownEvent(), knownEvent(), knownEvent(), knownEvent(), knownEvent()}
	s.Enqueue(context.Background(), SurfaceUI, batch)
	if len(buffered(s)) != 2 {
		t.Fatalf("accepted %d, want 2 (rate cap)", len(buffered(s)))
	}
	if droppedTotal(s) != 3 {
		t.Fatalf("rate-dropped = %d, want 3", droppedTotal(s))
	}
}

func TestInvalidEventNotBuffered(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BatchSize: 1000}})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{{Name: "made:up"}})
	if len(buffered(s)) != 0 {
		t.Fatalf("invalid event should not buffer, got %d", len(buffered(s)))
	}
}

func TestStopDrainsToSink(t *testing.T) {
	sink := &fakeSink{}
	s := NewService(Options{Identity: fixedIdentity("install-1"), Sink: sink, Consent: allowConsent, Config: ServiceConfig{FlushInterval: time.Hour, BatchSize: 1000}})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(sink.all()); got != 1 {
		t.Fatalf("sink received %d events after drain, want 1", got)
	}
}

func TestBatchSizeTriggersFlush(t *testing.T) {
	sink := &fakeSink{}
	s := NewService(Options{Identity: fixedIdentity("install-1"), Sink: sink, Consent: allowConsent, Config: ServiceConfig{FlushInterval: time.Hour, BatchSize: 1}})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})

	deadline := time.After(2 * time.Second)
	for {
		if len(sink.all()) == 1 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("batch-size trigger did not flush within timeout")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type panicSink struct{}

func (panicSink) Send(context.Context, []Event) error { panic("boom") }

func TestFlushRecoversFromSinkPanic(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Sink: panicSink{}, Consent: allowConsent, Config: ServiceConfig{BatchSize: 1000}})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	s.flush(context.Background())
}

type errSink struct{ calls int }

func (e *errSink) Send(context.Context, []Event) error {
	e.calls++
	return errors.New("nope")
}

func TestSendRetriesThenGivesUp(t *testing.T) {
	sink := &errSink{}
	s := NewService(Options{Identity: fixedIdentity("install-1"), Sink: sink, Consent: allowConsent, Config: ServiceConfig{BatchSize: 1000}})
	s.send(context.Background(), []Event{{Name: "page_view"}})
	if sink.calls != flushRetries+1 {
		t.Fatalf("send attempts = %d, want %d", sink.calls, flushRetries+1)
	}
}

func TestRateCapRefillsOverTime(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BufferSize: 1000, BatchSize: 1000, RateCapPerMin: 60}})
	burst := make([]RawEvent, 60)
	for i := range burst {
		burst[i] = knownEvent()
	}
	s.Enqueue(context.Background(), SurfaceUI, burst)
	if len(buffered(s)) != 60 {
		t.Fatalf("initial bucket: accepted %d, want 60", len(buffered(s)))
	}
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	if len(buffered(s)) != 60 {
		t.Fatalf("empty bucket should drop: accepted %d, want still 60", len(buffered(s)))
	}
	uiBudget := budgetOf(s, SurfaceUI, EventPageView)
	uiBudget.lastRefill = uiBudget.lastRefill.Add(-2 * time.Second)
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent(), knownEvent(), knownEvent()})
	if len(buffered(s)) != 62 {
		t.Fatalf("after ~2s refill: accepted %d, want 62 (2 more)", len(buffered(s)))
	}
}

func TestEnrichOverwritesClientSegmentation(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BatchSize: 1000}})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{{Name: "page_view", Properties: map[string]any{
		"path":         "/agents",
		propAppVersion: "9.9.9-pwned",
		propOS:         "pwn-os",
		propArch:       "pwn-arch",
	}}})
	if len(buffered(s)) != 1 {
		t.Fatalf("want 1 buffered, got %d", len(buffered(s)))
	}
	p := buffered(s)[0].Properties
	if p[propAppVersion] != samversion.Version {
		t.Errorf("appVersion = %v, want backend value %v", p[propAppVersion], samversion.Version)
	}
	if p[propOS] != runtime.GOOS {
		t.Errorf("os = %v, want %v", p[propOS], runtime.GOOS)
	}
	if p[propArch] != runtime.GOARCH {
		t.Errorf("arch = %v, want %v", p[propArch], runtime.GOARCH)
	}
}

func TestConsentToggleStopsCollection(t *testing.T) {
	enabled := true
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Consent:  ConsentFunc(func(context.Context) bool { return enabled }),
		Config:   ServiceConfig{BatchSize: 1000},
	})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	if len(buffered(s)) != 1 {
		t.Fatalf("consented: want 1 buffered, got %d", len(buffered(s)))
	}
	enabled = false
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	if len(buffered(s)) != 1 {
		t.Fatalf("consent revoked: want still 1 buffered, got %d", len(buffered(s)))
	}
}

type flakySink struct {
	mu    sync.Mutex
	calls int
	got   []Event
}

func (f *flakySink) Send(_ context.Context, evs []Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls == 1 {
		panic("boom")
	}
	f.got = append(f.got, evs...)
	return nil
}

func (f *flakySink) delivered() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.got)
}

func (f *flakySink) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for !cond() {
		select {
		case <-deadline:
			t.Fatal(msg)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestFlushLoopSurvivesSinkPanic(t *testing.T) {
	sink := &flakySink{}
	s := NewService(Options{Identity: fixedIdentity("install-1"), Sink: sink, Consent: allowConsent, Config: ServiceConfig{FlushInterval: time.Hour, BatchSize: 1}})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	waitFor(t, func() bool { return sink.callCount() == 1 }, "first flush (the panicking one) never ran")

	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	waitFor(t, func() bool { return sink.delivered() == 1 }, "loop did not deliver after recovering from a sink panic")
}

type blockingSink struct {
	mu       sync.Mutex
	inFlight int
}

func (b *blockingSink) Send(ctx context.Context, _ []Event) error {
	b.mu.Lock()
	b.inFlight++
	b.mu.Unlock()
	<-ctx.Done()
	b.mu.Lock()
	b.inFlight--
	b.mu.Unlock()
	return ctx.Err()
}

func (b *blockingSink) running() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inFlight
}

func TestStopFinalDrainDoesNotOutliveDeadline(t *testing.T) {
	sink := &blockingSink{}
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Sink:     sink,
		Consent:  allowConsent,
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{FlushInterval: time.Hour, BatchSize: 1000},
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	began := time.Now()
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(began); elapsed > sendTimeout/2 {
		t.Fatalf("Stop took %v, want the caller's 200ms deadline rather than the send timeout", elapsed)
	}
	waitFor(t, func() bool { return sink.running() == 0 },
		"the sink is still sending after Stop returned, so the deadline never reached it")
}

func TestConcurrentEnqueueIsRaceFree(t *testing.T) {
	sink := &fakeSink{}
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Sink:     sink,
		Consent:  allowConsent,
		Config:   ServiceConfig{BufferSize: 100, BatchSize: 10, FlushInterval: 10 * time.Millisecond, RateCapPerMin: 1_000_000},
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Stop(context.Background()) }()

	const producers, perProducer = 8, 50
	var wg sync.WaitGroup
	for i := 0; i < producers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perProducer; j++ {
				s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
			}
		}()
	}
	wg.Wait()
}

type warnCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *warnCapture) Enabled(_ context.Context, lvl slog.Level) bool { return lvl >= slog.LevelWarn }
func (h *warnCapture) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}
func (h *warnCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *warnCapture) WithGroup(string) slog.Handler      { return h }

func (h *warnCapture) matching(msg string) []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []slog.Record
	for _, r := range h.records {
		if r.Message == msg {
			out = append(out, r)
		}
	}
	return out
}

func (h *warnCapture) drops() []slog.Record { return h.matching(logMsgDropped) }

func (h *warnCapture) all() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]slog.Record(nil), h.records...)
}

func attrInt64(r slog.Record, key string) (int64, bool) {
	var v int64
	var found bool
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v, found = a.Value.Int64(), true
			return false
		}
		return true
	})
	return v, found
}

func TestReportDropsEmitsDeltaOncePerInterval(t *testing.T) {
	cap := &warnCapture{}
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Consent:  allowConsent,
		Logger:   slog.New(cap),
		Config:   ServiceConfig{BufferSize: 1, BatchSize: 1000, RateCapPerMin: 100000},
	})
	for i := 0; i < 4; i++ {
		s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	}
	s.reportDrops()
	drops := cap.drops()
	if len(drops) != 1 {
		t.Fatalf("first report: emitted %d drop lines, want 1", len(drops))
	}
	if delta, ok := attrInt64(drops[0], "dropped"); !ok || delta != 3 {
		t.Fatalf("first report: dropped delta = %d (found=%v), want 3", delta, ok)
	}

	s.reportDrops()
	if got := len(cap.drops()); got != 1 {
		t.Fatalf("silent report expected: total drop lines = %d, want still 1", got)
	}

	for i := 0; i < 2; i++ {
		s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	}
	s.reportDrops()
	drops = cap.drops()
	if len(drops) != 2 {
		t.Fatalf("third report: total drop lines = %d, want 2", len(drops))
	}
	if delta, _ := attrInt64(drops[1], "dropped"); delta != 2 {
		t.Errorf("third report: dropped delta = %d, want 2 (new only)", delta)
	}
	if total, _ := attrInt64(drops[1], "total"); total != 5 {
		t.Errorf("third report: total = %d, want 5 (cumulative)", total)
	}
}

func TestEnqueueKeepsValidEventsInMixedBatch(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BufferSize: 100, BatchSize: 1000, RateCapPerMin: 100000}})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{
		{Name: "made:up"},
		knownEvent(),
		{Name: "also:bogus"},
		knownEvent(),
	})
	if len(buffered(s)) != 2 {
		t.Fatalf("buffered = %d, want 2 (unknown events dropped, valid kept)", len(buffered(s)))
	}
	for _, ev := range buffered(s) {
		if ev.Name != "page_view" {
			t.Errorf("unexpected buffered event %q", ev.Name)
		}
	}
}

func TestParseConfigDefaultsWhenAbsent(t *testing.T) {
	sc := ParseConfig(nil)
	if sc.BufferSize != defaultBufferSize || sc.FlushInterval != defaultFlushInterval ||
		sc.BatchSize != defaultBatchSize || sc.RateCapPerMin != defaultRateCapPerMin {
		t.Errorf("nil config should yield defaults, got %+v", sc)
	}
}

func TestStopWithoutStartStillDrains(t *testing.T) {
	sink := &fakeSink{}
	s := NewService(Options{Identity: fixedIdentity("install-1"), Sink: sink, Consent: allowConsent, Config: ServiceConfig{FlushInterval: time.Hour, BatchSize: 1000}})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(sink.all()); got != 1 {
		t.Fatalf("stop-without-start drained %d events, want 1", got)
	}
}

func TestStartIsIdempotent(t *testing.T) {
	sink := &fakeSink{}
	s := NewService(Options{Identity: fixedIdentity("install-1"), Sink: sink, Consent: allowConsent, Config: ServiceConfig{FlushInterval: time.Hour, BatchSize: 1}})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	waitFor(t, func() bool { return len(sink.all()) == 1 }, "event not delivered after idempotent Start")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
}

type retryThenOKSink struct {
	mu        sync.Mutex
	calls     int
	failFirst int
	delivered int
}

func (s *retryThenOKSink) Send(_ context.Context, evs []Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls <= s.failFirst {
		return errors.New("transient")
	}
	s.delivered += len(evs)
	return nil
}

func TestSendRetriesThenSucceeds(t *testing.T) {
	sink := &retryThenOKSink{failFirst: 1}
	s := NewService(Options{Identity: fixedIdentity("install-1"), Sink: sink, Consent: allowConsent, Config: ServiceConfig{BatchSize: 1000}})
	s.send(context.Background(), []Event{{Name: "page_view"}})
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.calls != 2 {
		t.Fatalf("send attempts = %d, want 2 (one transient failure, then success)", sink.calls)
	}
	if sink.delivered != 1 {
		t.Fatalf("delivered %d events, want 1 (exactly once, no duplicate on retry)", sink.delivered)
	}
}

func TestWithdrawnConsentDiscardsBufferedEvents(t *testing.T) {
	enabled := true
	sink := &fakeSink{}
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Sink:     sink,
		Consent:  ConsentFunc(func(context.Context) bool { return enabled }),
		Config:   ServiceConfig{BatchSize: 1000},
	})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	if len(buffered(s)) != 1 {
		t.Fatalf("want 1 buffered before withdrawal, got %d", len(buffered(s)))
	}

	enabled = false
	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(sink.all()); got != 0 {
		t.Errorf("sent %d events after consent was withdrawn, want 0", got)
	}
}

func TestWebUISurfaceCannotEmitCLIEvents(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BatchSize: 1000}})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{{
		Name:       EventCLICommandRun,
		Properties: map[string]any{PropCommand: "sam run", PropOutcome: OutcomeSuccess},
	}})
	if len(buffered(s)) != 0 {
		t.Errorf("web UI surface emitted a CLI-only event, buffered %d", len(buffered(s)))
	}
}

func TestCLISurfaceCannotEmitWebUIEvents(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BatchSize: 1000}})
	s.Enqueue(context.Background(), SurfaceCLI, []RawEvent{knownEvent()})
	if len(buffered(s)) != 0 {
		t.Errorf("CLI surface emitted a web UI event, buffered %d", len(buffered(s)))
	}
}

func serverRunEvent() RawEvent {
	return RawEvent{
		Name: EventAgentRunCompleted,
		Properties: map[string]any{
			PropOutcome:   OutcomeSuccess,
			PropAgentType: AgentTypeBuiltin,
		},
	}
}

func TestServerSurfaceIsolation(t *testing.T) {
	cases := []struct {
		name    string
		surface string
		raw     RawEvent
	}{
		{"web UI cannot emit a server event", SurfaceUI, serverRunEvent()},
		{"CLI cannot emit a server event", SurfaceCLI, serverRunEvent()},
		{"server cannot emit a web UI event", SurfaceServer, knownEvent()},
		{"server cannot emit a CLI event", SurfaceServer, RawEvent{
			Name:       EventCLICommandRun,
			Properties: map[string]any{PropCommand: "sam run", PropOutcome: OutcomeSuccess},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BatchSize: 1000}})
			s.Enqueue(context.Background(), c.surface, []RawEvent{c.raw})
			if len(buffered(s)) != 0 {
				t.Errorf("surface %q emitted an event from another surface, buffered %d", c.surface, len(buffered(s)))
			}
		})
	}
}

func TestServerEventDropsPath(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BatchSize: 1000}})
	raw := serverRunEvent()
	raw.Properties[propPath] = "/Users/alice/work/acme"
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{raw})
	if len(buffered(s)) != 1 {
		t.Fatalf("want the server event buffered, got %d", len(buffered(s)))
	}
	if got, ok := buffered(s)[0].Properties[propPath]; ok {
		t.Errorf("server event carried path %q, want it stripped", got)
	}
}

func TestCLIEventDropsFilesystemPath(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BatchSize: 1000}})
	s.Enqueue(context.Background(), SurfaceCLI, []RawEvent{{
		Name: EventCLICommandRun,
		Properties: map[string]any{
			PropCommand: "sam run",
			PropOutcome: OutcomeSuccess,
			"path":      "/Users/alice/work/acme",
		},
	}})
	if len(buffered(s)) != 1 {
		t.Fatalf("want the CLI event buffered, got %d", len(buffered(s)))
	}
	if got, ok := buffered(s)[0].Properties["path"]; ok {
		t.Errorf("CLI event carried path %q, want it stripped", got)
	}
}

func TestUnknownSurfaceEmitsNothing(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BatchSize: 1000}})
	s.Enqueue(context.Background(), "smoke-signal", []RawEvent{knownEvent()})
	if len(buffered(s)) != 0 {
		t.Errorf("unknown surface buffered %d events, want 0", len(buffered(s)))
	}
}

func TestFlushDiscardsBufferedEventsAfterAnExternalWithdrawal(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted)}
	state := NewPersistedState(store, nil)
	sink := &fakeSink{}
	s := NewService(Options{
		Sink:     sink,
		Consent:  state,
		Identity: state,
		Config:   ServiceConfig{BatchSize: 1000},
	})

	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	if len(buffered(s)) != 1 {
		t.Fatalf("want 1 buffered event, got %d", len(buffered(s)))
	}

	store.consent = string(ConsentDenied)
	if !state.Enabled(context.Background()) {
		t.Fatal("precondition: the cached grant should still be served")
	}

	if err := s.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(sink.all()); got != 0 {
		t.Errorf("sent %d events after another process withdrew consent, want 0", got)
	}
}

type parkedConsent struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func newParkedConsent() *parkedConsent {
	return &parkedConsent{entered: make(chan struct{}, 1), release: make(chan struct{})}
}

func (p *parkedConsent) Enabled(context.Context) bool {
	if p.calls.Add(1) == 1 {
		return true
	}
	select {
	case p.entered <- struct{}{}:
	default:
	}
	<-p.release
	return true
}

func TestStopWaitsForAnInFlightFlushBeforeReleasingTheStore(t *testing.T) {
	consent := newParkedConsent()
	var released atomic.Bool
	s := NewService(Options{
		Config:   ServiceConfig{BatchSize: 1, FlushInterval: time.Hour},
		Sink:     &fakeSink{},
		Consent:  consent,
		Identity: fixedIdentity("install-1"),
		Logger:   slog.New(slog.DiscardHandler),
		OnStop:   func() { released.Store(true) },
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	select {
	case <-consent.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the flush loop never reached its consent check")
	}

	expired, cancel := context.WithCancel(context.Background())
	cancel()
	stopped := make(chan struct{})
	go func() { _ = s.Stop(expired); close(stopped) }()

	select {
	case <-stopped:
		t.Fatal("Stop returned while a flush was still in flight")
	case <-time.After(100 * time.Millisecond):
	}
	if released.Load() {
		t.Fatal("the store was released while a flush was still reading consent from it")
	}

	close(consent.release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the flush unblocked")
	}
	if !released.Load() {
		t.Error("the store was never released")
	}
}

func TestStopLeavesTheStoreOpenWhenAFlushOutlastsTheGrace(t *testing.T) {
	consent := newParkedConsent()
	var released atomic.Bool
	s := NewService(Options{
		Config:   ServiceConfig{BatchSize: 1, FlushInterval: time.Hour},
		Sink:     &fakeSink{},
		Consent:  consent,
		Identity: fixedIdentity("install-1"),
		Logger:   slog.New(slog.DiscardHandler),
		OnStop:   func() { released.Store(true) },
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer close(consent.release)

	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	select {
	case <-consent.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the flush loop never reached its consent check")
	}

	expired, cancel := context.WithCancel(context.Background())
	cancel()
	stopped := make(chan struct{})
	go func() { _ = s.Stop(expired); close(stopped) }()

	select {
	case <-stopped:
	case <-time.After(stopGrace + 5*time.Second):
		t.Fatal("Stop never gave up on the parked flush")
	}
	if released.Load() {
		t.Error("the store was closed under a flush still reading consent from it")
	}
}

type stubbornFlusher struct {
	fakeSink
	release chan struct{}
}

func (f *stubbornFlusher) Flush(context.Context) error {
	<-f.release
	return nil
}

func TestStopReturnsWhenTheSinkFlushIgnoresItsContext(t *testing.T) {
	sink := &stubbornFlusher{release: make(chan struct{})}
	defer close(sink.release)
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Sink:     sink,
		Consent:  allowConsent,
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{FlushInterval: time.Hour, BatchSize: 1000},
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	stopped := make(chan struct{})
	go func() { _ = s.Stop(ctx); close(stopped) }()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop never returned: a sink Flush ignoring its context held the process open")
	}
}

type parkedFreshConsent struct {
	entered chan struct{}
	release chan struct{}
}

func newParkedFreshConsent() *parkedFreshConsent {
	return &parkedFreshConsent{entered: make(chan struct{}, 1), release: make(chan struct{})}
}

func (p *parkedFreshConsent) Enabled(context.Context) bool { return true }

func (p *parkedFreshConsent) EnabledFresh(context.Context) bool {
	select {
	case p.entered <- struct{}{}:
	default:
	}
	<-p.release
	return true
}

func TestStopReportsEventsStrandedByItsDeadline(t *testing.T) {
	consent := newParkedFreshConsent()
	capture := &warnCapture{}
	s := NewService(Options{
		Sink:     &fakeSink{},
		Consent:  consent,
		Identity: fixedIdentity("install-1"),
		Logger:   slog.New(capture),
		Config:   ServiceConfig{BufferSize: 100, BatchSize: 1, RateCapPerMin: 100000, FlushInterval: time.Hour},
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer close(consent.release)

	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	select {
	case <-consent.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the flush loop never reached its consent check")
	}
	for i := 0; i < 3; i++ {
		s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	}

	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Stop(expired); err != nil {
		t.Fatal(err)
	}

	discards := capture.matching(logMsgDiscarded)
	if len(discards) != 1 {
		t.Fatalf("emitted %d discard lines, want 1 for the stranded buffer", len(discards))
	}
	if n, ok := attrInt64(discards[0], "discarded"); !ok || n != 3 {
		t.Errorf("discarded = %d (found=%v), want the 3 events left in the buffer", n, ok)
	}
}

func TestConsentDiscardIsCountedAndReported(t *testing.T) {
	var granted atomic.Bool
	granted.Store(true)
	capture := &warnCapture{}
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Sink:     &fakeSink{},
		Consent:  ConsentFunc(func(context.Context) bool { return granted.Load() }),
		Logger:   slog.New(capture),
		Config:   ServiceConfig{BufferSize: 100, BatchSize: 1000, RateCapPerMin: 100000},
	})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent(), knownEvent()})

	granted.Store(false)
	s.flush(context.Background())
	s.reportDrops()

	if got := len(capture.matching(logMsgConsentDiscard)); got != 1 {
		t.Errorf("consent discard emitted %d WARN lines, want 1 (Debug is invisible by default)", got)
	}
	discards := capture.matching(logMsgDiscarded)
	if len(discards) != 1 {
		t.Fatalf("emitted %d discard lines, want 1", len(discards))
	}
	if n, _ := attrInt64(discards[0], "discarded"); n != 2 {
		t.Errorf("discarded = %d, want 2", n)
	}
	if got := len(capture.drops()); got != 0 {
		t.Errorf("emitted %d pre-buffer drop lines, want 0: the two counts must stay distinct", got)
	}
}

func TestAPanickingConsentProviderDoesNotCrashTheHost(t *testing.T) {
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Sink:     &fakeSink{},
		Consent:  ConsentFunc(func(context.Context) bool { panic("consent store exploded") }),
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{BatchSize: 1000},
	})
	budgetOf(s, SurfaceUI, EventPageView).buf = []Event{{Name: "page_view"}, {Name: "page_view"}}

	s.flush(context.Background())

	if s.discarded != 2 {
		t.Errorf("discarded = %d, want 2: a batch lost to a panicking provider is still lost", s.discarded)
	}
	if droppedTotal(s) != 0 {
		t.Errorf("dropped = %d, want 0: nothing was refused admission", droppedTotal(s))
	}
}

func TestExhaustedRetriesAreCountedAsDiscarded(t *testing.T) {
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Sink:     &errSink{},
		Consent:  allowConsent,
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{BatchSize: 1000},
	})
	s.send(context.Background(), []Event{{Name: "page_view"}, {Name: "page_view"}})
	if s.discarded != 2 {
		t.Errorf("discarded = %d after retries were exhausted, want 2", s.discarded)
	}
	if droppedTotal(s) != 0 {
		t.Errorf("dropped = %d, want 0: a send failure is not a pre-buffer drop", droppedTotal(s))
	}
}

func TestNonRetryableAbandonIsCountedAsDiscarded(t *testing.T) {
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Sink:     &nonRetryableSink{},
		Consent:  allowConsent,
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{BatchSize: 1000},
	})
	s.send(context.Background(), []Event{{Name: "page_view"}})
	if s.discarded != 1 {
		t.Errorf("discarded = %d after a terminal send error, want 1", s.discarded)
	}
}

type partialDropSink struct {
	calls int
	n     int
}

func (p *partialDropSink) Send(context.Context, []Event) error {
	p.calls++
	return PartialDrop(errors.New("vendor accepted a partial batch"), p.n)
}

func TestPartialDropCountsOnlyTheExcludedEvents(t *testing.T) {
	sink := &partialDropSink{n: 1}
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Sink:     sink,
		Consent:  allowConsent,
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{BatchSize: 1000},
	})
	s.send(context.Background(), []Event{{Name: "page_view"}, {Name: "page_view"}})
	if sink.calls != 1 {
		t.Errorf("send attempts = %d, want 1 (a partial drop must not burn retries)", sink.calls)
	}
	if s.discarded != 1 {
		t.Errorf("discarded = %d, want 1: only the excluded event, not the whole batch", s.discarded)
	}
}

func TestRecoveredSinkPanicIsCountedAsDiscarded(t *testing.T) {
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Sink:     panicSink{},
		Consent:  allowConsent,
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{BufferSize: 100, BatchSize: 1000, RateCapPerMin: 100000},
	})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent(), knownEvent()})
	s.flush(context.Background())
	if s.discarded != 2 {
		t.Errorf("discarded = %d after a panicking sink swallowed the batch, want 2", s.discarded)
	}
}

func TestConcurrentEnqueueOrdersReceiveTimesWithSeq(t *testing.T) {
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Consent:  allowConsent,
		Config:   ServiceConfig{BufferSize: 1000, BatchSize: 1_000_000, RateCapPerMin: 1_000_000},
	})
	const callers = 200
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
		}()
	}
	wg.Wait()

	batch := s.drain()
	if len(batch) != callers {
		t.Fatalf("buffered %d events, want %d", len(batch), callers)
	}
	sort.Slice(batch, func(i, j int) bool { return batch[i].Seq < batch[j].Seq })
	inversions := 0
	for i := 1; i < len(batch); i++ {
		if batch[i].ReceivedAt.Before(batch[i-1].ReceivedAt) {
			inversions++
		}
	}
	if inversions != 0 {
		t.Errorf("%d of %d events carry a receivedAt earlier than their predecessor's, contradicting the sequence", inversions, callers)
	}
}

func TestStopIsIdempotent(t *testing.T) {
	sink := &capabilitySink{}
	var stops atomic.Int32
	s := NewService(Options{
		Sink:     sink,
		Consent:  allowConsent,
		Identity: fixedIdentity("install-1"),
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{BufferSize: 100, BatchSize: 1000, RateCapPerMin: 100000, FlushInterval: time.Hour},
		OnStop:   func() { stops.Add(1) },
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if got := stops.Load(); got != 1 {
		t.Errorf("OnStop ran %d times, want 1", got)
	}
	if got := sink.flushCount(); got != 1 {
		t.Errorf("sink Flush ran %d times, want 1: the second Stop flushed a released sink", got)
	}
}

type stubbornSink struct {
	mu      sync.Mutex
	sent    int
	release chan struct{}
}

func (s *stubbornSink) Send(_ context.Context, evs []Event) error {
	<-s.release
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent += len(evs)
	return nil
}

func TestStopReturnsWhenTheSinkSendIgnoresItsContext(t *testing.T) {
	sink := &stubbornSink{release: make(chan struct{})}
	defer close(sink.release)
	var released atomic.Bool
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Sink:     sink,
		Consent:  allowConsent,
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{FlushInterval: time.Hour, BatchSize: 1000},
		OnStop:   func() { released.Store(true) },
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	stopped := make(chan struct{})
	began := time.Now()
	go func() { _ = s.Stop(ctx); close(stopped) }()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop never returned: a sink Send ignoring its context held the process open")
	}
	if elapsed := time.Since(began); elapsed > 2*time.Second {
		t.Errorf("Stop took %v, want it bounded by the caller's 200ms deadline", elapsed)
	}
	if released.Load() {
		t.Error("the store was released while the abandoned drain was still sending through it")
	}
}

func TestEnqueueAfterStopIsRefusedRatherThanStranded(t *testing.T) {
	sink := &fakeSink{}
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Sink:     sink,
		Consent:  allowConsent,
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{BufferSize: 100, BatchSize: 1000, RateCapPerMin: 100000, FlushInterval: time.Hour},
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	}

	delivered := int64(len(sink.all()))
	dropped := droppedTotal(s)
	stillBuffered := len(buffered(s))
	s.mu.Lock()
	defer s.mu.Unlock()
	if stillBuffered != 0 {
		t.Errorf("%d events sit in the buffer after Stop, with no flush left to carry them", stillBuffered)
	}
	if s.seq != 1 {
		t.Errorf("issued %d sequence numbers, want 1: a refused event must not burn a number", s.seq)
	}
	if want := delivered + dropped + s.discarded + int64(stillBuffered); int64(s.seq) != want {
		t.Errorf("issued %d numbers but delivered+dropped+discarded+buffered = %d", s.seq, want)
	}
}

func TestStartAfterStopLaunchesNoLoop(t *testing.T) {
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Sink:     &fakeSink{},
		Consent:  allowConsent,
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{BufferSize: 100, BatchSize: 1, RateCapPerMin: 100000, FlushInterval: 10 * time.Millisecond},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	exited := make(chan struct{})
	go func() { s.wg.Wait(); close(exited) }()
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("Start after Stop launched a flush loop that nothing can cancel")
	}
}

func TestUnsetSurfaceEmitsNothing(t *testing.T) {
	s := NewService(Options{
		Sink:     &fakeSink{},
		Consent:  ConsentFunc(func(context.Context) bool { return true }),
		Identity: fixedIdentity("install-1"),
		Logger:   slog.New(slog.DiscardHandler),
	})

	s.Enqueue(context.Background(), "", []RawEvent{knownEvent()})

	if len(buffered(s)) != 0 {
		t.Fatalf("buffered %d events for an unset surface, want none", len(buffered(s)))
	}
}

func TestABareContextStillBoundsTheWait(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Logger: slog.New(slog.DiscardHandler)})
	s.deadlineFallback = 20 * time.Millisecond
	release := make(chan struct{})
	defer close(release)

	finished := make(chan bool, 1)
	go func() {
		finished <- s.runBounded(context.Background(), logMsgDrainAbandoned, func() { <-release })
	}()
	select {
	case waited := <-finished:
		if waited {
			t.Error("work outliving the fallback must be abandoned, not waited on")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runBounded never returned: a bare context reinstated the unbounded wait")
	}
}

func TestAnExistingDeadlineIsLeftAlone(t *testing.T) {
	want := time.Now().Add(time.Hour)
	ctx, cancelOuter := context.WithDeadline(context.Background(), want)
	defer cancelOuter()

	bounded, cancel := withFallbackDeadline(ctx, time.Millisecond)
	defer cancel()

	got, ok := bounded.Deadline()
	if !ok || !got.Equal(want) {
		t.Errorf("deadline = %v (set %v), want the caller's %v left untouched", got, ok, want)
	}
}

func TestWorkFinishingAsTheDeadlineFiresCountsAsFinished(t *testing.T) {
	for i := 0; i < 200; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		done := make(chan struct{})
		close(done)

		if !waitBounded(ctx, done) {
			cancel()
			t.Fatalf("run %d reported abandoned, but the work was already complete when the deadline fired", i)
		}
		cancel()
	}
}

func TestAnUnsetFallbackDoesNotExpireOnArrival(t *testing.T) {
	for _, fallback := range []time.Duration{0, -time.Second} {
		ctx, cancel := withFallbackDeadline(context.Background(), fallback)
		deadline, ok := ctx.Deadline()
		cancel()
		if !ok {
			t.Fatalf("fallback %v produced no deadline at all", fallback)
		}
		if !deadline.After(time.Now()) {
			t.Errorf("fallback %v produced a deadline already past, so every wait would be abandoned unstarted", fallback)
		}
	}
}

func TestFlushRefusesToSendWhenTheConsentReadFails(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted)}
	state := NewPersistedState(store, slog.New(slog.DiscardHandler))
	sink := &fakeSink{}
	s := NewService(Options{
		Sink:     sink,
		Consent:  state,
		Identity: state,
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{FlushInterval: time.Hour, BufferSize: 100, BatchSize: 1000, RateCapPerMin: 100000},
	})

	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	if len(buffered(s)) != 1 {
		t.Fatalf("precondition: buffered %d events, want 1", len(buffered(s)))
	}

	store.consent = string(ConsentDenied)
	store.err = errors.New("db down")

	s.flush(context.Background())

	if got := len(sink.all()); got != 0 {
		t.Errorf("delivered %d events on an unreadable consent read, want none", got)
	}
	s.mu.Lock()
	discarded := s.discarded
	s.mu.Unlock()
	if discarded != 1 {
		t.Errorf("discarded = %d, want the refused batch accounted for", discarded)
	}
}

func TestSinkFlushIsRefusedWhenTheConsentReadFails(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted)}
	state := NewPersistedState(store, slog.New(slog.DiscardHandler))
	sink := &flushCountingSink{}
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Sink:     sink,
		Consent:  state,
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{FlushInterval: time.Hour, BatchSize: 1000},
	})

	store.err = errors.New("db down")
	if !s.flushSink(context.Background()) {
		t.Fatal("the bounded wait should have completed")
	}
	if got := sink.flushes.Load(); got != 0 {
		t.Errorf("sink flushes = %d, want none while consent cannot be confirmed", got)
	}
}

type flushCountingSink struct {
	fakeSink
	flushes atomic.Int64
}

func (f *flushCountingSink) Flush(context.Context) error {
	f.flushes.Add(1)
	return nil
}

type retryThenSucceedSink struct {
	mu    sync.Mutex
	calls int
}

func (r *retryThenSucceedSink) Send(context.Context, []Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.calls == 1 {
		return errors.New("transient")
	}
	return nil
}

func (r *retryThenSucceedSink) attempts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type withdrawAfterFirstFreshRead struct{ freshReads atomic.Int64 }

func (c *withdrawAfterFirstFreshRead) Enabled(context.Context) bool { return true }
func (c *withdrawAfterFirstFreshRead) EnabledFresh(context.Context) bool {
	return c.freshReads.Add(1) == 1
}

func TestConsentIsRecheckedBetweenSendAttempts(t *testing.T) {
	consent := &withdrawAfterFirstFreshRead{}
	sink := &retryThenSucceedSink{}
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Sink:     sink,
		Consent:  consent,
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{FlushInterval: time.Hour, BatchSize: 1000},
	})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	s.flush(context.Background())

	if got := sink.attempts(); got != 1 {
		t.Errorf("sink was called %d times, want 1: the retry must not outlive the grant", got)
	}
	s.mu.Lock()
	discarded := s.discarded
	s.mu.Unlock()
	if discarded != 1 {
		t.Errorf("discarded = %d, want the undelivered event accounted for", discarded)
	}
}

func TestARetryStillDeliversWhileConsentStands(t *testing.T) {
	sink := &retryThenSucceedSink{}
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Sink:     sink,
		Consent:  allowConsent,
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{FlushInterval: time.Hour, BatchSize: 1000},
	})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	s.flush(context.Background())

	if got := sink.attempts(); got != 2 {
		t.Errorf("sink was called %d times, want 2 (one failure, one success)", got)
	}
	s.mu.Lock()
	discarded := s.discarded
	s.mu.Unlock()
	if discarded != 0 {
		t.Errorf("discarded = %d, want 0: the batch was delivered", discarded)
	}
}

type vendorURLSink struct {
	err error
}

func (v vendorURLSink) Send(context.Context, []Event) error { return v.err }
func (v vendorURLSink) Identify(context.Context, string, map[string]any) error {
	return v.err
}
func (v vendorURLSink) Flush(context.Context) error { return v.err }

const vendorSecret = "s3cr3t-vendor-token"

func vendorTransportError() error {
	return &url.Error{
		Op:  "Post",
		URL: "https://api.vendor.example/v1/track?token=" + vendorSecret,
		Err: errors.New("dial tcp 203.0.113.7:443: connect: connection refused"),
	}
}

func loggedErrors(t *testing.T, records []slog.Record) []string {
	t.Helper()
	return loggedAttr(records, "error")
}

func loggedAttr(records []slog.Record, key string) []string {
	var out []string
	for _, r := range records {
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == key {
				out = append(out, a.Value.String())
			}
			return true
		})
	}
	return out
}

func TestSinkErrorsAreLoggedWithoutTheirDestination(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want []string
	}{
		{
			name: "a retryable transport failure, logged on each attempt and at the give-up",
			err:  vendorTransportError(),
			want: []string{logMsgSendFail, logMsgSendGave},
		},
		{
			name: "a terminal transport failure, logged once",
			err:  NonRetryable(vendorTransportError()),
			want: []string{logMsgSendAbandoned},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			capture := &warnCapture{}
			s := NewService(Options{
				Identity: fixedIdentity("install-1"),
				Sink:     vendorURLSink{err: c.err},
				Consent:  allowConsent,
				Logger:   slog.New(capture),
				Config:   ServiceConfig{FlushInterval: time.Hour, BatchSize: 1000},
			})
			s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
			s.flush(context.Background())

			for _, msg := range c.want {
				records := capture.matching(msg)
				if len(records) == 0 {
					t.Fatalf("expected a %q line", msg)
				}
				for _, logged := range loggedErrors(t, records) {
					assertNoDestination(t, msg, logged)
				}
			}
		})
	}
}

func TestIdentifyAndFlushErrorsAreLoggedWithoutTheirDestination(t *testing.T) {
	capture := &warnCapture{}
	s := NewService(Options{
		Sink:     vendorURLSink{err: vendorTransportError()},
		Consent:  allowConsent,
		Identity: fixedIdentity("install-1"),
		Logger:   slog.New(capture),
		Config:   ServiceConfig{FlushInterval: time.Hour, BatchSize: 1000},
	})
	s.identifyOnce(t.Context(), "install-1", "", nil)
	if !s.flushSink(context.Background()) {
		t.Fatal("the bounded wait should have completed")
	}

	for _, msg := range []string{logMsgIdentifyFail, logMsgSinkFlushFail} {
		records := capture.matching(msg)
		if len(records) == 0 {
			t.Fatalf("expected a %q line", msg)
		}
		for _, logged := range loggedErrors(t, records) {
			assertNoDestination(t, msg, logged)
		}
	}
}

func assertNoDestination(t *testing.T, msg, logged string) {
	t.Helper()
	for _, forbidden := range []string{vendorSecret, "https://", "api.vendor.example", "token="} {
		if strings.Contains(logged, forbidden) {
			t.Errorf("%s logged %q, which leaks %q", msg, logged, forbidden)
		}
	}
	if !strings.Contains(logged, "Post") || !strings.Contains(logged, "connection refused") {
		t.Errorf("%s logged %q, which no longer says what failed or how", msg, logged)
	}
}

func TestWithoutSecretsKeepsAnErrorThatNamesNoSecret(t *testing.T) {
	for _, err := range []error{
		errors.New("vendor rejected the batch: 429 too many requests"),
		errors.New("context deadline exceeded (Client.Timeout exceeded while awaiting headers)"),
	} {
		if got := withoutSecrets(err); got != err {
			t.Errorf("withoutSecrets returned %v, want the original error untouched", got)
		}
	}
	if withoutSecrets(nil) != nil {
		t.Error("withoutSecrets(nil) must stay nil")
	}
}

func TestWithoutSecretsStripsAnEmbeddedURL(t *testing.T) {
	err := fmt.Errorf("push to https://api.vendor.example/v1/track?token=%s failed: %w", vendorSecret, errors.New("i/o timeout"))
	got := withoutSecrets(err).Error()
	if strings.Contains(got, vendorSecret) || strings.Contains(got, "api.vendor.example") {
		t.Errorf("withoutSecrets = %q, want the endpoint gone", got)
	}
	if !strings.Contains(got, "i/o timeout") {
		t.Errorf("withoutSecrets = %q, want the failure kind kept", got)
	}
}

func TestWithoutSecretsStripsCredentialsThatAreNotURLs(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		forbidden []string
		kept      []string
	}{
		{
			name:      "a vendor naming the write key it rejected",
			err:       errors.New("segment: 401 Unauthorized, write key SEGMENT_WRITEKEY_abc123 rejected"),
			forbidden: []string{"SEGMENT_WRITEKEY_abc123"},
			kept:      []string{"401 Unauthorized", "rejected"},
		},
		{
			name:      "a database transport printing its DSN",
			err:       errors.New("dial: host=db.internal user=sam password=hunter2 dbname=sam: connection reset"),
			forbidden: []string{"hunter2", "db.internal", "sam"},
			kept:      []string{"dial", "connection reset"},
		},
		{
			name:      "a proxy quoting the header it read",
			err:       errors.New("dial tcp api.vendor.example:443 (Authorization: Bearer eyJhbGciOi): refused"),
			forbidden: []string{"eyJhbGciOi", "api.vendor.example"},
			kept:      []string{"dial tcp", "refused"},
		},
		{
			name:      "an endpoint composed without a scheme",
			err:       errors.New("push to //api.vendor.example/track?token=" + vendorSecret + " failed: i/o timeout"),
			forbidden: []string{vendorSecret, "api.vendor.example", "token="},
			kept:      []string{"push to", "i/o timeout"},
		},
		{
			name:      "a scheme URL, which must keep working",
			err:       vendorTransportError(),
			forbidden: []string{vendorSecret, "https://", "api.vendor.example", "token="},
			kept:      []string{"Post", "connection refused"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := withoutSecrets(c.err).Error()
			for _, forbidden := range c.forbidden {
				if strings.Contains(got, forbidden) {
					t.Errorf("withoutSecrets = %q, which still carries %q", got, forbidden)
				}
			}
			for _, kept := range c.kept {
				if !strings.Contains(got, kept) {
					t.Errorf("withoutSecrets = %q, which no longer says %q", got, kept)
				}
			}
		})
	}
}

type panickingVendorSink struct {
	value any
}

func (p panickingVendorSink) Send(context.Context, []Event) error { panic(p.value) }
func (p panickingVendorSink) Identify(context.Context, string, map[string]any) error {
	panic(p.value)
}
func (p panickingVendorSink) Flush(context.Context) error { panic(p.value) }

func TestRecoveredPanicsAreLoggedWithoutTheirSecrets(t *testing.T) {
	cases := []struct {
		name  string
		value any
	}{
		{name: "an error value", value: vendorTransportError()},
		{name: "a string value", value: "post https://api.vendor.example/v1/track?token=" + vendorSecret + ": connection reset"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			capture := &warnCapture{}
			s := NewService(Options{
				Sink:     panickingVendorSink{value: c.value},
				Consent:  allowConsent,
				Identity: fixedIdentity("install-1"),
				Logger:   slog.New(capture),
				Config:   ServiceConfig{FlushInterval: time.Hour, BufferSize: 100, BatchSize: 1000, RateCapPerMin: 100000},
			})
			s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
			s.flush(context.Background())
			s.identifyOnce(t.Context(), "install-1", "", nil)
			s.flushSink(context.Background())

			panics := loggedAttr(capture.all(), "panic")
			if len(panics) < 3 {
				t.Fatalf("captured %d panic lines, want one from each of flush, identify, and the sink flush", len(panics))
			}
			for _, logged := range panics {
				for _, forbidden := range []string{vendorSecret, "https://", "api.vendor.example", "token="} {
					if strings.Contains(logged, forbidden) {
						t.Errorf("a recovered panic logged %q, which leaks %q", logged, forbidden)
					}
				}
				if !strings.Contains(logged, redactedSecret) {
					t.Errorf("a recovered panic logged %q, which redacted nothing at all", logged)
				}
			}
		})
	}
}

func TestExportedSanitizersRedactForCallersOutsideThePackage(t *testing.T) {
	got := SanitizedError(errors.New("dial postgres://sam:hunter2@db.internal:5432/sam failed"))
	if strings.Contains(got.Error(), "hunter2") {
		t.Errorf("SanitizedError kept the password: %v", got)
	}
	const token = "sk-ant-api03-9f8e7d6c5b4a3210fedcba9876543210"
	if panicked := SanitizedPanic(errors.New("auth failed with " + token)); strings.Contains(panicked, token) {
		t.Errorf("SanitizedPanic kept the token: %v", panicked)
	}
}
