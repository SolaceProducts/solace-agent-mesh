// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package entrypoint

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/httputil"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

func (h *runAnalyticsHarness) submitAs(t *testing.T, agentName, taskID, surface string) {
	t.Helper()
	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-" + taskID,
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("hello")},
	}
	if err := h.core.SubmitTask(context.Background(), agentName, taskID, msg, auth.NewTestSubject(auth.SubjectFields{UserID: "user-1"}), FromClientSurface(surface)); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
}

func TestRunReportsTheClientThatSubmittedIt(t *testing.T) {
	for _, surface := range []string{httputil.SurfaceWebUI, httputil.SurfaceCLI, httputil.SurfaceAPI} {
		t.Run(surface, func(t *testing.T) {
			h := newRunAnalyticsHarness(t)
			h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
			h.submitAs(t, "my-agent", "task-"+surface, surface)
			h.core.fireTaskCompleteHooks("task-"+surface, completedTask("task-"+surface), nil)

			events := eventsNamed(h.drain(t), productanalytics.EventAgentRunCompleted)
			if len(events) != 1 {
				t.Fatalf("agent:run_completed emitted %d times, want 1", len(events))
			}
			if got := events[0].Properties[productanalytics.PropSubmittedFrom]; got != surface {
				t.Errorf("submittedFrom = %v, want %q", got, surface)
			}
		})
	}
}

func TestAnUnrecognizedSurfaceIsDroppedByTheEmitter(t *testing.T) {
	info := &TaskInfo{TaskOrigin: TaskOrigin{Surface: "acme-internal-portal", AgentType: productanalytics.AgentTypeUserCreated}}
	props := runReportProperties(info, completedTask("task-forged"), nil, usageFacts{})
	if got, present := props[productanalytics.PropSubmittedFrom]; present {
		t.Errorf("submittedFrom = %v, want an unlisted surface dropped before the event was built", got)
	}

	info.Surface = httputil.SurfaceCLI
	props = runReportProperties(info, completedTask("task-cli"), nil, usageFacts{})
	if got := props[productanalytics.PropSubmittedFrom]; got != httputil.SurfaceCLI {
		t.Errorf("submittedFrom = %v, want %q kept", got, httputil.SurfaceCLI)
	}
}

func TestAnUndeclaredRequestReadsAsTheAPI(t *testing.T) {
	for header, want := range map[string]string{
		"":                     httputil.SurfaceAPI,
		"acme-internal-portal": httputil.SurfaceAPI,
		httputil.SurfaceWebUI:  httputil.SurfaceWebUI,
		httputil.SurfaceCLI:    httputil.SurfaceCLI,
	} {
		r := httptest.NewRequest("POST", "/api/v1/message:stream", nil)
		if header != "" {
			r.Header.Set(httputil.HeaderClientSurface, header)
		}
		if got := httputil.ClientSurface(r); got != want {
			t.Errorf("a request declaring %q read as %q, want %q", header, got, want)
		}
	}
}
