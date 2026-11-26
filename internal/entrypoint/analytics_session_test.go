// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package entrypoint

import (
	"context"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

const declaredSessionID = "019a1c2d-3e4f-7abc-8def-0123456789ab"

func (h *runAnalyticsHarness) submitDeclaring(t *testing.T, agentName, taskID, headerValue string) {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/v1/message:stream", nil)
	if headerValue != "" {
		r.Header.Set(httputil.HeaderClientSession, headerValue)
	}
	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-" + taskID,
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("hello")},
	}
	if err := h.core.SubmitTask(context.Background(), agentName, taskID, msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"}),
		FromClientSurface(httputil.ClientSurface(r)),
		FromClientSession(httputil.ClientSession(r))); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
}

func TestASubmitRecordsTheDeclaredAnalyticsSession(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	h.submitDeclaring(t, "my-agent", "task-declared", declaredSessionID)

	info := h.core.tasks.Get("task-declared")
	if info == nil {
		t.Fatal("the task was not registered at all")
	}
	if info.AnalyticsSessionID != declaredSessionID {
		t.Errorf("recorded analytics session %q, want the declared %q", info.AnalyticsSessionID, declaredSessionID)
	}
	if info.SessionID == declaredSessionID {
		t.Error("the declared analytics session was written to TaskInfo.SessionID, which is the A2A contextID")
	}
}

func TestAnUndeclaredOrMalformedSessionIsRecordedAsAbsent(t *testing.T) {
	for name, header := range map[string]string{
		"none declared":     "",
		"not a uuid":        "session-7",
		"no dashes":         "019a1c2d3e4f7abc8def0123456789ab",
		"braced":            "{019a1c2d-3e4f-7abc-8def-0123456789ab}",
		"trailing text":     declaredSessionID + "x",
		"leading space":     " " + declaredSessionID,
		"oversized":         declaredSessionID + declaredSessionID,
		"empty declaration": " ",
	} {
		t.Run(name, func(t *testing.T) {
			h := newRunAnalyticsHarness(t)
			h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
			h.submitDeclaring(t, "my-agent", "task-"+name, header)

			info := h.core.tasks.Get("task-" + name)
			if info == nil {
				t.Fatal("the task was not registered at all")
			}
			if info.AnalyticsSessionID != "" {
				t.Errorf("a request declaring %q recorded session %q, want none", header, info.AnalyticsSessionID)
			}
		})
	}
}

func TestAnAssertedSessionIsTheShapeEnqueueBinds(t *testing.T) {
	accepted := []string{
		declaredSessionID,
		"00000000-0000-0000-0000-000000000000",
		"019A1C2D-3E4F-7ABC-8DEF-0123456789AB",
	}
	refused := []string{
		"",
		"session-7",
		"019a1c2d3e4f7abc8def0123456789ab",
		"{019a1c2d-3e4f-7abc-8def-0123456789ab}",
		"urn:uuid:019a1c2d-3e4f-7abc-8def-0123456789ab",
		declaredSessionID + "x",
	}

	for _, value := range append(append([]string(nil), accepted...), refused...) {
		t.Run(value, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/v1/message:stream", nil)
			if value != "" {
				r.Header.Set(httputil.HeaderClientSession, value)
			}
			wantAccepted := slices.Contains(accepted, value)
			if got := httputil.ClientSession(r) != ""; got != wantAccepted {
				t.Fatalf("httputil.ClientSession accepted=%v for %q, want %v", got, value, wantAccepted)
			}

			h := newRunAnalyticsHarness(t)
			h.analytics.Enqueue(context.Background(), productanalytics.SurfaceServer, []productanalytics.RawEvent{{
				SessionID: value,
				Name:      productanalytics.EventAgentRunCompleted,
				Properties: map[string]any{
					productanalytics.PropOutcome:   productanalytics.OutcomeSuccess,
					productanalytics.PropAgentType: productanalytics.AgentTypeBuiltin,
				},
			}})
			events := h.drain(t)
			if len(events) != 1 {
				t.Fatalf("delivered %d events, want 1", len(events))
			}
			if bound := events[0].SessionID != ""; bound != wantAccepted {
				t.Errorf("Enqueue bound=%v for %q while the entrypoint accepted=%v; a value one keeps and the other drops loses the session with no symptom",
					bound, value, wantAccepted)
			}
		})
	}
}

func TestARelayedRunDiscardsADeclaredSession(t *testing.T) {
	h := newRunAnalyticsHarness(t, withAdapter(&relayAdapter{surface: httputil.SurfaceSlack}))
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	h.submitDeclaring(t, "my-agent", "task-relayed", declaredSessionID)

	info := h.core.tasks.Get("task-relayed")
	if info == nil {
		t.Fatal("the task was not registered at all")
	}
	if info.AnalyticsSessionID != "" {
		t.Errorf("a relayed run recorded session %q, want the declared one discarded with the surface", info.AnalyticsSessionID)
	}
}
