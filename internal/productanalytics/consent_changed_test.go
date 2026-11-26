// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"testing"
	"time"
)

type consentPipeline struct {
	store *fakeStateStore
	state *PersistedState
	svc   *Service
	sink  *fakeSink
}

func newConsentPipeline(t *testing.T, initial ConsentState) *consentPipeline {
	t.Helper()
	store := &fakeStateStore{consent: string(initial)}
	if initial == ConsentGranted {
		secret, err := newBindingSecret()
		if err != nil {
			t.Fatal(err)
		}
		store.installID = "11111111-1111-1111-1111-111111111111"
		store.bindingSecret = secret
	}
	state := NewPersistedState(store, nil)
	sink := &fakeSink{}
	svc := NewService(Options{
		Identity: state,
		Consent:  state,
		Sink:     sink,
		Config:   ServiceConfig{BatchSize: 1000, RateCapPerMin: 100000},
	})
	ReportConsentChanges(state, svc)
	return &consentPipeline{store: store, state: state, svc: svc, sink: sink}
}

func (p *consentPipeline) readCurrentDecision(t *testing.T) {
	t.Helper()
	if _, err := p.state.ConsentWithError(context.Background()); err != nil {
		t.Fatalf("read current decision: %v", err)
	}
}

func (p *consentPipeline) changes(t *testing.T) [][2]string {
	t.Helper()
	p.svc.flush(context.Background())
	var out [][2]string
	for _, ev := range p.sink.all() {
		if ev.Name != EventConsentChanged {
			continue
		}
		previous, _ := ev.Properties[PropConsentPrevious].(string)
		next, _ := ev.Properties[PropConsentNew].(string)
		out = append(out, [2]string{previous, next})
	}
	return out
}

func TestAWithdrawalDeliversOnlyItsOwnNotice(t *testing.T) {
	p := newConsentPipeline(t, ConsentGranted)
	p.readCurrentDecision(t)

	p.svc.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})

	if err := p.state.SetConsent(context.Background(), ConsentDenied); err != nil {
		t.Fatal(err)
	}

	got := p.changes(t)
	want := [2]string{ConsentStateGranted, ConsentStateWithdrawn}
	if len(got) != 1 || got[0] != want {
		t.Errorf("reported %v, want exactly one %v", got, want)
	}
	for _, ev := range p.sink.all() {
		if ev.Name == knownEvent().Name {
			t.Errorf("event %q reached the sink alongside the withdrawal notice", ev.Name)
		}
	}
}

func TestNothingElseIsReportedAfterAWithdrawal(t *testing.T) {
	p := newConsentPipeline(t, ConsentGranted)
	if err := p.state.SetConsent(context.Background(), ConsentDenied); err != nil {
		t.Fatal(err)
	}

	p.svc.Enqueue(context.Background(), SurfaceUI, []RawEvent{knownEvent()})

	p.svc.flush(context.Background())
	for _, ev := range p.sink.all() {
		if ev.Name != EventConsentChanged {
			t.Errorf("event %q reached the sink after consent was withdrawn", ev.Name)
		}
	}
}

func TestAFirstGrantIsReportedWithNoPriorDecision(t *testing.T) {
	p := newConsentPipeline(t, ConsentUnset)
	p.readCurrentDecision(t)

	if err := p.state.SetConsent(context.Background(), ConsentGranted); err != nil {
		t.Fatal(err)
	}

	got := p.changes(t)
	want := [2]string{ConsentStateUnset, ConsentStateGranted}
	if len(got) != 1 || got[0] != want {
		t.Errorf("reported %v, want exactly one %v", got, want)
	}
}

func TestARegrantCarriesTheWithdrawalItFollows(t *testing.T) {
	p := newConsentPipeline(t, ConsentGranted)
	p.readCurrentDecision(t)
	if err := p.state.SetConsent(context.Background(), ConsentDenied); err != nil {
		t.Fatal(err)
	}

	if err := p.state.SetConsent(context.Background(), ConsentGranted); err != nil {
		t.Fatal(err)
	}

	got := p.changes(t)
	want := [][2]string{
		{ConsentStateGranted, ConsentStateWithdrawn},
		{ConsentStateWithdrawn, ConsentStateGranted},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("reported %v, want %v", got, want)
	}
}

func TestARepeatedGrantIsNotASecondChange(t *testing.T) {
	p := newConsentPipeline(t, ConsentGranted)
	p.readCurrentDecision(t)

	if err := p.state.SetConsent(context.Background(), ConsentGranted); err != nil {
		t.Fatal(err)
	}

	if got := p.changes(t); len(got) != 0 {
		t.Errorf("re-affirming a grant reported %v, want nothing", got)
	}
}

func TestSetConsentWorksWithNoReporterWired(t *testing.T) {
	store := &fakeStateStore{consent: string(ConsentGranted), installID: "11111111-1111-1111-1111-111111111111"}
	state := NewPersistedState(store, nil)

	if err := state.SetConsent(context.Background(), ConsentDenied); err != nil {
		t.Fatal(err)
	}

	if store.consent != string(ConsentDenied) {
		t.Errorf("stored consent = %q, want %q", store.consent, ConsentDenied)
	}
}

func TestReportConsentChangesIgnoresAMissingSide(t *testing.T) {
	ReportConsentChanges(nil, NewService(Options{Consent: allowConsent}))
	ReportConsentChanges(NewPersistedState(&fakeStateStore{}, nil), nil)
	ReportConsentChanges(nil, nil)
}

func TestAGrantFromAProcessThatNeverReadTheDecisionIsNotReported(t *testing.T) {
	p := newConsentPipeline(t, ConsentUnset)

	if err := p.state.SetConsent(context.Background(), ConsentGranted); err != nil {
		t.Fatal(err)
	}

	if got := p.changes(t); len(got) != 0 {
		t.Errorf("reported %v from a cold cache, want nothing", got)
	}
}

func TestAGrantAfterAStaleReadIsNotReported(t *testing.T) {
	p := newConsentPipeline(t, ConsentUnset)
	readAt := time.Now()
	p.state.now = func() time.Time { return readAt }
	p.readCurrentDecision(t)

	p.store.consent = string(ConsentGranted)
	p.state.now = func() time.Time { return readAt.Add(cacheTTL) }

	if err := p.state.SetConsent(t.Context(), ConsentGranted); err != nil {
		t.Fatal(err)
	}

	if got := p.changes(t); len(got) != 0 {
		t.Errorf("reported %v from a stale read, want nothing", got)
	}
}
