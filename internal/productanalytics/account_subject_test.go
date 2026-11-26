// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	testAccountSubject = "auth0|5f1c2d3e4a"
	testAccountEmail   = "Ada@Example.COM"
	testTraceID        = "0190f3c4-7b1a-7c2d-8e3f-4a5b6c7d8e9f"
)

type acceptance struct{ accepted atomic.Bool }

func (a *acceptance) source() AccountSource {
	return func() (Account, bool) {
		if !a.accepted.Load() {
			return Account{}, false
		}
		return Account{Subject: testAccountSubject, Email: testAccountEmail}, true
	}
}

func newDesktopService(sink Sink, consent ConsentProvider, identity IdentityProvider) (*Service, *acceptance) {
	s := newCapabilityService(sink)
	if consent != nil {
		s.consent = consent
	}
	if identity != nil {
		s.identity = identity
	}
	a := &acceptance{}
	s.FileUnderAccounts(a.source())
	return s, a
}

func pageView() RawEvent {
	return RawEvent{EventID: clientUUID, SessionID: clientUUID, Name: EventPageView, Properties: map[string]any{"path": "/agents"}}
}

func serverRun() RawEvent {
	return RawEvent{Name: EventAgentRunCompleted, Properties: map[string]any{PropOutcome: OutcomeSuccess, PropTraceID: testTraceID}}
}

func TestEventsAreFiledUnderTheInstallUntilTheLicenseIsAccepted(t *testing.T) {
	sink := &capabilitySink{}
	s, _ := newDesktopService(sink, nil, nil)

	s.Enqueue(t.Context(), SurfaceUI, []RawEvent{pageView()})
	s.flush(t.Context())

	ev := onlyEvent(t, sink)
	if ev.DistinctID != "install-1" {
		t.Errorf("DistinctID = %q, want the install marker", ev.DistinctID)
	}
	if got := ev.Properties[PropIdentity]; got != IdentityInstall {
		t.Errorf("identity = %v, want %q", got, IdentityInstall)
	}
	if _, ok := ev.Properties[PropEmailDomain]; ok {
		t.Error("an anonymous event carries an email domain")
	}
}

func TestEventsAreFiledUnderTheAccountOnceTheLicenseIsAccepted(t *testing.T) {
	sink := &capabilitySink{}
	s, a := newDesktopService(sink, nil, nil)
	a.accepted.Store(true)

	s.Enqueue(t.Context(), SurfaceUI, []RawEvent{pageView()})
	s.Enqueue(t.Context(), SurfaceServer, []RawEvent{serverRun()})
	s.flush(t.Context())

	for _, ev := range allEvents(sink) {
		if ev.DistinctID != testAccountSubject {
			t.Errorf("%s: DistinctID = %q, want the account subject", ev.Name, ev.DistinctID)
		}
		if got := ev.Properties[PropIdentity]; got != IdentityAccount {
			t.Errorf("%s: identity = %v, want %q", ev.Name, got, IdentityAccount)
		}
		if got := ev.Properties[PropEmailDomain]; got != "example.com" {
			t.Errorf("%s: emailDomain = %v, want example.com", ev.Name, got)
		}
		if leaked := carrying(ev, "install-1", testAccountEmail, strings.ToLower(testAccountEmail)); leaked != "" {
			t.Errorf("%s: an account event carries %s", ev.Name, leaked)
		}
	}
}

func TestNothingLinksTheTwoSidesOfTheAcceptance(t *testing.T) {
	sink := &capabilitySink{}
	s, a := newDesktopService(sink, nil, nil)

	s.Enqueue(t.Context(), SurfaceUI, []RawEvent{pageView()})
	s.Enqueue(t.Context(), SurfaceServer, []RawEvent{serverRun()})
	a.accepted.Store(true)
	s.Enqueue(t.Context(), SurfaceUI, []RawEvent{pageView()})
	s.Enqueue(t.Context(), SurfaceServer, []RawEvent{serverRun()})
	s.flush(t.Context())

	events := allEvents(sink)
	if len(events) != 4 {
		t.Fatalf("delivered %d events, want 4", len(events))
	}
	before, after := events[:2], events[2:]
	if before[0].SessionID == after[0].SessionID {
		t.Error("one browser session arrives as the same bound id on both sides")
	}
	if before[0].EventID == after[0].EventID {
		t.Error("one client event id arrives as the same bound id on both sides")
	}
	if before[1].Properties[PropTraceID] == after[1].Properties[PropTraceID] {
		t.Error("one trace arrives as the same bound id on both sides")
	}
	if before[0].PublisherID == after[0].PublisherID {
		t.Error("one publisher id spans both sides")
	}
	if after[0].Seq != 1 {
		t.Errorf("the account side's sequence starts at %d, want 1", after[0].Seq)
	}
}

func TestEventsQueuedBeforeTheAcceptanceGoOutAnonymous(t *testing.T) {
	sink := &capabilitySink{}
	s, a := newDesktopService(sink, nil, nil)

	s.Enqueue(t.Context(), SurfaceUI, []RawEvent{pageView()})
	a.accepted.Store(true)
	s.Enqueue(t.Context(), SurfaceUI, []RawEvent{pageView()})
	s.flush(t.Context())

	events := allEvents(sink)
	if len(events) != 2 {
		t.Fatalf("delivered %d events, want both sides of the acceptance", len(events))
	}
	if events[0].DistinctID != "install-1" || events[0].Properties[PropIdentity] != IdentityInstall {
		t.Errorf("the event queued before the acceptance went out as %q (%v)", events[0].DistinctID, events[0].Properties[PropIdentity])
	}
	if events[1].DistinctID != testAccountSubject {
		t.Errorf("the event after the acceptance went out as %q", events[1].DistinctID)
	}
}

func TestEachSideGetsItsOwnProfileAndOnlyTheAccountsHasTheEmail(t *testing.T) {
	sink := &capabilitySink{}
	s, a := newDesktopService(sink, nil, nil)

	s.Enqueue(t.Context(), SurfaceUI, []RawEvent{pageView()})
	a.accepted.Store(true)
	s.Enqueue(t.Context(), SurfaceUI, []RawEvent{pageView()})
	s.flush(t.Context())

	if fmt.Sprint(sink.installIDs) != fmt.Sprint([]string{"install-1", testAccountSubject}) {
		t.Fatalf("profiles written for %v, want the install's then the account's", sink.installIDs)
	}
	install, account := sink.identifies[0], sink.identifies[1]
	for key, value := range installTraits() {
		if account[key] != value {
			t.Errorf("the account's profile has %s = %v, want the install's %v", key, account[key], value)
		}
	}
	if _, ok := install[traitEmail]; ok {
		t.Error("the install's profile carries an email")
	}
	if account["$email"] != testAccountEmail || account[PropEmailDomain] != "example.com" {
		t.Errorf("the account's profile has $email %v and domain %v", account["$email"], account[PropEmailDomain])
	}
}

func TestWithdrawingConsentStopsTheAccountsEventsAndRegrantingResumesThem(t *testing.T) {
	var granted atomic.Bool
	granted.Store(true)
	consent := ConsentFunc(func(context.Context) bool { return granted.Load() })
	var marker atomic.Value
	marker.Store("install-1")
	identity := IdentityFunc(func(context.Context) Identity {
		if !granted.Load() {
			return Identity{}
		}
		return testIdentity(marker.Load().(string))
	})
	sink := &capabilitySink{}
	s, a := newDesktopService(sink, consent, identity)
	a.accepted.Store(true)

	granted.Store(false)
	marker.Store("install-2")
	s.Enqueue(t.Context(), SurfaceUI, []RawEvent{pageView()})
	if n := len(buffered(s)); n != 0 {
		t.Fatalf("withdrawn consent buffered %d account events", n)
	}

	granted.Store(true)
	s.Enqueue(t.Context(), SurfaceUI, []RawEvent{pageView()})
	s.flush(t.Context())
	ev := onlyEvent(t, sink)
	if ev.DistinctID != testAccountSubject {
		t.Errorf("after a re-grant the event went out as %q, want the same account", ev.DistinctID)
	}
}

func TestAnAccountEventBufferedBeforeAWithdrawalIsNotSentAfterAQuickRegrant(t *testing.T) {
	var granted atomic.Bool
	granted.Store(true)
	consent := ConsentFunc(func(context.Context) bool { return granted.Load() })
	var marker atomic.Value
	marker.Store("install-1")
	identity := IdentityFunc(func(context.Context) Identity {
		if !granted.Load() {
			return Identity{}
		}
		return testIdentity(marker.Load().(string))
	})
	sink := &capabilitySink{}
	s, a := newDesktopService(sink, consent, identity)
	a.accepted.Store(true)

	beforeWithdrawal := pageView()
	beforeWithdrawal.Properties = map[string]any{"path": "/before"}
	s.Enqueue(t.Context(), SurfaceUI, []RawEvent{beforeWithdrawal})

	granted.Store(false)
	marker.Store("install-2")
	granted.Store(true)

	s.Enqueue(t.Context(), SurfaceUI, []RawEvent{pageView()})
	s.flush(t.Context())

	if got := onlyEvent(t, sink).Properties["path"]; got != "/agents" {
		t.Errorf("delivered the page view at %v, want only the one collected after the re-grant", got)
	}
}

func TestEmailDomain(t *testing.T) {
	for email, want := range map[string]string{
		"ada@Example.COM":  "example.com",
		"a@b@corp.example": "corp.example",
		"no-at-sign":       "",
		"trailing@":        "",
		"":                 "",
	} {
		if got := emailDomain(email); got != want {
			t.Errorf("emailDomain(%q) = %q, want %q", email, got, want)
		}
	}
}

func allEvents(sink *capabilitySink) []Event {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	var events []Event
	for _, batch := range sink.batches {
		events = append(events, batch...)
	}
	return events
}

func onlyEvent(t *testing.T, sink *capabilitySink) Event {
	t.Helper()
	events := allEvents(sink)
	if len(events) != 1 {
		t.Fatalf("delivered %d events, want 1", len(events))
	}
	return events[0]
}

func carrying(ev Event, values ...string) string {
	fields := []string{ev.DistinctID, ev.EventID, ev.SessionID, ev.PublisherID}
	for _, value := range ev.Properties {
		fields = append(fields, fmt.Sprint(value))
	}
	for _, want := range values {
		for _, field := range fields {
			if strings.Contains(field, want) {
				return want
			}
		}
	}
	return ""
}
