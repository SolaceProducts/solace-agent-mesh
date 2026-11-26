// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"
)

func budgetOf(s *Service, surface string, name EventName) *surfaceBudget {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.budgetLocked(surface, Event{Name: name}, time.Now())
}

func machineFactBudget(s *Service, surface string) *surfaceBudget {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.budgetLocked(surface, Event{
		Name:       EventToolUsed,
		Properties: map[string]any{PropRunOrigin: RunOriginMachineIngress},
	}, time.Now())
}

func buffered(s *Service) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, 0, s.bufferedLocked())
	for _, budget := range s.budgets {
		out = append(out, budget.buf...)
	}
	slices.SortFunc(out, func(a, b Event) int { return cmp.Compare(a.Seq, b.Seq) })
	return out
}

func bufferedOn(s *Service, surface string, name EventName) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.budgetLocked(surface, Event{Name: name}, time.Now()).buf)
}

func droppedTotal(s *Service) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total int64
	for _, budget := range s.budgets {
		total += budget.dropped
	}
	return total
}

func droppedOn(s *Service, surface string, name EventName) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.budgetLocked(surface, Event{Name: name}, time.Now()).dropped
}

func attrString(r slog.Record, key string) (string, bool) {
	var v string
	var found bool
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v, found = a.Value.String(), true
			return false
		}
		return true
	})
	return v, found
}

func TestOneSurfaceCannotSpendAnothersRateBudget(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BufferSize: 10000, BatchSize: 1_000_000, RateCapPerMin: 600}})

	flood := make([]RawEvent, 5000)
	for i := range flood {
		flood[i] = knownEvent()
	}
	s.Enqueue(context.Background(), SurfaceUI, flood)
	if got := bufferedOn(s, SurfaceUI, EventPageView); got != 600 {
		t.Fatalf("web UI accepted %d of a 5000-event flood, want its own cap of 600", got)
	}
	if got := droppedOn(s, SurfaceUI, EventPageView); got != 4400 {
		t.Fatalf("web UI dropped %d, want 4400 charged to the surface that overran", got)
	}

	runs := make([]RawEvent, 100)
	for i := range runs {
		runs[i] = serverRunEvent()
	}
	s.Enqueue(context.Background(), SurfaceServer, runs)
	if got := bufferedOn(s, SurfaceServer, EventAgentRunCompleted); got != 100 {
		t.Fatalf("server surface buffered %d run reports after the flood, want all 100", got)
	}
	if got := droppedOn(s, SurfaceServer, EventAgentRunCompleted); got != 0 {
		t.Errorf("server surface dropped %d, want 0: the flood was on another surface", got)
	}
}

func TestOneSurfaceCannotEvictAnothersBufferedEvents(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BufferSize: 4, BatchSize: 1_000_000, RateCapPerMin: 1_000_000}})

	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{serverRunEvent(), serverRunEvent()})
	for i := 0; i < 50; i++ {
		s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	}
	if got := bufferedOn(s, SurfaceServer, EventAgentRunCompleted); got != 2 {
		t.Fatalf("server surface holds %d run reports after a web UI overflow, want both", got)
	}
	if got := droppedOn(s, SurfaceServer, EventAgentRunCompleted); got != 0 {
		t.Errorf("server surface dropped %d to another surface's overflow, want 0", got)
	}
	if got := bufferedOn(s, SurfaceUI, EventPageView); got != 4 {
		t.Errorf("web UI holds %d, want its own buffer size of 4", got)
	}
}

func TestReportDropsNamesEachSurface(t *testing.T) {
	capture := &warnCapture{}
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Consent:  allowConsent,
		Logger:   slog.New(capture),
		Config:   ServiceConfig{BufferSize: 1000, BatchSize: 1_000_000, RateCapPerMin: 1},
	})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent(), knownEvent(), knownEvent()})
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{serverRunEvent(), serverRunEvent()})

	s.reportDrops()
	drops := capture.drops()
	if len(drops) != 2 {
		t.Fatalf("emitted %d drop lines, want one per surface that lost events", len(drops))
	}
	want := map[string]int64{SurfaceServer: 1, SurfaceUI: 2}
	for _, record := range drops {
		surface, ok := attrString(record, "surface")
		if !ok {
			t.Fatalf("drop line carries no surface: %v", record)
		}
		delta, _ := attrInt64(record, "dropped")
		if delta != want[surface] {
			t.Errorf("surface %q: dropped delta = %d, want %d", surface, delta, want[surface])
		}
		delete(want, surface)
	}
	if len(want) != 0 {
		t.Errorf("no drop line for %v", want)
	}
}

func TestFlushDrainsEverySurfaceInAdmissionOrder(t *testing.T) {
	sink := &fakeSink{}
	s := NewService(Options{
		Identity: fixedIdentity("install-1"),
		Consent:  allowConsent,
		Sink:     sink,
		Config:   ServiceConfig{BufferSize: 100, BatchSize: 1_000_000, RateCapPerMin: 1_000_000},
	})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{serverRunEvent()})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})

	s.flush(context.Background())
	batch := sink.all()
	if len(batch) != 3 {
		t.Fatalf("delivered %d events, want all 3", len(batch))
	}
	for i, ev := range batch {
		if ev.Seq != uint64(i+1) {
			t.Errorf("event %d has seq %d, want %d: the batch must stay in admission order", i, ev.Seq, i+1)
		}
	}
}

func TestBatchThresholdCountsEverySurface(t *testing.T) {
	s := NewService(Options{Identity: fixedIdentity("install-1"), Consent: allowConsent, Config: ServiceConfig{BufferSize: 100, BatchSize: 2, RateCapPerMin: 1_000_000}})
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})
	select {
	case <-s.flushSignal:
		t.Fatal("one event of a two-event batch signalled a flush")
	default:
	}
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{serverRunEvent()})
	select {
	case <-s.flushSignal:
	default:
		t.Error("two events across two surfaces did not reach the batch threshold")
	}
}
