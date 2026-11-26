// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package entrypoint

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelfamily"
	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

func (h *runAnalyticsHarness) publishAgentTask(t *testing.T, taskID string, metadata map[string]any) {
	t.Helper()
	task := a2a.Task{
		ID:   taskID,
		Kind: "task",
		Status: a2a.TaskStatus{
			State:   a2a.TaskStateCompleted,
			Message: &a2a.Message{Kind: "message", Role: a2a.RoleAgent, Parts: []a2a.Part{a2a.NewTextPart("done")}},
		},
		Metadata: metadata,
	}
	payload, err := json.Marshal(a2a.NewResponse(taskID, task))
	if err != nil {
		t.Fatalf("marshal task response: %v", err)
	}
	topic := a2a.GatewayResponseTopic(h.core.Namespace(), h.core.cfg.GatewayID, taskID)
	if err := h.core.svc.Broker().PublishGuaranteed(context.Background(), topic, &broker.Message{Payload: payload}); err != nil {
		t.Fatalf("publish task response: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for h.core.tasks.Get(taskID) != nil {
		if time.Now().After(deadline) {
			t.Fatal("the task response was never delivered to the gateway")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func usageMetadata(toolCategories, connectorCategories []string, peerDelegated bool) map[string]any {
	metadata := map[string]any{"agent_name": "acme-payroll-assistant"}
	if len(toolCategories) > 0 {
		metadata[a2a.MetadataToolCategories] = toolCategories
	}
	if len(connectorCategories) > 0 {
		metadata[a2a.MetadataConnectorCategories] = connectorCategories
	}
	if peerDelegated {
		metadata[a2a.MetadataPeerDelegated] = true
	}
	return metadata
}

func eventsNamed(events []productanalytics.Event, name string) []productanalytics.Event {
	var out []productanalytics.Event
	for _, ev := range events {
		if ev.Name == name {
			out = append(out, ev)
		}
	}
	return out
}

func toolCategoryPairs(events []productanalytics.Event) []string {
	var out []string
	for _, ev := range eventsNamed(events, productanalytics.EventToolUsed) {
		out = append(out, fmt.Sprintf("%v/%v",
			ev.Properties[productanalytics.PropToolCategory],
			ev.Properties[productanalytics.PropConnectorCategory]))
	}
	slices.Sort(out)
	return out
}

func TestToolUsedCarriesTheCategoriesOverTheBroker(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-tools", nil)

	h.publishAgentTask(t, taskID, usageMetadata(
		[]string{productanalytics.ToolCategoryBuiltin, productanalytics.ToolCategoryConnector},
		[]string{"slack", "sql"},
		true,
	))

	events := h.drain(t)
	if got := len(eventsNamed(events, productanalytics.EventAgentRunCompleted)); got != 1 {
		t.Fatalf("agent:run_completed emitted %d times, want 1", got)
	}
	want := []string{"builtin/<nil>", "connector/slack", "connector/sql"}
	if got := toolCategoryPairs(events); !slices.Equal(got, want) {
		t.Errorf("tool:used events = %v, want %v", got, want)
	}
	if got := len(eventsNamed(events, productanalytics.EventAgentPeerDelegated)); got != 1 {
		t.Errorf("agent:peer_delegated emitted %d times, want 1", got)
	}
}

func TestWorkflowRunReportsTheFactsItsNodesProduced(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	card := testAgentCard("workflow_0199aaaa-bbbb-7ccc-8ddd-eeeeffff0000", displayNameExt("Claims Intake"))
	h.core.agents.AddOrUpdate(card)
	taskID := h.submit(t, card.Name, "task-workflow-usage", nil)

	metadata := usageMetadata(
		[]string{productanalytics.ToolCategoryBuiltin, productanalytics.ToolCategoryConnector},
		[]string{"sql"},
		true,
	)
	metadata["workflow_name"] = "Claims Intake"
	metadata[a2a.MetadataModelFamily] = modelfamily.ClaudeSonnet
	metadata[a2a.MetadataModelProvider] = "anthropic"
	h.publishAgentTask(t, taskID, metadata)

	events := h.drain(t)
	runs := eventsNamed(events, productanalytics.EventAgentRunCompleted)
	if len(runs) != 1 {
		t.Fatalf("agent:run_completed emitted %d times, want 1", len(runs))
	}
	run := runs[0].Properties
	if got := run[productanalytics.PropAgentType]; got != productanalytics.AgentTypeWorkflow {
		t.Errorf("%s = %v, want %q", productanalytics.PropAgentType, got, productanalytics.AgentTypeWorkflow)
	}
	if got := run[productanalytics.PropModel]; got != modelfamily.ClaudeSonnet {
		t.Errorf("%s = %v, want %q", productanalytics.PropModel, got, modelfamily.ClaudeSonnet)
	}
	if got := run[productanalytics.PropProvider]; got != "anthropic" {
		t.Errorf("%s = %v, want anthropic", productanalytics.PropProvider, got)
	}
	want := []string{"builtin/<nil>", "connector/sql"}
	if got := toolCategoryPairs(events); !slices.Equal(got, want) {
		t.Errorf("tool:used events = %v, want %v", got, want)
	}
	if got := len(eventsNamed(events, productanalytics.EventAgentPeerDelegated)); got != 1 {
		t.Errorf("agent:peer_delegated emitted %d times, want 1", got)
	}
}

func TestToolUsedDropsAnUnlistedConnectorCategory(t *testing.T) {
	facts := readUsageFacts(taskWithUsage([]string{productanalytics.ToolCategoryConnector}, []string{"acme_customer_ledger"}), nil)
	if len(facts.connectorCategories) != 0 {
		t.Errorf("connectorCategories = %v, want the unlisted category refused", facts.connectorCategories)
	}
	if !slices.Equal(facts.toolCategories, []string{productanalytics.ToolCategoryConnector}) {
		t.Errorf("toolCategories = %v, want the approved category kept", facts.toolCategories)
	}
}

func TestAnUnlistedConnectorCategoryAloneFabricatesNoToolUse(t *testing.T) {
	facts := readUsageFacts(taskWithUsage(nil, []string{"acme_customer_ledger"}), nil)
	events := usageFactEvents(&TaskInfo{}, "2026-01-01T00:00:00.000Z", facts)

	if len(events) != 0 {
		t.Errorf("emitted %d events for a category nothing recognised, want none: %+v", len(events), events)
	}
}

func taskWithUsage(toolCategories, connectorCategories []string) *a2a.Task {
	return &a2a.Task{
		ID:       "task-usage",
		Kind:     "task",
		Status:   a2a.TaskStatus{State: a2a.TaskStateCompleted},
		Metadata: usageMetadata(toolCategories, connectorCategories, false),
	}
}

func TestRunWithNoUsageFactsEmitsOnlyTheRun(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-plain", nil)

	h.publishAgentTask(t, taskID, map[string]any{"agent_name": "my-agent"})

	ev := onlyEvent(t, h.drain(t))
	if ev.Name != productanalytics.EventAgentRunCompleted {
		t.Errorf("event = %q, want only the run report", ev.Name)
	}
}

func TestToolUsedIsReadFromTheErrorPathToo(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-failed-tools", nil)

	h.publishAgentError(t, taskID, &a2a.Error{
		Code:    -32603,
		Message: "The AI model is currently rate limited.",
		Data: map[string]any{
			"token_usage":                   map[string]any{"prompt_tokens": 120},
			a2a.MetadataToolCategories:      []string{productanalytics.ToolCategoryCustom},
			a2a.MetadataConnectorCategories: []string{"knowledge_base"},
			a2a.MetadataPeerDelegated:       true,
		},
	})

	events := h.drain(t)
	want := []string{"connector/knowledge_base", "custom/<nil>"}
	if got := toolCategoryPairs(events); !slices.Equal(got, want) {
		t.Errorf("tool:used events = %v, want %v", got, want)
	}
	if got := len(eventsNamed(events, productanalytics.EventAgentPeerDelegated)); got != 1 {
		t.Errorf("agent:peer_delegated emitted %d times, want 1", got)
	}
}

func TestUsageFactsAreRefusedWhereTheAgentCannotBeClassified(t *testing.T) {
	internalCard := testAgentCard("Builder", a2a.AgentExtension{
		URI:    a2a.ExtensionURIInternal,
		Params: map[string]any{"internal": true},
	})
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(internalCard)
	taskID := h.submit(t, internalCard.Name, "task-excluded", nil)

	h.publishAgentTask(t, taskID, usageMetadata(
		[]string{productanalytics.ToolCategoryConnector},
		[]string{"sql"},
		true,
	))

	events := h.drain(t)
	for _, name := range []string{
		productanalytics.EventAgentRunCompleted,
		productanalytics.EventToolUsed,
		productanalytics.EventAgentPeerDelegated,
	} {
		if got := len(eventsNamed(events, name)); got != 0 {
			t.Errorf("%s emitted %d times, want none: %+v", name, got, events)
		}
	}
}

func TestUsageEventPropertiesAreOnlyTheApprovedEnums(t *testing.T) {
	info := &TaskInfo{
		AgentName: "acme-payroll-assistant",
		TraceID:   "0199cccc-dddd-7eee-8fff-000011112222",
	}
	events := usageFactEvents(info, "2026-08-20T00:00:00.000Z", usageFacts{
		toolCategories:      []string{productanalytics.ToolCategoryBuiltin, productanalytics.ToolCategoryConnector},
		connectorCategories: []string{"sql"},
		peerDelegated:       true,
	})

	want := []map[string]any{
		{
			productanalytics.PropToolCategory: productanalytics.ToolCategoryBuiltin,
			productanalytics.PropRunOrigin:    productanalytics.RunOriginInteractive,
			productanalytics.PropTraceID:      info.TraceID,
		},
		{
			productanalytics.PropToolCategory:      productanalytics.ToolCategoryConnector,
			productanalytics.PropConnectorCategory: "sql",
			productanalytics.PropRunOrigin:         productanalytics.RunOriginInteractive,
			productanalytics.PropTraceID:           info.TraceID,
		},
		{
			productanalytics.PropRunOrigin: productanalytics.RunOriginInteractive,
			productanalytics.PropTraceID:   info.TraceID,
		},
	}
	if len(events) != len(want) {
		t.Fatalf("emitted %d events, want %d: %+v", len(events), len(want), events)
	}
	for i, ev := range events {
		if !maps.Equal(ev.Properties, want[i]) {
			t.Errorf("event %d (%s) properties = %+v, want %+v", i, ev.Name, ev.Properties, want[i])
		}
	}
}

func TestUsageEventPropertiesNameNothing(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("acme-payroll-assistant"))
	taskID := h.submit(t, "acme-payroll-assistant", "task-names", nil)

	metadata := usageMetadata(
		[]string{
			productanalytics.ToolCategoryBuiltin,
			productanalytics.ToolCategoryConnector,
			productanalytics.ToolCategoryCustom,
		},
		[]string{"sql"},
		true,
	)
	metadata["agent_name"] = "acme-payroll-assistant"
	metadata["produced_artifacts"] = []map[string]any{{"filename": "Q3 payroll — Acme.xlsx", "version": 1}}
	metadata[a2a.MetadataToolModelFamilies] = []string{modelfamily.GPT4o}
	metadata[a2a.MetadataBuiltinCategories] = []string{"builtin_artifact_tools"}
	h.publishAgentTask(t, taskID, metadata)

	events := h.drain(t)
	if len(events) == 0 {
		t.Fatal("nothing was emitted at all")
	}
	allowed := map[string]bool{
		productanalytics.PropOutcome:           true,
		productanalytics.PropAgentType:         true,
		productanalytics.PropToolCategory:      true,
		productanalytics.PropConnectorCategory: true,
		productanalytics.PropBuiltinCategory:   true,
		productanalytics.PropProvider:          true,
		productanalytics.PropModel:             true,
		productanalytics.PropToolModels:        true,
		productanalytics.PropRunOrigin:         true,
		productanalytics.PropTraceID:           true,
		productanalytics.PropInterface:         true,
		"appVersion":                           true,
		"os":                                   true,
		"arch":                                 true,
	}
	for _, ev := range events {
		rendered, err := json.Marshal(ev.Properties)
		if err != nil {
			t.Fatalf("marshal properties: %v", err)
		}
		for _, secret := range []string{
			"acme-payroll-assistant", "Acme", "payroll", "Q3", "xlsx", "sql_query",
		} {
			if strings.Contains(string(rendered), secret) {
				t.Errorf("%s: %q reached the emitted properties %s", ev.Name, secret, rendered)
			}
		}
		for key := range ev.Properties {
			if !allowed[key] {
				t.Errorf("%s: unexpected property %q; the emitted set is closed", ev.Name, key)
			}
		}
	}
}

func builtinCategoryPairs(events []productanalytics.Event) []string {
	var out []string
	for _, ev := range eventsNamed(events, productanalytics.EventToolUsed) {
		out = append(out, fmt.Sprintf("%v/%v",
			ev.Properties[productanalytics.PropToolCategory],
			ev.Properties[productanalytics.PropBuiltinCategory]))
	}
	slices.Sort(out)
	return out
}

func TestBuiltinCategoryRefinesTheBuiltinEventAtTheEmitter(t *testing.T) {
	info := &TaskInfo{
		AgentName: "acme-payroll-assistant",
		TraceID:   "0199cccc-dddd-7eee-8fff-000011112222",
	}
	events := usageFactEvents(info, "2026-08-20T00:00:00.000Z", usageFacts{
		toolCategories:    []string{productanalytics.ToolCategoryBuiltin},
		builtinCategories: []string{"builtin_artifact_tools", "data_analysis"},
	})

	want := []map[string]any{
		{
			productanalytics.PropToolCategory:    productanalytics.ToolCategoryBuiltin,
			productanalytics.PropBuiltinCategory: "builtin_artifact_tools",
			productanalytics.PropRunOrigin:       productanalytics.RunOriginInteractive,
			productanalytics.PropTraceID:         info.TraceID,
		},
		{
			productanalytics.PropToolCategory:    productanalytics.ToolCategoryBuiltin,
			productanalytics.PropBuiltinCategory: "data_analysis",
			productanalytics.PropRunOrigin:       productanalytics.RunOriginInteractive,
			productanalytics.PropTraceID:         info.TraceID,
		},
	}
	if len(events) != len(want) {
		t.Fatalf("emitted %d events, want %d: %+v", len(events), len(want), events)
	}
	for i, ev := range events {
		if ev.Name != productanalytics.EventToolUsed {
			t.Errorf("event %d = %q, want %q", i, ev.Name, productanalytics.EventToolUsed)
		}
		if !maps.Equal(ev.Properties, want[i]) {
			t.Errorf("event %d properties = %+v, want %+v", i, ev.Properties, want[i])
		}
	}
}

func TestAnUnrefinedBuiltinStillReportsTheBareEvent(t *testing.T) {
	events := usageFactEvents(&TaskInfo{}, "2026-08-20T00:00:00.000Z", usageFacts{
		toolCategories: []string{productanalytics.ToolCategoryBuiltin},
	})

	want := []map[string]any{{
		productanalytics.PropToolCategory: productanalytics.ToolCategoryBuiltin,
		productanalytics.PropRunOrigin:    productanalytics.RunOriginInteractive,
	}}
	if len(events) != len(want) {
		t.Fatalf("emitted %d events, want %d: %+v", len(events), len(want), events)
	}
	if !maps.Equal(events[0].Properties, want[0]) {
		t.Errorf("properties = %+v, want %+v", events[0].Properties, want[0])
	}
}

func TestBuiltinCategoryCrossesTheBroker(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-builtin-groups", nil)

	metadata := usageMetadata([]string{productanalytics.ToolCategoryBuiltin}, nil, false)
	metadata[a2a.MetadataBuiltinCategories] = []string{"builtin_artifact_tools", "scheduling_tools"}
	h.publishAgentTask(t, taskID, metadata)

	events := h.drain(t)
	want := []string{"builtin/builtin_artifact_tools", "builtin/scheduling_tools"}
	if got := builtinCategoryPairs(events); !slices.Equal(got, want) {
		t.Errorf("tool:used events = %v, want %v", got, want)
	}
}

func TestEveryBuiltinCategoryTheCatalogApprovesArrivesAsItsOwnEvent(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-every-builtin-group", nil)

	metadata := usageMetadata([]string{productanalytics.ToolCategoryBuiltin}, nil, false)
	metadata[a2a.MetadataBuiltinCategories] = slices.Clone(productanalytics.BuiltinToolCategories)
	h.publishAgentTask(t, taskID, metadata)

	want := make([]string, 0, len(productanalytics.BuiltinToolCategories))
	for _, category := range productanalytics.BuiltinToolCategories {
		want = append(want, "builtin/"+category)
	}
	slices.Sort(want)
	if got := builtinCategoryPairs(h.drain(t)); !slices.Equal(got, want) {
		t.Errorf("%d approved groups produced %d tool:used events:\n  got  %v\n  want %v",
			len(productanalytics.BuiltinToolCategories), len(got), got, want)
	}
}

func TestUnlistedBuiltinCategoryIsRefusedOnArrival(t *testing.T) {
	metadata := usageMetadata([]string{productanalytics.ToolCategoryBuiltin}, nil, false)
	metadata[a2a.MetadataBuiltinCategories] = []string{"acme_internal_toolset", "builtin_artifact_tools"}
	task := &a2a.Task{
		ID:       "task-usage",
		Kind:     "task",
		Status:   a2a.TaskStatus{State: a2a.TaskStateCompleted},
		Metadata: metadata,
	}

	facts := readUsageFacts(task, nil)
	if !slices.Equal(facts.builtinCategories, []string{"builtin_artifact_tools"}) {
		t.Errorf("builtinCategories = %v, want only the approved group kept", facts.builtinCategories)
	}
}

func TestAnUnlistedBuiltinCategoryAloneFabricatesNoToolUse(t *testing.T) {
	metadata := map[string]any{a2a.MetadataBuiltinCategories: []string{"acme_internal_toolset"}}
	task := &a2a.Task{
		ID:       "task-usage",
		Kind:     "task",
		Status:   a2a.TaskStatus{State: a2a.TaskStateCompleted},
		Metadata: metadata,
	}

	events := usageFactEvents(&TaskInfo{}, "2026-01-01T00:00:00.000Z", readUsageFacts(task, nil))
	if len(events) != 0 {
		t.Errorf("emitted %d events for a group nothing recognised, want none: %+v", len(events), events)
	}
}

func TestOversizedUsageSetIsCappedOnArrival(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-flood", nil)

	flood := make([]string, 5000)
	for i := range flood {
		flood[i] = productanalytics.ToolCategoryBuiltin
	}
	h.publishAgentTask(t, taskID, usageMetadata(flood, nil, false))

	used := eventsNamed(h.drain(t), productanalytics.EventToolUsed)
	if len(used) != 1 {
		t.Fatalf("a %d element set of one repeated category produced %d tool:used events, want 1", len(flood), len(used))
	}
}

func TestUnrecognizedCategoriesEmitNoEvent(t *testing.T) {
	h := newRunAnalyticsHarness(t)
	h.core.agents.AddOrUpdate(testAgentCard("my-agent"))
	taskID := h.submit(t, "my-agent", "task-unknown", nil)

	h.publishAgentTask(t, taskID, usageMetadata(
		[]string{"acme_internal_tool", "payroll_oracle"},
		nil,
		false,
	))

	if used := eventsNamed(h.drain(t), productanalytics.EventToolUsed); len(used) != 0 {
		t.Fatalf("unrecognized categories emitted %d tool:used events, want 0: %+v", len(used), used)
	}
}
