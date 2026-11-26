// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/usagefacts"
)

func registerUsageFactTool(t *testing.T, agent *Agent, name string) {
	t.Helper()
	err := agent.Tools().Register(&testBuiltinTool{
		name:        name,
		description: "no-op tool",
		params:      &tool.JSONSchema{Type: "object", Properties: map[string]any{}},
		execFn: func(_ context.Context, _ map[string]any, _ *tool.Context) (*tool.Result, error) {
			return tool.OK("ok"), nil
		},
	})
	if err != nil {
		t.Fatalf("register %q: %v", name, err)
	}
}

func toolCallTurns(names ...string) []*llm.ChatResponse {
	turns := make([]*llm.ChatResponse, 0, len(names)+1)
	for i, name := range names {
		turns = append(turns, &llm.ChatResponse{
			FinishReason: "tool_calls",
			ToolCalls:    []llm.ToolCall{{ID: "call-" + name + string(rune('a'+i)), Name: name, Arguments: `{}`}},
		})
	}
	return append(turns, &llm.ChatResponse{Content: "done", FinishReason: "stop"})
}

func finalTaskOf(t *testing.T, resp *a2a.Response) a2a.Task {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected error response: %+v", resp.Error)
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	var task a2a.Task
	if err := json.Unmarshal(envelope.Result, &task); err != nil {
		t.Fatalf("unmarshal task: %v", err)
	}
	return task
}

func metadataStrings(t *testing.T, metadata map[string]any, key string) []string {
	t.Helper()
	raw, present := metadata[key]
	if !present {
		return nil
	}
	values, ok := raw.([]any)
	if !ok {
		t.Fatalf("%s = %#v, want a JSON array", key, raw)
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%s carries a %T, want only strings", key, v)
		}
		out = append(out, s)
	}
	return out
}

func recordProvenance(agent *Agent, byName map[string]ToolProvenance) {
	for name, provenance := range byName {
		agent.RecordToolProvenance(name, provenance)
	}
}

func TestFinishedTaskReportsEachToolCategoryOnce(t *testing.T) {
	agent, _, _ := newTestAgent(toolCallTurns("alpha", "beta", "alpha")...)
	agent.cfg.MaxLLMCalls = 10
	for _, name := range []string{"alpha", "beta"} {
		registerUsageFactTool(t, agent, name)
	}
	recordProvenance(agent, map[string]ToolProvenance{
		"alpha": {Category: taxonomy.ToolCategoryBuiltin},
		"beta":  {Category: taxonomy.ToolCategoryBuiltin},
	})

	resp, err := agent.HandleTask(context.Background(), "task-cat",
		newUserMessage("go"), a2a.UserProperties{ClientID: "c", UserID: "u"})
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	task := finalTaskOf(t, resp)
	if got := metadataStrings(t, task.Metadata, a2a.MetadataToolCategories); !slices.Equal(got, []string{taxonomy.ToolCategoryBuiltin}) {
		t.Errorf("%s = %v, want exactly one builtin", a2a.MetadataToolCategories, got)
	}
	if _, present := task.Metadata[a2a.MetadataConnectorCategories]; present {
		t.Errorf("a run that touched no connector must carry no connector categories: %#v", task.Metadata[a2a.MetadataConnectorCategories])
	}
	if _, present := task.Metadata[a2a.MetadataPeerDelegated]; present {
		t.Errorf("a run that delegated to nobody must carry no delegation flag")
	}
}

func TestFinishedTaskReportsWhichBuiltinToolsetsItUsed(t *testing.T) {
	agent, _, _ := newTestAgent(toolCallTurns("artifact_lister", "web_getter", "project_search")...)
	agent.cfg.MaxLLMCalls = 10
	for _, name := range []string{"artifact_lister", "web_getter", "project_search"} {
		registerUsageFactTool(t, agent, name)
	}
	recordProvenance(agent, map[string]ToolProvenance{
		"artifact_lister": {
			Category:        taxonomy.ToolCategoryBuiltin,
			BuiltinCategory: "builtin_artifact_tools",
		},
		"web_getter": {
			Category:        taxonomy.ToolCategoryBuiltin,
			BuiltinCategory: "builtin_web_request_tools",
		},
		"project_search": {Category: taxonomy.ToolCategoryBuiltin},
	})

	resp, err := agent.HandleTask(context.Background(), "task-builtin-cat",
		newUserMessage("go"), a2a.UserProperties{ClientID: "c", UserID: "u"})
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	task := finalTaskOf(t, resp)
	want := []string{"builtin_artifact_tools", "builtin_web_request_tools"}
	if got := metadataStrings(t, task.Metadata, a2a.MetadataBuiltinCategories); !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", a2a.MetadataBuiltinCategories, got, want)
	}
	if got := metadataStrings(t, task.Metadata, a2a.MetadataToolCategories); !slices.Equal(got, []string{taxonomy.ToolCategoryBuiltin}) {
		t.Errorf("%s = %v, want the builtin category kept beside the refinement", a2a.MetadataToolCategories, got)
	}
	rendered, err := json.Marshal(task.Metadata)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	for _, name := range []string{"artifact_lister", "web_getter", "project_search"} {
		if strings.Contains(string(rendered), name) {
			t.Errorf("tool name %q reached the terminal metadata %s", name, rendered)
		}
	}
}

func TestFinishedTaskReportsAConnectorCategory(t *testing.T) {
	agent, _, _ := newTestAgent(toolCallTurns("acme_ledger_sql_query_1a2b3c4d", "alpha")...)
	agent.cfg.MaxLLMCalls = 10
	registerUsageFactTool(t, agent, "acme_ledger_sql_query_1a2b3c4d")
	registerUsageFactTool(t, agent, "alpha")
	recordProvenance(agent, map[string]ToolProvenance{
		"acme_ledger_sql_query_1a2b3c4d": {
			Category:          taxonomy.ToolCategoryConnector,
			ConnectorCategory: "sql",
		},
		"alpha": {Category: taxonomy.ToolCategoryBuiltin},
	})

	resp, err := agent.HandleTask(context.Background(), "task-conn",
		newUserMessage("go"), a2a.UserProperties{ClientID: "c", UserID: "u"})
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	task := finalTaskOf(t, resp)
	wantCategories := []string{taxonomy.ToolCategoryBuiltin, taxonomy.ToolCategoryConnector}
	if got := metadataStrings(t, task.Metadata, a2a.MetadataToolCategories); !slices.Equal(got, wantCategories) {
		t.Errorf("%s = %v, want %v", a2a.MetadataToolCategories, got, wantCategories)
	}
	if got := metadataStrings(t, task.Metadata, a2a.MetadataConnectorCategories); !slices.Equal(got, []string{"sql"}) {
		t.Errorf("%s = %v, want [sql]", a2a.MetadataConnectorCategories, got)
	}
}

func TestFinishedTaskReportsNothingForAnUnclassifiedTool(t *testing.T) {
	agent, _, _ := newTestAgent(toolCallTurns("alpha")...)
	registerUsageFactTool(t, agent, "alpha")

	resp, err := agent.HandleTask(context.Background(), "task-unknown",
		newUserMessage("go"), a2a.UserProperties{ClientID: "c", UserID: "u"})
	if err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	task := finalTaskOf(t, resp)
	if _, present := task.Metadata[a2a.MetadataToolCategories]; present {
		t.Errorf("an unclassified tool must report no category, got %#v", task.Metadata[a2a.MetadataToolCategories])
	}
}

func TestTerminalMetadataNamesNothing(t *testing.T) {
	var facts taskUsageFacts
	facts.MarkPeerDelegated()
	facts.AddToolCategory(taxonomy.ToolCategoryConnector)
	facts.AddConnectorCategory("sql")

	metadata := map[string]any{}
	facts.AddTo(metadata)
	rendered, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, secret := range []string{
		"acme_ledger_sql_query_1a2b3c4d", "Acme Support Bot", "acme-payroll-prod", "test-agent",
	} {
		if strings.Contains(string(rendered), secret) {
			t.Errorf("%q reached the terminal metadata %s", secret, rendered)
		}
	}
	for key, value := range metadata {
		switch key {
		case a2a.MetadataToolCategories, a2a.MetadataConnectorCategories, a2a.MetadataPeerDelegated,
			a2a.MetadataModelFamily, a2a.MetadataModelProvider:
		default:
			t.Errorf("unexpected key %q = %v; the carried set is closed", key, value)
		}
	}
}

func TestRecordToolUsageCountsOnlyRealPeerDelegation(t *testing.T) {
	agent, _, _ := newTestAgent()
	peer := &peerAgentTool{peerName: "Support Bot", agent: agent, namespace: "test-ns"}
	if err := agent.Tools().Register(peer); err != nil {
		t.Fatalf("register peer tool: %v", err)
	}
	workflow := &workflowAgentTool{peerName: "Refunds", agent: agent, namespace: "test-ns"}
	if err := agent.Tools().Register(workflow); err != nil {
		t.Fatalf("register workflow tool: %v", err)
	}
	registerUsageFactTool(t, agent, "peer_impostor")
	recordProvenance(agent, map[string]ToolProvenance{
		"peer_impostor": {Category: taxonomy.ToolCategoryCustom},
	})

	cases := []struct {
		name          string
		calls         []llm.ToolCall
		wantDelegated bool
	}{
		{"a peer delegation", singleCall(peer.Name(), "c1", "{}"), true},
		{"a workflow invocation is not peer delegation", singleCall(workflow.Name(), "c2", "{}"), false},
		{"an operator's tool named like a peer is not one", singleCall("peer_impostor", "c3", "{}"), false},
		{"a tool that is not registered at all", singleCall("vanished", "c4", "{}"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			state := newTestTaskState(agent)
			agent.recordToolUsage(state, c.calls)
			metadata := map[string]any{}
			state.usageFacts.AddTo(metadata)
			if got, _ := metadata[a2a.MetadataPeerDelegated].(bool); got != c.wantDelegated {
				t.Errorf("%s = %v, want %v", a2a.MetadataPeerDelegated, got, c.wantDelegated)
			}
		})
	}
}

func TestRecordToolUsageCollapsesRepeatedDelegation(t *testing.T) {
	agent, _, _ := newTestAgent()
	peer := &peerAgentTool{peerName: "Support Bot", agent: agent, namespace: "test-ns"}
	if err := agent.Tools().Register(peer); err != nil {
		t.Fatalf("register peer tool: %v", err)
	}
	state := newTestTaskState(agent)
	for i := range 1000 {
		agent.recordToolUsage(state, singleCall(peer.Name(), "c", "{}"))
		if i == 0 {
			first := map[string]any{}
			state.usageFacts.AddTo(first)
			if first[a2a.MetadataPeerDelegated] != true {
				t.Fatal("the first delegation was not recorded")
			}
		}
	}

	metadata := map[string]any{}
	state.usageFacts.AddTo(metadata)
	if got := metadata[a2a.MetadataPeerDelegated]; got != true {
		t.Errorf("%s = %v, want true", a2a.MetadataPeerDelegated, got)
	}
	if len(metadata) != 1 {
		t.Errorf("metadata = %#v, want the delegation flag alone", metadata)
	}
}

func TestErrorResponseCarriesTheUsageFacts(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := newTestTaskState(agent)
	state.usageFacts.AddToolCategory(taxonomy.ToolCategoryConnector)
	state.usageFacts.AddConnectorCategory("slack")
	state.usageFacts.MarkPeerDelegated()

	resp := errorResponseWithUsage(state, -32603, "The AI model is currently rate limited.")
	data, ok := resp.Error.Data.(map[string]any)
	if !ok {
		t.Fatalf("Error.Data = %#v, want a map", resp.Error.Data)
	}
	want := map[string]any{
		a2a.MetadataToolCategories:      []string{taxonomy.ToolCategoryConnector},
		a2a.MetadataConnectorCategories: []string{"slack"},
		a2a.MetadataPeerDelegated:       true,
	}
	if !reflect.DeepEqual(data, want) {
		t.Errorf("Error.Data = %#v, want %#v", data, want)
	}
}

func TestErrorResponseStaysEmptyWithNoFacts(t *testing.T) {
	agent, _, _ := newTestAgent()
	resp := errorResponseWithUsage(newTestTaskState(agent), -32603, "boom")
	if resp.Error.Data != nil {
		t.Errorf("Error.Data = %#v, want nil", resp.Error.Data)
	}
}

func TestUsageFactSetsAreBounded(t *testing.T) {
	var facts taskUsageFacts
	for i := range usagefacts.MaxSetElements * 4 {
		facts.AddConnectorCategory(string(rune('a' + i%26)))
		facts.AddToolCategory(string(rune('A' + i%26)))
	}
	metadata := map[string]any{}
	facts.AddTo(metadata)
	for _, key := range []string{a2a.MetadataConnectorCategories, a2a.MetadataToolCategories} {
		if got := len(metadata[key].([]string)); got > usagefacts.MaxSetElements {
			t.Errorf("%s = %d elements, want at most %d", key, got, usagefacts.MaxSetElements)
		}
	}
}
