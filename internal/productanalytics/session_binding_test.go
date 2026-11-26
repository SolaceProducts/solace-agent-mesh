// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

const rawBrowserSessionID = "019a1c2d-3e4f-7abc-8def-0123456789ab"

func browserErrorEvent(sessionID string) RawEvent {
	return RawEvent{
		SessionID: sessionID,
		Name:      EventErrorDisplayed,
		Properties: map[string]any{
			PropErrorCategory: "auth_failure",
			PropErrorSource:   ErrorSourceAPI,
		},
	}
}

func runReportEvent(sessionID string) RawEvent {
	return RawEvent{
		SessionID: sessionID,
		Name:      EventAgentRunCompleted,
		Properties: map[string]any{
			PropOutcome:   OutcomeError,
			PropAgentType: AgentTypeUserCreated,
		},
	}
}

func TestABrowserEventAndARunReportShareOneBoundSession(t *testing.T) {
	s, _ := newRotatingService(nil)
	s.Enqueue(context.Background(), SurfaceUI, []RawEvent{browserErrorEvent(rawBrowserSessionID)})
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{runReportEvent(rawBrowserSessionID)})

	events := buffered(s)
	if len(events) != 2 {
		t.Fatalf("buffered %d events, want the browser event and the run report", len(events))
	}
	browser, run := events[0], events[1]
	if browser.Name != EventErrorDisplayed || run.Name != EventAgentRunCompleted {
		t.Fatalf("buffered %q then %q, want %q then %q", browser.Name, run.Name, EventErrorDisplayed, EventAgentRunCompleted)
	}
	if browser.SessionID == "" {
		t.Fatal("the browser event carries no bound session, so there is nothing to join to")
	}
	if browser.SessionID != run.SessionID {
		t.Errorf("browser event bound its session to %q and the run report to %q; one session must be one value or the two cannot be joined at all",
			browser.SessionID, run.SessionID)
	}
	if browser.SessionID == rawBrowserSessionID {
		t.Errorf("session reached a sink as the raw id %q; a value the client minted is a correlation key the binding exists to replace", rawBrowserSessionID)
	}
	if _, err := uuid.Parse(browser.SessionID); err != nil {
		t.Errorf("bound session value is not a UUID: %v", err)
	}
}

func TestTwoSessionsDoNotCollapseOntoOneBoundValue(t *testing.T) {
	const otherSession = "019a1c2d-3e4f-7abc-8def-ffffffffffff"
	s, _ := newRotatingService(nil)
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{runReportEvent(rawBrowserSessionID), runReportEvent(otherSession)})

	events := buffered(s)
	if events[0].SessionID == events[1].SessionID {
		t.Error("two sessions collapsed onto one bound value, so per-session attribution merges")
	}
}

func TestARotatedMarkerRotatesAServerEventSession(t *testing.T) {
	s, identity := newRotatingService(nil)
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{runReportEvent(rawBrowserSessionID)})
	before := buffered(s)[0].SessionID

	identity.set("install-b")
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{runReportEvent(rawBrowserSessionID)})
	after := buffered(s)[0].SessionID

	if before == "" || after == "" {
		t.Fatalf("a server event lost its session across the rotation: %q then %q", before, after)
	}
	if before == after {
		t.Errorf("the same session bound to %q either side of a marker rotation, joining two consented periods", before)
	}
}

func TestAServerEventWithNoSessionCarriesNone(t *testing.T) {
	for name, sessionID := range map[string]string{
		"none declared":  "",
		"not a uuid":     "session-7",
		"nearly a uuid":  "019a1c2d3e4f7abc8def0123456789ab",
		"uuid plus text": rawBrowserSessionID + "x",
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := newRotatingService(nil)
			s.Enqueue(context.Background(), SurfaceServer, []RawEvent{runReportEvent(sessionID)})

			events := buffered(s)
			if len(events) != 1 {
				t.Fatalf("buffered %d events, want the run report", len(events))
			}
			if got := events[0].SessionID; got != "" {
				t.Errorf("a run report with no usable session (%q) reported session %q, want none", sessionID, got)
			}
		})
	}
}
