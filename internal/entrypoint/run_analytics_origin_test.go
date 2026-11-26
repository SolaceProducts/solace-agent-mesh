// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package entrypoint

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	gwauth "github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

func rawNames(events []productanalytics.RawEvent) []string {
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Name)
	}
	slices.Sort(out)
	return out
}

func rawNamed(events []productanalytics.RawEvent, name string) []productanalytics.RawEvent {
	var out []productanalytics.RawEvent
	for _, ev := range events {
		if ev.Name == name {
			out = append(out, ev)
		}
	}
	return out
}

func factfulTask(taskID string) *a2a.Task {
	task := completedTask(taskID)
	task.Metadata = map[string]any{
		a2a.MetadataToolCategories:      []string{productanalytics.ToolCategoryConnector},
		a2a.MetadataConnectorCategories: []string{"sql"},
		a2a.MetadataPeerDelegated:       true,
	}
	return task
}

func TestMachineIngressReportsWhatItRanWith(t *testing.T) {
	h := newRunAnalyticsHarness(t, withAdapter(&machineIngressAdapter{entrypointType: "webhook"}))
	info := &TaskInfo{
		TraceID: "trace-webhook",
		TaskOrigin: TaskOrigin{
			Trigger:        a2a.TriggerInteractive,
			MachineIngress: true,
			AgentType:      productanalytics.AgentTypeUserCreated,
		},
	}

	events := h.core.terminalTaskEvents(info, factfulTask("task-webhook"), nil)

	want := []string{
		productanalytics.EventAgentPeerDelegated,
		productanalytics.EventEntrypointUsed,
		productanalytics.EventToolUsed,
	}
	if got := rawNames(events); !slices.Equal(got, want) {
		t.Fatalf("a webhook-driven run reported %v, want %v", got, want)
	}
	for _, ev := range events {
		if ev.Name == productanalytics.EventEntrypointUsed {
			continue
		}
		if got := ev.Properties[productanalytics.PropRunOrigin]; got != productanalytics.RunOriginMachineIngress {
			t.Errorf("%s carries runOrigin %v, want %q so a query can separate it from a person's run",
				ev.Name, got, productanalytics.RunOriginMachineIngress)
		}
	}
}

func TestMachineIngressReportsEveryToolCategory(t *testing.T) {
	h := newRunAnalyticsHarness(t, withAdapter(&machineIngressAdapter{entrypointType: "mcp"}))
	task := completedTask("task-every-category")
	task.Metadata = map[string]any{a2a.MetadataToolCategories: productanalytics.ToolCategories}
	info := &TaskInfo{
		TaskOrigin: TaskOrigin{
			Trigger:        a2a.TriggerInteractive,
			MachineIngress: true,
			AgentType:      productanalytics.AgentTypeUserCreated,
		},
	}

	events := h.core.terminalTaskEvents(info, task, nil)

	reported := make([]string, 0, len(productanalytics.ToolCategories))
	for _, ev := range rawNamed(events, productanalytics.EventToolUsed) {
		category, _ := ev.Properties[productanalytics.PropToolCategory].(string)
		reported = append(reported, category)
		if got := ev.Properties[productanalytics.PropRunOrigin]; got != productanalytics.RunOriginMachineIngress {
			t.Errorf("tool:used for %q carries runOrigin %v, want %q", category, got, productanalytics.RunOriginMachineIngress)
		}
	}
	slices.Sort(reported)
	want := slices.Clone(productanalytics.ToolCategories)
	slices.Sort(want)
	if !slices.Equal(reported, want) {
		t.Errorf("a webhook-driven run reported categories %v, want every one the catalog approves %v", reported, want)
	}
}

func TestScheduledRunReportsWhatItRanWith(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	info := &TaskInfo{
		TraceID: "trace-scheduled",
		TaskOrigin: TaskOrigin{
			Trigger:   a2a.TriggerScheduled,
			AgentType: productanalytics.AgentTypeUserCreated,
		},
	}

	events := h.core.terminalTaskEvents(info, factfulTask("task-scheduled"), nil)

	want := []string{productanalytics.EventAgentPeerDelegated, productanalytics.EventToolUsed}
	if got := rawNames(events); !slices.Equal(got, want) {
		t.Fatalf("a scheduled run reported %v, want %v", got, want)
	}
	for _, ev := range events {
		if got := ev.Properties[productanalytics.PropRunOrigin]; got != productanalytics.RunOriginScheduled {
			t.Errorf("%s carries runOrigin %v, want %q", ev.Name, got, productanalytics.RunOriginScheduled)
		}
	}
}

func TestAutomatedRunsStillReportNoRunCompleted(t *testing.T) {
	h := newRunAnalyticsHarness(t, withAdapter(&machineIngressAdapter{entrypointType: "webhook"}))
	cases := []struct {
		name   string
		origin TaskOrigin
	}{
		{"machine ingress", TaskOrigin{Trigger: a2a.TriggerInteractive, MachineIngress: true, AgentType: productanalytics.AgentTypeUserCreated}},
		{"a scheduled task", TaskOrigin{Trigger: a2a.TriggerScheduled, AgentType: productanalytics.AgentTypeUserCreated}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			events := h.core.terminalTaskEvents(&TaskInfo{TaskOrigin: c.origin}, factfulTask("task-1"), nil)
			if got := rawNamed(events, productanalytics.EventAgentRunCompleted); len(got) != 0 {
				t.Errorf("%s produced %d run reports, want none: reportableRun must stay as narrow as it was", c.name, len(got))
			}
		})
	}
}

func TestAnInteractiveRunsFactsCarryTheirOrigin(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	info := &TaskInfo{
		TaskOrigin: TaskOrigin{
			Trigger:   a2a.TriggerInteractive,
			AgentType: productanalytics.AgentTypeUserCreated,
		},
	}

	events := h.core.terminalTaskEvents(info, factfulTask("task-interactive"), nil)

	want := []string{
		productanalytics.EventAgentPeerDelegated,
		productanalytics.EventAgentRunCompleted,
		productanalytics.EventToolUsed,
	}
	if got := rawNames(events); !slices.Equal(got, want) {
		t.Fatalf("an interactive run reported %v, want %v", got, want)
	}
	for _, ev := range rawNamed(events, productanalytics.EventAgentRunCompleted) {
		if got, set := ev.Properties[productanalytics.PropRunOrigin]; set {
			t.Errorf("agent:run_completed carries runOrigin %v; it means a person ran an agent by construction, so the field would hold one value forever", got)
		}
	}
	for _, ev := range events {
		if ev.Name == productanalytics.EventAgentRunCompleted {
			continue
		}
		if got := ev.Properties[productanalytics.PropRunOrigin]; got != productanalytics.RunOriginInteractive {
			t.Errorf("%s carries runOrigin %v, want %q", ev.Name, got, productanalytics.RunOriginInteractive)
		}
	}
}

func TestMachineIngressOutranksTheScheduledTrigger(t *testing.T) {
	origin := TaskOrigin{
		Trigger:        a2a.TriggerScheduled,
		MachineIngress: true,
		AgentType:      productanalytics.AgentTypeUserCreated,
	}
	if got := runOrigin(origin); got != productanalytics.RunOriginMachineIngress {
		t.Errorf("runOrigin = %q, want %q", got, productanalytics.RunOriginMachineIngress)
	}
}

func TestMachineIngressFactsCrossTheBroker(t *testing.T) {
	h := newRunAnalyticsHarness(t, withAdapter(&machineIngressAdapter{entrypointType: "webhook"}))
	card := testAgentCard("my-agent")
	h.core.agents.AddOrUpdate(card)
	taskID := h.submit(t, card.Name, "task-webhook-broker", nil)

	h.publishAgentTask(t, taskID, usageMetadata(
		[]string{productanalytics.ToolCategoryConnector},
		[]string{"sql"},
		true,
	))

	events := h.drain(t)
	if got := toolCategoryPairs(events); !slices.Equal(got, []string{"connector/sql"}) {
		t.Fatalf("tool:used delivered %v, want the connector pair a webhook-driven run reported", got)
	}
	for _, ev := range eventsNamed(events, productanalytics.EventToolUsed) {
		if got := ev.Properties[productanalytics.PropRunOrigin]; got != productanalytics.RunOriginMachineIngress {
			t.Errorf("tool:used arrived with runOrigin %v, want %q: the catalog must approve the field, not strip it",
				got, productanalytics.RunOriginMachineIngress)
		}
	}
	if got := len(eventsNamed(events, productanalytics.EventAgentPeerDelegated)); got != 1 {
		t.Errorf("agent:peer_delegated delivered %d times, want 1", got)
	}
	if got := len(eventsNamed(events, productanalytics.EventAgentRunCompleted)); got != 0 {
		t.Errorf("agent:run_completed delivered %d times, want none for machine ingress", got)
	}
}

func TestSAMsOwnTrafficReportsNoUsageFacts(t *testing.T) {
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
	h.core.fireTaskCompleteHooks("task-investigation", factfulTask("task-investigation"), nil)

	if events := h.drain(t); len(events) != 0 {
		t.Fatalf("SAM's own traffic reported its tool use as an install's adoption: %+v", events)
	}
}
