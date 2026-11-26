// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package entrypoint

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

func onlyEntrypointEvent(t *testing.T, events []productanalytics.Event) productanalytics.Event {
	t.Helper()
	used := eventsNamed(events, productanalytics.EventEntrypointUsed)
	if len(used) != 1 {
		t.Fatalf("entrypoint:used emitted %d times, want 1: %+v", len(used), events)
	}
	return used[0]
}

func TestEntrypointUsedReportsAMachineDrivenRun(t *testing.T) {
	h := newRunAnalyticsHarness(t, withAdapter(&machineIngressAdapter{entrypointType: "event_mesh"}))
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-entrypoint", nil)

	h.publishAgentTask(t, taskID, usageMetadata(
		[]string{productanalytics.ToolCategoryBuiltin},
		nil,
		true,
	))

	events := h.drain(t)
	ev := onlyEntrypointEvent(t, events)
	if got := ev.Properties[productanalytics.PropEntrypointType]; got != "event_mesh" {
		t.Errorf("entrypointType = %v, want %q", got, "event_mesh")
	}
	if got := ev.Properties[productanalytics.PropOutcome]; got != productanalytics.OutcomeSuccess {
		t.Errorf("outcome = %v, want %q", got, productanalytics.OutcomeSuccess)
	}
	if got := ev.Properties[productanalytics.PropInterface]; got != productanalytics.SurfaceServer {
		t.Errorf("interface = %v, want %q", got, productanalytics.SurfaceServer)
	}
}

func TestEntrypointUsedIsAbsentFromAnInteractiveRun(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-interactive", nil)
	h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)

	events := h.drain(t)
	if got := len(eventsNamed(events, productanalytics.EventEntrypointUsed)); got != 0 {
		t.Fatalf("a person's run emitted %d entrypoint:used events, want 0: %+v", got, events)
	}
	if got := len(eventsNamed(events, productanalytics.EventAgentRunCompleted)); got != 1 {
		t.Fatalf("agent:run_completed emitted %d times, want 1", got)
	}
}

func TestEntrypointUsedOutcomeMapping(t *testing.T) {
	cases := []struct {
		name    string
		task    *a2a.Task
		a2aErr  *a2a.Error
		outcome string
	}{
		{"completed is a success", completedTask("t"), nil, productanalytics.OutcomeSuccess},
		{"cancelled is cancelled", &a2a.Task{Status: a2a.TaskStatus{State: a2a.TaskStateCancelled}}, nil, productanalytics.OutcomeCancelled},
		{"an agent error is an error", nil, &a2a.Error{Code: -32000, Message: "boom"}, productanalytics.OutcomeError},
		{"a gateway timeout is a timeout", nil, &a2a.Error{Code: TaskTimeoutErrorCode, Message: "The request timed out."}, productanalytics.OutcomeTimeout},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newRunAnalyticsHarness(t, withAdapter(&machineIngressAdapter{}))
			h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
			taskID := h.submit(t, "my-agent", "task-entrypoint-outcome", nil)
			h.core.fireTaskCompleteHooks(taskID, c.task, c.a2aErr)

			ev := onlyEntrypointEvent(t, h.drain(t))
			if got := ev.Properties[productanalytics.PropOutcome]; got != c.outcome {
				t.Errorf("outcome = %v, want %q", got, c.outcome)
			}
		})
	}
}

func TestEntrypointUsedPropertiesAreOnlyTheTwoEnums(t *testing.T) {
	h := newRunAnalyticsHarness(t, withAdapter(&machineIngressAdapter{entrypointType: "mcp"}))

	ev := h.core.entrypointUsedEvent(completedTask("task-shape"), nil, "2026-08-20T00:00:00.000Z")

	want := map[string]any{
		productanalytics.PropOutcome:        productanalytics.OutcomeSuccess,
		productanalytics.PropEntrypointType: "mcp",
	}
	if !maps.Equal(ev.Properties, want) {
		t.Fatalf("emitted properties = %+v, want %+v", ev.Properties, want)
	}
	if ev.Name != productanalytics.EventEntrypointUsed {
		t.Errorf("event name = %q, want %q", ev.Name, productanalytics.EventEntrypointUsed)
	}
}

func TestEntrypointUsedNamesNothingTheOperatorTyped(t *testing.T) {
	h := newRunAnalyticsHarness(t, withAdapter(&machineIngressAdapter{entrypointType: "webhook"}))
	h.core.agents.AddOrUpdate(testAgentCard("acme-payroll-assistant"))
	taskID := h.submit(t, "acme-payroll-assistant", "task-entrypoint-privacy", map[string]any{
		"webhook_name": "acme-payroll-nightly",
		"path":         "/gw/acme-payroll/invoke",
		"topic":        "acme/payroll/nightly/run",
	})

	metadata := usageMetadata([]string{productanalytics.ToolCategoryConnector}, []string{"sql"}, false)
	metadata["agent_name"] = "acme-payroll-assistant"
	metadata["produced_artifacts"] = []map[string]any{{"filename": "Q3 payroll — Acme.xlsx", "version": 1}}
	h.publishAgentTask(t, taskID, metadata)

	events := h.drain(t)
	ev := onlyEntrypointEvent(t, events)
	rendered, err := json.Marshal(ev.Properties)
	if err != nil {
		t.Fatalf("marshal properties: %v", err)
	}
	for _, secret := range []string{"acme", "payroll", "nightly", "Q3", "xlsx", "/gw/"} {
		if strings.Contains(strings.ToLower(string(rendered)), strings.ToLower(secret)) {
			t.Errorf("%q reached the emitted properties %s", secret, rendered)
		}
	}
	allowed := map[string]bool{
		productanalytics.PropOutcome:        true,
		productanalytics.PropEntrypointType: true,
		productanalytics.PropInterface:      true,
		"appVersion":                        true,
		"os":                                true,
		"arch":                              true,
	}
	for key := range ev.Properties {
		if !allowed[key] {
			t.Errorf("unexpected property %q; the emitted set is closed", key)
		}
	}
}

func TestEntrypointUsedCarriesNoTraceID(t *testing.T) {
	h := newRunAnalyticsHarness(t, withAdapter(&machineIngressAdapter{}))
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-entrypoint-trace", nil)
	info := h.core.tasks.Get(taskID)
	if info == nil || info.TraceID == "" {
		t.Fatal("SubmitTask did not record a trace id")
	}

	ev := h.core.entrypointUsedEvent(completedTask(taskID), nil, "2026-08-20T00:00:00.000Z")
	if got, ok := ev.Properties[productanalytics.PropTraceID]; ok {
		t.Errorf("traceId = %v, want the emitter to omit it", got)
	}
}

func TestEntrypointTypeIsNotForgeableFromMessageMetadata(t *testing.T) {
	h := newRunAnalyticsHarness(t, withAdapter(&machineIngressAdapter{entrypointType: "webhook"}))
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-entrypoint-forged", map[string]any{
		"entrypointType": "slack",
		"gatewayType":    "slack",
		"source":         "mcp",
	})
	h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)

	ev := onlyEntrypointEvent(t, h.drain(t))
	if got := ev.Properties[productanalytics.PropEntrypointType]; got != "webhook" {
		t.Errorf("entrypointType = %v, want the adapter's %q", got, "webhook")
	}
}

func TestEntrypointUsedIsNotForgeableFromMessageMetadata(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-entrypoint-claimed", map[string]any{
		"machineIngress": true,
		"entrypointType": "webhook",
	})
	h.core.fireTaskCompleteHooks(taskID, completedTask(taskID), nil)

	events := h.drain(t)
	if got := len(eventsNamed(events, productanalytics.EventEntrypointUsed)); got != 0 {
		t.Fatalf("a client claimed an entrypoint use via metadata: %+v", events)
	}
}

type unlistedEntrypointAdapter struct {
	recordingAdapter
}

func (a *unlistedEntrypointAdapter) MachineIngress() bool   { return true }
func (a *unlistedEntrypointAdapter) EntrypointType() string { return "acme_gateway" }

func TestEntrypointUsedDropsAnUnlistedType(t *testing.T) {
	h := newRunAnalyticsHarness(t, withAdapter(&unlistedEntrypointAdapter{}))

	ev := h.core.entrypointUsedEvent(completedTask("task-unlisted-type"), nil, "2026-08-20T00:00:00.000Z")

	if got, ok := ev.Properties[productanalytics.PropEntrypointType]; ok {
		t.Errorf("entrypointType = %v, want the unlisted type dropped before it was set", got)
	}
	if got := ev.Properties[productanalytics.PropOutcome]; got != productanalytics.OutcomeSuccess {
		t.Errorf("outcome = %v, want the event to survive without its type", got)
	}
}

func TestEntrypointUsedSkipsSAMsOwnTraffic(t *testing.T) {
	h := newRunAnalyticsHarness(t, withAdapter(&machineIngressAdapter{entrypointType: "event_mesh"}))
	h.core.agents.AddOrUpdate(testAgentCard(platform.BuiltinSentinelName))

	monitor := NewSentinelMonitorService(
		SentinelMonitorConfig{
			Enabled:       true,
			TargetAgent:   platform.BuiltinSentinelName,
			OnTaskFailure: true,
			MaxConcurrent: 1,
			SystemUser:    "sentinel-monitor",
		},
		h.core,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	ctx := context.Background()
	monitor.Start(ctx)
	t.Cleanup(monitor.Stop)

	investigationCtx := gwauth.ContextWithSubject(ctx, monitor.systemIdentity())
	msg := a2a.Message{
		Kind:      "message",
		MessageID: "msg-investigation",
		Role:      a2a.RoleUser,
		Parts:     []a2a.Part{a2a.NewTextPart("Automated alert")},
		Metadata:  map[string]any{"agent_name": platform.BuiltinSentinelName, "sessionBehavior": "RUN_BASED"},
	}
	if err := h.core.SubmitTaskAttributedTo(investigationCtx, platform.BuiltinSentinelName, "task-investigation", msg, stagedCaller(investigationCtx), "alice"); err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	h.core.fireTaskCompleteHooks("task-investigation", completedTask("task-investigation"), nil)

	if events := h.drain(t); len(events) != 0 {
		t.Fatalf("SAM's own traffic was counted as programmatic entrypoint use: %+v", events)
	}
}
