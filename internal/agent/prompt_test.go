// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestPromptBuilderEmpty(t *testing.T) {
	pb := NewPromptBuilder()
	if pb.Build() != "" {
		t.Errorf("empty builder should produce empty string")
	}
}

func TestPromptBuilderInstruction(t *testing.T) {
	pb := NewPromptBuilder()
	pb.AddInstruction("You are a helpful assistant.")
	result := pb.Build()

	if !strings.Contains(result, "You are a helpful assistant.") {
		t.Errorf("missing instruction in prompt: %s", result)
	}
	if !strings.Contains(result, "## Instructions") {
		t.Errorf("missing section header: %s", result)
	}
}

func TestPromptBuilderSkipsEmpty(t *testing.T) {
	pb := NewPromptBuilder()
	pb.AddInstruction("")
	pb.AddGlobalInstruction("")
	pb.AddContext("Test", "")
	if pb.Build() != "" {
		t.Errorf("should skip empty sections")
	}
}

func TestPromptBuilderMultipleSections(t *testing.T) {
	pb := NewPromptBuilder()
	pb.AddInstruction("You are a search agent.")
	pb.AddGlobalInstruction("Always be concise.")
	pb.AddContext("Current Time", "2024-01-01T00:00:00Z")

	result := pb.Build()
	if !strings.Contains(result, "## Instructions") {
		t.Error("missing Instructions section")
	}
	if !strings.Contains(result, "## Global Instructions") {
		t.Error("missing Global Instructions section")
	}
	if !strings.Contains(result, "## Current Time") {
		t.Error("missing Current Time section")
	}

	iIdx := strings.Index(result, "## Instructions")
	gIdx := strings.Index(result, "## Global Instructions")
	if iIdx >= gIdx {
		t.Error("Instructions should appear before Global Instructions")
	}
}

const largeTableRule = "table of more than 20 rows"

func TestLargeTableRuleNeedsArtifactContentTemplatesAndADisplayingGateway(t *testing.T) {
	cases := []struct {
		name                   string
		includeArtifactContent bool
		templatesEnabled       bool
		displaysTemplates      bool
		want                   bool
	}{
		{"artifact content, templates and display on", true, true, true, true},
		{"templates disabled", true, false, true, false},
		{"artifact content off", false, true, true, false},
		{"root gateway does not display templates", true, true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			agent, _, _ := newTestAgent()
			agent.cfg.EnableEmbedResolution = new(true)
			agent.cfg.EnableTemplateInstructions = new(c.templatesEnabled)
			agent.cfg.EnableArtifactContentInstruction = new(c.includeArtifactContent)
			prompt := agent.buildSystemPrompt(t.Context(), nil, gatewayContext{DisplaysTemplates: c.displaysTemplates}, promptSkillState{}, false)
			if got := strings.Contains(prompt, largeTableRule); got != c.want {
				t.Errorf("large-table rule present = %v, want %v", got, c.want)
			}
		})
	}
}

func TestLargeTableRulePointsAtTemplatesAndNamesTools(t *testing.T) {
	_, rule, found := strings.Cut(embedsGuide(true, true), largeTableRule)
	if !found {
		t.Fatal("large-table rule missing")
	}
	for _, want := range []string{BlockPrefixTemplateLiquid, "`query_data_with_sql`", "`execute_python_code`"} {
		if !strings.Contains(rule, want) {
			t.Errorf("large-table rule does not mention %q", want)
		}
	}
}

func TestSetupTaskPromptCarriesLargeTableRuleOnlyWhereTemplatesDisplay(t *testing.T) {
	for _, displays := range []bool{true, false} {
		t.Run(fmt.Sprintf("displays=%v", displays), func(t *testing.T) {
			agent, _, _ := newTestAgent()
			agent.cfg.EnableEmbedResolution = new(true)
			agent.cfg.EnableTemplateInstructions = new(true)
			agent.cfg.EnableArtifactContentInstruction = new(true)
			msg := newUserMessage("hi")
			if displays {
				msg.Metadata = map[string]any{a2a.MetadataDisplaysTemplateSignals: true}
			}

			state, _, earlyResp, err := agent.setupTask(t.Context(), "task-1", msg, a2a.UserProperties{UserID: "alice"}, taskDelivery{})
			if err != nil || earlyResp != nil {
				t.Fatalf("setupTask: err=%v earlyResp=%+v", err, earlyResp)
			}
			if len(state.Messages) == 0 || state.Messages[0].Role != "system" {
				t.Fatalf("first message is not the system prompt: %+v", state.Messages)
			}
			if got := strings.Contains(state.Messages[0].Content, largeTableRule); got != displays {
				t.Errorf("large-table rule present = %v, want %v", got, displays)
			}
		})
	}
}

func buildSystemPromptOnce(t *testing.T) string {
	t.Helper()
	a := New(AgentConfig{Name: "prefix-agent", Model: "test-model", Namespace: "test-ns", MaxLLMCalls: 3})
	svc := newMockServices(newMockLLM(), newMemSessionStore())
	a.svc = svc
	if err := a.Init(t.Context(), nil, svc); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, n := range []string{"zeta", "alpha", "mike", "kilo", "echo", "tango"} {
		if err := a.tools.Register(&testBuiltinTool{
			name:         n,
			description:  "test tool " + n,
			instructions: "Use " + n + " only when appropriate.",
		}); err != nil {
			t.Fatalf("Register(%s): %v", n, err)
		}
	}
	return a.buildSystemPrompt(context.Background(), nil, gatewayContext{}, promptSkillState{}, false)
}

func TestSystemPromptStableAcrossBuilds(t *testing.T) {
	ref := buildSystemPromptOnce(t)
	for i := range 200 {
		got := buildSystemPromptOnce(t)
		if got != ref {
			j := 0
			for j < len(ref) && j < len(got) && ref[j] == got[j] {
				j++
			}
			lo := max(0, j-60)
			t.Fatalf("system prompt diverged across independent builds (iteration %d, byte %d):\n ref: %q\n got: %q",
				i, j, ref[lo:min(len(ref), j+80)], got[lo:min(len(got), j+80)])
		}
	}
}

func TestResumeTaskExecutionPromptCarriesLargeTableRuleOnlyWhereTemplatesDisplay(t *testing.T) {
	for _, displays := range []bool{true, false} {
		t.Run(fmt.Sprintf("displays=%v", displays), func(t *testing.T) {
			agent, mock, store := newTestAgent(&llm.ChatResponse{Content: "done", FinishReason: "stop"})
			agent.cfg.EnableEmbedResolution = new(true)
			agent.cfg.EnableTemplateInstructions = new(true)
			agent.cfg.EnableArtifactContentInstruction = new(true)
			agent.runCtx = t.Context()
			if err := store.Create(t.Context(), &session.Session{AppName: agent.cfg.Name, UserID: "alice", SessionID: "s-resume", ContextID: "s-resume"}); err != nil {
				t.Fatal(err)
			}
			state := newTestTaskState(agent)
			state.TaskID = "task-resume"
			state.EffectiveSessionID = "s-resume"
			state.UserProps.UserID = "alice"
			state.DisplaysTemplateSignals = displays
			state.PausedAssistantMsg = assistantMsgWithOneToolCall("call-r1", "peer", `{}`)
			state.SyncResults[0] = tool.OK("peer answered")
			state.Messages = []llm.ChatMessage{{Role: "system", Content: "stale"}, {Role: "user", Content: "ask peer"}, state.PausedAssistantMsg}
			agent.pausedTasks.Store(state)

			agent.resumeTaskExecution(state)

			calls := mock.getCalls()
			if len(calls) == 0 {
				t.Fatal("the resumed task made no LLM call")
			}
			system := calls[0].Messages[0]
			if system.Role != "system" {
				t.Fatalf("first message of the LLM request is %q, want the system prompt", system.Role)
			}
			if got := strings.Contains(system.Content, largeTableRule); got != displays {
				t.Errorf("large-table rule present = %v, want %v", got, displays)
			}
		})
	}
}

func TestShapeToolsKeepsEveryToolAndPeerFiles(t *testing.T) {
	defs := []llm.ToolDefinition{
		{Name: "unload_skill"}, {Name: "list_skill_resources"},
		{Name: "peer_finance", Parameters: map[string]any{"type": "object", "properties": map[string]any{"task_description": map[string]any{}}}},
	}

	got := shapeTools(slices.Clone(defs))

	if len(got) != 3 {
		t.Fatalf("tools = %v, want all three", got)
	}
	props, _ := got[2].Parameters["properties"].(map[string]any)
	if props["task_description"] == nil || props["artifacts"] == nil {
		t.Errorf("peer tool parameters = %v, want task_description and artifacts", got[2].Parameters)
	}
}

var testStubBatchBytes = llm.ToolResultStubBatchBytes("gemini", "")

func toolTurn(id string, size int) []llm.ChatMessage {
	return []llm.ChatMessage{
		{Role: "user", Content: "q " + id},
		{Role: "tool", Content: strings.Repeat("r", size), ContentBlocks: []llm.ContentBlock{{Type: "text"}}, ToolCallID: id, WithheldBytes: 99},
		{Role: "assistant", Content: "answer " + id},
	}
}

func stubbedIDs(history []llm.ChatMessage) []string {
	var ids []string
	for _, m := range history {
		if m.Role == "tool" && m.Content == stubbedToolResult {
			ids = append(ids, m.ToolCallID)
		}
	}
	return ids
}

func TestStubToolResultsKeepsEverythingButToolOutput(t *testing.T) {
	history := []llm.ChatMessage{
		{Role: "user", Content: "q"},
		{Role: "tool", Content: "big result", ToolCallID: "c1"},
		{Role: "tool", Content: "", ToolCallID: "c2"},
		{Role: "assistant", Content: "answer"},
	}

	got := stubToolResults(history, 0)

	if ids := stubbedIDs(got); !slices.Equal(ids, []string{"c1", "c2"}) {
		t.Errorf("stubbed %v, want every replayed result, the empty one included", ids)
	}
	if got[0].Content != "q" || got[3].Content != "answer" || history[1].Content != "big result" {
		t.Error("only the replayed tool output may change, and the input must not")
	}
}

func TestStubToolResultsKeepsResultsUntilTheBatchFills(t *testing.T) {
	quarter := testStubBatchBytes / 4
	history := slices.Concat(toolTurn("c1", quarter), toolTurn("c2", quarter))

	got := stubToolResults(history, testStubBatchBytes)

	if ids := stubbedIDs(got); len(ids) != 0 {
		t.Errorf("stubbed %v, want no result stubbed while the replayed results are under one batch", ids)
	}
	for _, m := range got {
		if m.Role == "tool" && m.WithheldBytes != 99 {
			t.Errorf("kept result %s books %d withheld bytes, want its 99: the prompt still carries its reference", m.ToolCallID, m.WithheldBytes)
		}
	}
}

func TestStubToolResultsBatchBoundaryEdges(t *testing.T) {
	batch := testStubBatchBytes
	cases := []struct {
		name  string
		sizes []int
		want  []string
	}{
		{"a result straddling the boundary is stubbed", []int{batch * 5 / 8, batch * 5 / 8, batch * 5 / 8}, []string{"c0", "c1"}},
		{"exactly one batch stubs every result", []int{batch / 2, batch / 2}, []string{"c0", "c1"}},
		{"one byte under a batch stubs nothing", []int{batch / 2, batch/2 - 1}, nil},
		{"one byte over a batch keeps the result starting on it", []int{batch / 2, batch / 2, 1}, []string{"c0", "c1"}},
		{"a single result larger than a batch is stubbed", []int{batch + batch/4}, []string{"c0"}},
		{"an empty result before the boundary is stubbed", []int{0, batch}, []string{"c0", "c1"}},
		{"an empty result on the boundary is kept", []int{batch, 0}, []string{"c0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var history []llm.ChatMessage
			for i, size := range tc.sizes {
				history = slices.Concat(history, toolTurn(fmt.Sprintf("c%d", i), size))
			}

			got := stubToolResults(history, testStubBatchBytes)

			if ids := stubbedIDs(got); !slices.Equal(ids, tc.want) {
				t.Errorf("stubbed %v, want %v", ids, tc.want)
			}
			for _, m := range got {
				if m.Role != "tool" {
					continue
				}
				want, wantBlocks := int64(99), 1
				if m.Content == stubbedToolResult {
					want, wantBlocks = 0, 0
				}
				if m.WithheldBytes != want {
					t.Errorf("result %s books %d withheld bytes, want %d", m.ToolCallID, m.WithheldBytes, want)
				}
				if len(m.ContentBlocks) != wantBlocks {
					t.Errorf("result %s carries %d content blocks, want %d", m.ToolCallID, len(m.ContentBlocks), wantBlocks)
				}
			}
		})
	}
}

func TestStubToolResultsStubsEveryResultThatStartsBeforeTheBoundary(t *testing.T) {
	half := testStubBatchBytes / 2
	history := slices.Concat(toolTurn("c1", half), toolTurn("c2", half), toolTurn("c3", half), toolTurn("c4", half/4))

	got := stubToolResults(history, testStubBatchBytes)

	if ids := stubbedIDs(got); !slices.Equal(ids, []string{"c1", "c2"}) {
		t.Errorf("stubbed %v, want c1 and c2, which start before the first batch boundary; c3 starts on it", ids)
	}
	if got[0].Content != "q c1" || got[2].Content != "answer c1" || history[1].Content == stubbedToolResult {
		t.Error("only replayed tool output may change, and the input must not")
	}
	if got[1].ToolCallID != "c1" {
		t.Errorf("stubbed result lost its call id: %+v", got[1])
	}
}

func TestStubToolResultsOnlyGrowsBetweenBoundaries(t *testing.T) {
	resultSize := testStubBatchBytes / 5
	var history []llm.ChatMessage
	var previous []llm.ChatMessage
	rewrites := 0
	for turn := range 20 {
		history = slices.Concat(history, toolTurn(fmt.Sprintf("c%d", turn), resultSize))
		got := stubToolResults(history, testStubBatchBytes)
		for i := range previous {
			if previous[i].Content != got[i].Content {
				rewrites++
				break
			}
		}
		if previous != nil && len(stubbedIDs(previous)) > len(stubbedIDs(got)) {
			t.Fatalf("turn %d stubs fewer results than the turn before", turn)
		}
		previous = got
	}
	if want := 20 * resultSize / testStubBatchBytes; rewrites != want {
		t.Errorf("earlier history changed on %d of 20 turns, want %d: once per batch of results", rewrites, want)
	}
}

func TestWorkflowNodeReminderNamesTheExactPrefix(t *testing.T) {
	text := workflowNodeReminder("check", "Stockout Sweep", "wf_out.json", nil)
	for _, want := range []string{BlockPrefixSaveArtifact, `filename="wf_out.json"`, "none"} {
		if !strings.Contains(text, want) {
			t.Errorf("workflow instructions missing %q:\n%s", want, text)
		}
	}
}

func TestPromptWithEmbedsOnTeachesTheChatEmbeds(t *testing.T) {
	cfg := deployedTestConfig(AgentConfig{Name: "nw-agent", SystemPrompt: "Base.", Model: "test-model"})
	on := true
	cfg.EnableEmbedResolution = &on
	ag := New(cfg)

	got := ag.buildSystemPrompt(t.Context(), nil, gatewayContext{}, promptSkillState{}, false)

	for _, want := range []string{"## Embeds", EmbedOpen + "math:", EmbedOpen + "datetime:", EmbedOpen + "status_update:"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt with embeds on missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Using Dynamic Embeds") {
		t.Error("the prompt must not carry the full embed guide")
	}
}
