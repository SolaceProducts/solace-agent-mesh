// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type capabilitySink struct {
	mu           sync.Mutex
	batches      [][]Event
	identifies   []map[string]any
	installIDs   []string
	flushes      int
	maxBatch     int
	sendErr      error
	identifyErr  error
	identifyFail int
}

func (c *capabilitySink) Send(_ context.Context, events []Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sendErr != nil {
		return c.sendErr
	}
	c.batches = append(c.batches, append([]Event(nil), events...))
	return nil
}

func (c *capabilitySink) Identify(_ context.Context, installID string, traits map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.identifyFail > 0 {
		c.identifyFail--
		return c.identifyErr
	}
	c.installIDs = append(c.installIDs, installID)
	c.identifies = append(c.identifies, traits)
	return nil
}

func (c *capabilitySink) Flush(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.flushes++
	return nil
}

func (c *capabilitySink) MaxBatchSize() int { return c.maxBatch }

func (c *capabilitySink) sizes() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]int, 0, len(c.batches))
	for _, b := range c.batches {
		out = append(out, len(b))
	}
	return out
}

func (c *capabilitySink) identifyCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.identifies)
}

func (c *capabilitySink) flushCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.flushes
}

func newCapabilityService(sink Sink) *Service {
	return NewService(Options{
		Sink:     sink,
		Consent:  allowConsent,
		Identity: fixedIdentity("install-1"),
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{BufferSize: 1000, BatchSize: 1000, RateCapPerMin: 100000, FlushInterval: time.Hour},
	})
}

func TestSendSplitsToTheSinkBatchLimit(t *testing.T) {
	sink := &capabilitySink{maxBatch: 2}
	s := newCapabilityService(sink)
	s.send(context.Background(), make([]Event, 5))
	got := sink.sizes()
	want := []int{2, 2, 1}
	if len(got) != len(want) {
		t.Fatalf("chunk sizes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("chunk sizes = %v, want %v", got, want)
		}
	}
}

func TestSendDoesNotSplitWithinTheLimit(t *testing.T) {
	sink := &capabilitySink{maxBatch: 10}
	s := newCapabilityService(sink)
	s.send(context.Background(), make([]Event, 5))
	if got := sink.sizes(); len(got) != 1 || got[0] != 5 {
		t.Errorf("chunk sizes = %v, want a single batch of 5", got)
	}
}

func TestSendDoesNotSplitForAPlainSink(t *testing.T) {
	sink := &fakeSink{}
	s := NewService(Options{Identity: fixedIdentity("install-1"), Sink: sink, Consent: allowConsent, Config: ServiceConfig{BatchSize: 1000}})
	s.send(context.Background(), make([]Event, 5))
	if got := len(sink.batches); got != 1 {
		t.Errorf("plain sink received %d batches, want 1", got)
	}
}

type nonRetryableSink struct {
	mu    sync.Mutex
	calls int
}

func (n *nonRetryableSink) Send(context.Context, []Event) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls++
	return NonRetryable(errors.New("payload rejected"))
}

func (n *nonRetryableSink) callCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.calls
}

func TestSendAbandonsANonRetryableBatchImmediately(t *testing.T) {
	sink := &nonRetryableSink{}
	s := NewService(Options{Identity: fixedIdentity("install-1"), Sink: sink, Consent: allowConsent, Logger: slog.New(slog.DiscardHandler), Config: ServiceConfig{BatchSize: 1000}})
	s.send(context.Background(), []Event{{Name: "page_view"}})
	if got := sink.callCount(); got != 1 {
		t.Errorf("send attempts = %d, want 1 (a terminal error must not burn retries)", got)
	}
}

func TestIsRetryableDefaultsToTrue(t *testing.T) {
	if !IsRetryable(errors.New("plain")) {
		t.Error("a plain error must stay retryable, so today's transport behavior is unchanged")
	}
	if !IsRetryable(nil) {
		t.Error("nil must not be classified as terminal")
	}
	if IsRetryable(NonRetryable(errors.New("terminal"))) {
		t.Error("a NonRetryable error must classify as terminal")
	}
}

func TestIsRetryableSeesThroughWrapping(t *testing.T) {
	wrapped := errors.Join(errors.New("context"), NonRetryable(errors.New("terminal")))
	if IsRetryable(wrapped) {
		t.Error("a wrapped terminal error must still classify as terminal")
	}
}

func TestPartialDropCarriesTheExcludedCount(t *testing.T) {
	base := errors.New("vendor accepted a partial batch")
	err := PartialDrop(base, 3)
	if got := PartialDropCount(err); got != 3 {
		t.Errorf("PartialDropCount = %d, want 3", got)
	}
	if IsRetryable(err) {
		t.Error("a partial drop must not be retried: the accepted events already landed")
	}
	if err.Error() != base.Error() {
		t.Errorf("Error() = %q, want the wrapped error's message %q", err.Error(), base.Error())
	}
	if !errors.Is(err, base) {
		t.Error("PartialDrop must unwrap to the original error")
	}
}

func TestPartialDropIsANoOpWithoutBothArguments(t *testing.T) {
	if PartialDrop(nil, 3) != nil {
		t.Error("PartialDrop(nil, n) must stay nil: there is no error to annotate")
	}
	base := errors.New("vendor rejected the batch")
	if got := PartialDrop(base, 0); got != base {
		t.Errorf("PartialDrop(err, 0) = %v, want the error unchanged", got)
	}
}

func TestPartialDropCountIsZeroForOtherErrors(t *testing.T) {
	if got := PartialDropCount(errors.New("plain")); got != 0 {
		t.Errorf("PartialDropCount = %d, want 0 for an error without the capability", got)
	}
	if got := PartialDropCount(nil); got != 0 {
		t.Errorf("PartialDropCount = %d, want 0 for a nil error", got)
	}
}

func TestIdentifyRunsOncePerProcessWithInstallTraits(t *testing.T) {
	sink := &capabilitySink{}
	s := newCapabilityService(sink)
	for i := 0; i < 3; i++ {
		s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
		s.flush(context.Background())
	}
	if got := sink.identifyCount(); got != 1 {
		t.Fatalf("Identify called %d times, want 1", got)
	}
	traits := sink.identifies[0]
	for _, key := range []string{propAppVersion, propOS, propArch} {
		if _, ok := traits[key]; !ok {
			t.Errorf("install traits missing %q", key)
		}
	}
	if sink.installIDs[0] != "install-1" {
		t.Errorf("identified install %q, want install-1", sink.installIDs[0])
	}
}

func TestIdentifyRetriesAfterAFailure(t *testing.T) {
	sink := &capabilitySink{identifyFail: 1, identifyErr: errors.New("vendor down")}
	s := newCapabilityService(sink)
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	s.flush(context.Background())
	if got := sink.identifyCount(); got != 0 {
		t.Fatalf("Identify recorded %d successes after a failure, want 0", got)
	}
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	s.flush(context.Background())
	if got := sink.identifyCount(); got != 1 {
		t.Errorf("Identify succeeded %d times on the retry, want 1", got)
	}
}

func TestTermsShownToAnAccountThatAlreadyAgreedMarksTheInstallReturning(t *testing.T) {
	cases := []struct {
		name       string
		properties map[string]any
		wantMarked bool
	}{
		{"already agreed", map[string]any{PropAlreadyAgreed: true}, true},
		{"first agreement", map[string]any{PropAlreadyAgreed: false}, false},
		{"not reported", map[string]any{}, false},
		{"not a boolean", map[string]any{PropAlreadyAgreed: "true"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := &capabilitySink{}
			s := newCapabilityService(sink)
			s.Enqueue(t.Context(), SurfaceUI, []RawEvent{{Name: EventActivationTermsShown, Properties: tc.properties}})
			s.flush(t.Context())

			marked := false
			for i, traits := range sink.identifies {
				if traits[traitReturningAccount] == true {
					marked = true
					if sink.installIDs[i] != "install-1" {
						t.Errorf("marked %q returning, want install-1", sink.installIDs[i])
					}
					if len(traits) != 1 {
						t.Errorf("returning mark carried %v, want only %s", traits, traitReturningAccount)
					}
				}
			}
			if marked != tc.wantMarked {
				t.Errorf("install marked returning = %v, want %v", marked, tc.wantMarked)
			}
		})
	}
}

func TestTheReturningMarkNeverLandsOnAnAccountProfile(t *testing.T) {
	sink := &capabilitySink{}
	s, accepted := newDesktopService(sink, nil, nil)
	accepted.accepted.Store(true)

	s.Enqueue(t.Context(), SurfaceUI, []RawEvent{{Name: EventActivationTermsShown, Properties: map[string]any{PropAlreadyAgreed: true}}})
	s.flush(t.Context())

	for i, traits := range sink.identifies {
		if _, ok := traits[traitReturningAccount]; ok {
			t.Errorf("profile %q got %s, want it only on install profiles", sink.installIDs[i], traitReturningAccount)
		}
	}
}

type returningMarkFailSink struct {
	capabilitySink
	returningFails    int
	returningAttempts atomic.Int32
}

func (r *returningMarkFailSink) Identify(ctx context.Context, installID string, traits map[string]any) error {
	if traits[traitReturningAccount] == true {
		r.returningAttempts.Add(1)
		r.mu.Lock()
		fail := r.returningFails > 0
		if fail {
			r.returningFails--
		}
		r.mu.Unlock()
		if fail {
			return errors.New("vendor down")
		}
	}
	return r.capabilitySink.Identify(ctx, installID, traits)
}

func (r *returningMarkFailSink) returningMarks() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	marks := 0
	for _, traits := range r.identifies {
		if traits[traitReturningAccount] == true {
			marks++
		}
	}
	return marks
}

func termsShownAlreadyAgreed() RawEvent {
	return RawEvent{Name: EventActivationTermsShown, Properties: map[string]any{PropAlreadyAgreed: true}}
}

func TestAReturningMarkThatFailsIsRetriedOnTheNextFlush(t *testing.T) {
	sink := &returningMarkFailSink{returningFails: 1}
	s := newCapabilityService(sink)

	s.Enqueue(t.Context(), SurfaceUI, []RawEvent{termsShownAlreadyAgreed()})
	s.flush(t.Context())
	if got := sink.returningMarks(); got != 0 {
		t.Fatalf("install marked returning %d times while the vendor was down, want 0", got)
	}

	for range 3 {
		s.Enqueue(t.Context(), SurfaceUI, []RawEvent{knownEvent()})
		s.flush(t.Context())
	}
	if got := sink.returningMarks(); got != 1 {
		t.Errorf("install marked returning %d times after the vendor recovered, want 1", got)
	}
	if got := int(sink.returningAttempts.Load()); got != 2 {
		t.Errorf("returning mark attempted %d times, want 2 (the failure and one retry)", got)
	}
}

func TestAReturningMarkStopsAfterTheAttemptLimit(t *testing.T) {
	sink := &returningMarkFailSink{returningFails: 1000}
	s := newCapabilityService(sink)

	s.Enqueue(t.Context(), SurfaceUI, []RawEvent{termsShownAlreadyAgreed()})
	const flushes = maxIdentifyAttempts + 3
	for i := 0; i < flushes; i++ {
		if i > 0 {
			s.Enqueue(t.Context(), SurfaceUI, []RawEvent{knownEvent()})
		}
		s.flush(t.Context())
	}
	if got := int(sink.returningAttempts.Load()); got != maxIdentifyAttempts {
		t.Errorf("returning mark attempted %d times over %d flushes, want it capped at %d", got, flushes, maxIdentifyAttempts)
	}
}

func TestAProfileIsIdentifiedAgainWhenItsTraitsChange(t *testing.T) {
	sink := &capabilitySink{}
	s := newCapabilityService(sink)

	s.identifyOnce(t.Context(), "sub-1", "", map[string]any{"$email": "ada@example.com"})
	s.identifyOnce(t.Context(), "sub-1", "", map[string]any{"$email": "ada@example.com"})
	s.identifyOnce(t.Context(), "sub-1", "", map[string]any{"$email": "ada@example.org"})

	if got := sink.identifyCount(); got != 2 {
		t.Fatalf("Identify called %d times, want 2 (once per email)", got)
	}
	if got := sink.identifies[1]["$email"]; got != "ada@example.org" {
		t.Errorf("profile email after the change = %v, want ada@example.org", got)
	}
}

func TestAlternatingProfilesAreEachIdentifiedOnce(t *testing.T) {
	sink := &capabilitySink{}
	s := newCapabilityService(sink)
	account := map[string]any{"$email": "ada@example.com"}

	for range 3 {
		s.identifyOnce(t.Context(), "install-1", "", nil)
		s.identifyOnce(t.Context(), "sub-1", "", account)
	}

	if got := sink.identifyCount(); got != 2 {
		t.Errorf("Identify called %d times across alternating profiles, want 2 (one per profile)", got)
	}
}

func TestNothingIsSentOrIdentifiedWithoutAnIdentity(t *testing.T) {
	sink := &capabilitySink{}
	s := NewService(Options{
		Sink:    sink,
		Consent: allowConsent,
		Logger:  slog.New(slog.DiscardHandler),
		Config:  ServiceConfig{BatchSize: 1000},
	})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	s.flush(context.Background())
	if got := sink.identifyCount(); got != 0 {
		t.Errorf("Identify called %d times with no install marker, want 0", got)
	}
	if got := len(sink.sizes()); got != 0 {
		t.Errorf("sink received %d events with no identity to bind them to, want 0", got)
	}
}

type panicIdentifySink struct {
	capabilitySink
	identifyCalls atomic.Int32
}

func (p *panicIdentifySink) Identify(context.Context, string, map[string]any) error {
	p.identifyCalls.Add(1)
	panic("vendor identify exploded")
}

func TestAPanickingIdentifyStillDeliversTheBatch(t *testing.T) {
	sink := &panicIdentifySink{}
	s := newCapabilityService(sink)
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	s.flush(context.Background())

	if got := sink.identifyCalls.Load(); got != 1 {
		t.Fatalf("Identify called %d times, want 1", got)
	}
	if got := sink.sizes(); len(got) != 1 || got[0] != 1 {
		t.Errorf("sink received batches %v, want one of 1: a panicking Identify must not swallow the events", got)
	}
}

func TestAPanickingIdentifyStopsBeingRetried(t *testing.T) {
	sink := &panicIdentifySink{}
	s := newCapabilityService(sink)
	const flushes = maxIdentifyAttempts + 3
	for i := 0; i < flushes; i++ {
		s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
		s.flush(context.Background())
	}
	if got := int(sink.identifyCalls.Load()); got != maxIdentifyAttempts {
		t.Errorf("Identify attempted %d times over %d flushes, want it capped at %d", got, flushes, maxIdentifyAttempts)
	}
	if got := len(sink.sizes()); got != flushes {
		t.Errorf("sink received %d batches, want all %d", got, flushes)
	}
}

func TestStopDoesNotFlushTheSinkAfterConsentIsWithdrawn(t *testing.T) {
	var granted atomic.Bool
	granted.Store(true)
	sink := &capabilitySink{}
	s := NewService(Options{
		Sink:     sink,
		Consent:  ConsentFunc(func(context.Context) bool { return granted.Load() }),
		Identity: fixedIdentity("install-1"),
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{BufferSize: 1000, BatchSize: 1000, RateCapPerMin: 100000, FlushInterval: time.Hour},
	})
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})

	granted.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if got := sink.flushCount(); got != 0 {
		t.Errorf("sink Flush ran %d times after consent was withdrawn, want 0", got)
	}
	if got := sink.sizes(); len(got) != 0 {
		t.Errorf("sink received %v after consent was withdrawn, want nothing", got)
	}
}

type panicOnFreshConsent struct{}

func (panicOnFreshConsent) Enabled(context.Context) bool      { return true }
func (panicOnFreshConsent) EnabledFresh(context.Context) bool { panic("consent store exploded") }

func TestAPanickingConsentProviderDoesNotEscapeTheSinkFlush(t *testing.T) {
	sink := &capabilitySink{}
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Sink:     sink,
		Consent:  panicOnFreshConsent{},
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{BatchSize: 1000},
	})

	if !s.flushSink(context.Background()) {
		t.Fatal("flushSink reported the push abandoned, want it completed after the recover")
	}
	if got := sink.flushCount(); got != 0 {
		t.Errorf("sink Flush ran %d times behind a panicking consent read, want 0", got)
	}
}

type slowIdentifySink struct {
	capabilitySink
	delay time.Duration
}

func (s *slowIdentifySink) Identify(ctx context.Context, installID string, traits map[string]any) error {
	time.Sleep(s.delay)
	return s.capabilitySink.Identify(ctx, installID, traits)
}

func TestIdentifyAnnouncesOnceUnderConcurrentCallers(t *testing.T) {
	sink := &slowIdentifySink{delay: 20 * time.Millisecond}
	s := newCapabilityService(sink)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.identifyOnce(t.Context(), "install-1", "", nil)
		}()
	}
	wg.Wait()

	if got := sink.identifyCount(); got != 1 {
		t.Errorf("Identify succeeded %d times across concurrent callers, want 1", got)
	}
}

func TestStopFlushesTheSinkAfterTheFinalDrain(t *testing.T) {
	sink := &capabilitySink{}
	s := newCapabilityService(sink)
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if got := sink.flushCount(); got != 1 {
		t.Errorf("sink Flush called %d times during Stop, want 1", got)
	}
	if got := sink.sizes(); len(got) != 1 {
		t.Errorf("sink received %v batches, want the drained one before the flush", got)
	}
}

type withdrawOnFirstChunkSink struct {
	granted *atomic.Bool

	mu        sync.Mutex
	delivered []Event
	calls     int
}

func (w *withdrawOnFirstChunkSink) MaxBatchSize() int { return 1 }

func (w *withdrawOnFirstChunkSink) Send(_ context.Context, events []Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	if w.calls == 1 {
		w.granted.Store(false)
		return errors.New("vendor unavailable")
	}
	w.delivered = append(w.delivered, events...)
	return nil
}

func (w *withdrawOnFirstChunkSink) deliveredCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.delivered)
}

func TestSendStopsAtAChunkBoundaryWhenConsentIsWithdrawn(t *testing.T) {
	var granted atomic.Bool
	granted.Store(true)
	sink := &withdrawOnFirstChunkSink{granted: &granted}
	s := NewService(Options{
		Sink:     sink,
		Consent:  ConsentFunc(func(context.Context) bool { return granted.Load() }),
		Identity: fixedIdentity("install-1"),
		Logger:   slog.New(slog.DiscardHandler),
		Config:   ServiceConfig{BufferSize: 1000, BatchSize: 1000, RateCapPerMin: 100000, FlushInterval: time.Hour},
	})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent(), knownEvent(), knownEvent()})
	s.flush(context.Background())

	if got := sink.deliveredCount(); got != 0 {
		t.Errorf("%d events reached the sink after the withdrawal, want 0", got)
	}
	s.mu.Lock()
	discarded := s.discarded
	s.mu.Unlock()
	if discarded != 3 {
		t.Errorf("discarded = %d, want all 3 accounted for exactly once", discarded)
	}
}
