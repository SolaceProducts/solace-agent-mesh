// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

const rawTraceID = "019a1c2d-3e4f-7abc-8def-0123456789ab"

func tracedRunEvent() RawEvent {
	return RawEvent{
		Name: EventAgentRunCompleted,
		Properties: map[string]any{
			PropOutcome:   OutcomeSuccess,
			PropAgentType: AgentTypeBuiltin,
			PropTraceID:   rawTraceID,
		},
	}
}

func traceOf(t *testing.T, ev Event) string {
	t.Helper()
	value, ok := ev.Properties[PropTraceID].(string)
	if !ok {
		t.Fatalf("event carries no trace id at all: %#v", ev.Properties)
	}
	return value
}

func TestOneTaskEventsShareOneTraceValueWithinAPeriod(t *testing.T) {
	s, _ := newRotatingService(nil)
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{tracedRunEvent(), tracedRunEvent()})

	events := buffered(s)
	if len(events) != 2 {
		t.Fatalf("buffered %d events, want 2", len(events))
	}
	first, second := traceOf(t, events[0]), traceOf(t, events[1])
	if first != second {
		t.Errorf("one task produced %q and %q, so its events no longer reconstruct into one trace", first, second)
	}
	if _, err := uuid.Parse(first); err != nil {
		t.Errorf("bound trace value is not a UUID: %v", err)
	}
}

func TestTwoTasksDoNotCollapseOntoOneTraceValue(t *testing.T) {
	other := tracedRunEvent()
	other.Properties[PropTraceID] = "019a1c2d-3e4f-7abc-8def-ffffffffffff"

	s, _ := newRotatingService(nil)
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{tracedRunEvent(), other})

	events := buffered(s)
	if traceOf(t, events[0]) == traceOf(t, events[1]) {
		t.Error("two tasks collapsed onto one trace value, so per-task correlation merges")
	}
}

func TestARotatedMarkerRotatesTheTraceValue(t *testing.T) {
	s, identity := newRotatingService(nil)
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{tracedRunEvent()})
	before := traceOf(t, buffered(s)[0])

	identity.set("install-b")
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{tracedRunEvent()})
	after := traceOf(t, buffered(s)[0])

	if before == after {
		t.Errorf("trace value %q spans both markers, so the two periods are linkable by it", before)
	}
}

func TestTheRawTraceIDNeverReachesASink(t *testing.T) {
	uiEvent := RawEvent{Name: EventChatFirstResponse, Properties: map[string]any{PropTraceID: rawTraceID}}
	for _, c := range []struct {
		surface string
		raw     RawEvent
	}{
		{SurfaceServer, tracedRunEvent()},
		{SurfaceUI, uiEvent},
	} {
		t.Run(c.surface, func(t *testing.T) {
			s, _ := newRotatingService(nil)
			s.Enqueue(context.Background(), c.surface, []RawEvent{c.raw})
			events := buffered(s)
			if len(events) != 1 {
				t.Fatalf("buffered %d events, want 1", len(events))
			}
			bound := traceOf(t, events[0])
			if bound == rawTraceID {
				t.Error("the raw trace id reached the sink, so a vendor export joins onto this install's logs")
			}
			if strings.Contains(bound, strings.TrimSuffix(rawTraceID, "-0123456789ab")) {
				t.Errorf("the bound value %q quotes the raw id it was derived from", bound)
			}
		})
	}
}

func TestTwoServicesSharingAMarkerAgreeOnTheSameTrace(t *testing.T) {
	a, _ := newRotatingService(nil)
	b, _ := newRotatingService(nil)
	a.Enqueue(context.Background(), SurfaceServer, []RawEvent{tracedRunEvent()})
	b.Enqueue(context.Background(), SurfaceServer, []RawEvent{tracedRunEvent()})
	if traceOf(t, buffered(a)[0]) != traceOf(t, buffered(b)[0]) {
		t.Error("two processes disagree on one task's trace value, so a run reported by both never reconstructs")
	}
}

func TestTheTraceDomainIsSeparateFromTheEventAndSessionDomains(t *testing.T) {
	identity := testIdentity("11111111-1111-4111-8111-111111111111")
	asTrace := boundID(identity, idDomainTrace, clientUUID)
	asEvent := boundID(identity, idDomainEvent, clientUUID)
	asSession := boundID(identity, idDomainSession, clientUUID)
	if asTrace == asEvent || asTrace == asSession {
		t.Errorf("one UUID maps to %q as a trace and as another id", asTrace)
	}
	unkeyed := identity
	unkeyed.BindingKey = nil
	if plain := boundID(unkeyed, idDomainTrace, clientUUID); plain == asTrace {
		t.Error("the binding key does not key the trace mapping, so a holder of the marker and the raw id reproduces it")
	}
}

func TestAnEventWithoutATraceGetsNone(t *testing.T) {
	s, _ := newRotatingService(nil)
	s.Enqueue(context.Background(), SurfaceServer, []RawEvent{serverRunEvent()})
	if value, ok := buffered(s)[0].Properties[PropTraceID]; ok {
		t.Errorf("trace id %v appeared on an event that reported none", value)
	}
}

func TestTheChatResponseJoinsToItsServerRun(t *testing.T) {
	s, _ := newRotatingService(nil)
	ctx := context.Background()
	s.Enqueue(ctx, SurfaceUI, []RawEvent{{
		Name:       EventChatFirstResponse,
		Properties: map[string]any{PropTraceID: rawTraceID},
	}})
	s.Enqueue(ctx, SurfaceServer, []RawEvent{{
		Name: EventAgentRunCompleted,
		Properties: map[string]any{
			PropTraceID:   rawTraceID,
			PropOutcome:   OutcomeSuccess,
			PropAgentType: AgentTypeUserCreated,
		},
	}})

	events := buffered(s)
	if len(events) != 2 {
		t.Fatalf("buffered %d events, want 2", len(events))
	}
	uiTrace, serverTrace := traceOf(t, events[0]), traceOf(t, events[1])
	if uiTrace != serverTrace {
		t.Errorf("the chat response bound to %q and its run to %q, so the surfaces do not join", uiTrace, serverTrace)
	}
	if uiTrace == rawTraceID {
		t.Error("the raw trace reached the sink, so a vendor export joins onto this install's logs")
	}
}

func TestTheDisplayedErrorJoinsToItsServerRun(t *testing.T) {
	s, _ := newRotatingService(nil)
	ctx := context.Background()
	s.Enqueue(ctx, SurfaceUI, []RawEvent{{
		Name: EventErrorDisplayed,
		Properties: map[string]any{
			PropTraceID:       rawTraceID,
			PropErrorCategory: "rate_limit",
			"page":            "/chat",
		},
	}})
	s.Enqueue(ctx, SurfaceServer, []RawEvent{{
		Name: EventAgentRunCompleted,
		Properties: map[string]any{
			PropTraceID:       rawTraceID,
			PropOutcome:       OutcomeError,
			PropAgentType:     AgentTypeUserCreated,
			PropErrorCategory: "rate_limit",
			PropProvider:      "anthropic",
		},
	}})

	events := buffered(s)
	if len(events) != 2 {
		t.Fatalf("buffered %d events, want 2", len(events))
	}
	uiTrace, serverTrace := traceOf(t, events[0]), traceOf(t, events[1])
	if uiTrace != serverTrace {
		t.Errorf("the displayed error bound to %q and its run to %q, so the failure the user saw cannot be attributed to a provider", uiTrace, serverTrace)
	}
	if uiTrace == rawTraceID || serverTrace == rawTraceID {
		t.Error("the raw trace reached the sink, so a vendor export joins onto this install's logs")
	}
}

func TestOtherBrowserEventsCannotCarryATrace(t *testing.T) {
	for _, name := range []string{"page_view", "agent:created", "app:launched"} {
		out, _ := scrub(SurfaceUI, RawEvent{Name: name, Properties: map[string]any{PropTraceID: rawTraceID}})
		if got, present := out[PropTraceID]; present {
			t.Errorf("%s kept a trace id (%v); only chat:first_response and error:displayed may carry one", name, got)
		}
	}
}
