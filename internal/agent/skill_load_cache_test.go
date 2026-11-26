// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const (
	docsInstructions   = "Write in the house style."
	renderInstructions = "Render before sending."
)

type fakeSkillState struct {
	loaded  map[string]bool
	version int64
}

func (s *fakeSkillState) IsLoaded(name string) bool { return s.loaded[name] }
func (s *fakeSkillState) Load(name string)          { s.loaded[name] = true; s.version++ }
func (s *fakeSkillState) Unload(name string)        { delete(s.loaded, name); s.version++ }
func (s *fakeSkillState) Version() int64            { return s.version }
func (s *fakeSkillState) LoadedNames() []string {
	names := make([]string, 0, len(s.loaded))
	for name := range s.loaded {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func newSkillCacheAgent(t *testing.T) *Agent {
	t.Helper()
	agent, _, _ := newTestAgent()
	agent.SetSkillManager(&fakeSkillManager{
		instructions: map[string]string{"docs": docsInstructions},
		toolOwner:    map[string]string{"docs__render": "docs"},
		hasResources: true,
	})
	for _, tl := range []*testBuiltinTool{
		{name: "docs__render", description: "render a document", instructions: renderInstructions},
		{name: "read_skill_resource", description: "read a skill resource"},
	} {
		if err := agent.Tools().Register(tl); err != nil {
			t.Fatalf("register %s: %v", tl.name, err)
		}
	}
	return agent
}

func appendSkillToolExchange(t *testing.T, agent *Agent, msgs []llm.ChatMessage, toolName, callID string, result *tool.Result) []llm.ChatMessage {
	t.Helper()
	content, _ := agent.formatToolResult(t.Context(), result, nil, nil)
	return append(msgs,
		llm.ChatMessage{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: callID, Name: toolName, Arguments: `{"name":"docs"}`}}},
		llm.ChatMessage{Role: "tool", ToolCallID: callID, Content: content},
	)
}

func appendLoadSkillExchange(t *testing.T, agent *Agent, msgs []llm.ChatMessage, callID string, result *tool.Result) []llm.ChatMessage {
	t.Helper()
	return appendSkillToolExchange(t, agent, msgs, LoadSkillToolName, callID, result)
}

func loadResult(status, instructions string) *tool.Result {
	r := &tool.Result{Status: status, Message: "Loaded skill 'docs'."}
	r.LLMData = map[string]any{
		LoadedSkillDataKey:       "docs",
		SkillInstructionsDataKey: instructions,
		ToolInstructionsDataKey:  ToolInstructionLine("docs__render", renderInstructions),
	}
	return r
}

func deliveringLoadResult() *tool.Result { return loadResult(tool.StatusSuccess, docsInstructions) }

func toolNames(defs []llm.ToolDefinition) []string {
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	return names
}

func TestLoadingASkillMidTaskLeavesTheCachedPrefixUnchanged(t *testing.T) {
	agent := newSkillCacheAgent(t)
	skills := &fakeSkillState{loaded: map[string]bool{}}
	rc := &taskRunContext{skillState: skills}
	state := &TaskExecutionState{Messages: []llm.ChatMessage{{Role: "system"}, {Role: "user", Content: "draft the memo"}}}
	var cache promptToolCache

	toolsBefore := toolNames(agent.refreshToolsAndPrompt(t.Context(), state, rc, &cache))
	promptBefore := state.Messages[0].Content

	skills.Load("docs")
	state.Messages = appendLoadSkillExchange(t, agent, state.Messages, "call-1", deliveringLoadResult())
	toolsAfter := toolNames(agent.refreshToolsAndPrompt(t.Context(), state, rc, &cache))

	if state.Messages[0].Content != promptBefore {
		t.Errorf("system prompt changed on load_skill — the cached prefix is lost:\nbefore: %q\nafter:  %q", promptBefore, state.Messages[0].Content)
	}
	if strings.Contains(promptBefore, renderInstructions) {
		t.Error("system prompt lists a skill tool's instructions: it would change whenever any session loads that skill")
	}
	if !slices.Contains(toolsBefore, "read_skill_resource") {
		t.Errorf("resource tools are hidden before a load (%v): loading the first skill with resources would rewrite the tool list", toolsBefore)
	}
	if want := append(slices.Clone(toolsBefore), "docs__render"); !slices.Equal(slices.Sorted(slices.Values(toolsAfter)), slices.Sorted(slices.Values(want))) {
		t.Errorf("tools after load = %v, want %v plus only the skill's own tool", toolsAfter, toolsBefore)
	}

	var nextTask promptToolCache
	nextState := &TaskExecutionState{Messages: slices.Clone(state.Messages)}
	agent.refreshToolsAndPrompt(t.Context(), nextState, rc, &nextTask)
	if nextState.Messages[0].Content != promptBefore {
		t.Error("the next task's system prompt differs from the one the load happened under — its cached prefix is lost")
	}
}

func TestSkillInstructionsFallBackToTheSystemPromptWhenTheHistoryLacksThem(t *testing.T) {
	agent := newSkillCacheAgent(t)
	user := llm.ChatMessage{Role: "user", Content: "draft the memo"}
	base := func() []llm.ChatMessage { return []llm.ChatMessage{{Role: "system"}, user} }
	cases := []struct {
		name    string
		history func() []llm.ChatMessage
		want    bool
	}{
		{"delivered by a load_skill result", func() []llm.ChatMessage {
			return appendLoadSkillExchange(t, agent, base(), "call-1", deliveringLoadResult())
		}, false},
		{"seeded without a load in the history (fresh sub-task, compacted history)", base, true},
		{"loaded before results carried instructions", func() []llm.ChatMessage {
			return appendLoadSkillExchange(t, agent, base(), "call-1",
				tool.OK("Loaded skill 'docs'. Its instructions and tools are now available."))
		}, true},
		{"load failed though its result carries instructions", func() []llm.ChatMessage {
			return appendLoadSkillExchange(t, agent, base(), "call-1", loadResult(tool.StatusError, docsInstructions))
		}, true},
		{"delivered an older version than the agent now holds (updated, then rehydrated)", func() []llm.ChatMessage {
			return appendLoadSkillExchange(t, agent, base(), "call-1", loadResult(tool.StatusSuccess, "Write in the old house style."))
		}, true},
		{"result shaped like a load but answering another tool's call", func() []llm.ChatMessage {
			msgs := appendLoadSkillExchange(t, agent, base(), "call-1", tool.OK("Loaded skill 'docs'."))
			return appendSkillToolExchange(t, agent, msgs, "read_skill_resource", "call-2", deliveringLoadResult())
		}, true},
		{"loaded, unloaded, and loaded again with no new result", func() []llm.ChatMessage {
			msgs := appendLoadSkillExchange(t, agent, base(), "call-1", deliveringLoadResult())
			return appendSkillToolExchange(t, agent, msgs, UnloadSkillToolName, "call-2", tool.OK("Unloaded skill 'docs'."))
		}, true},
		{"unloaded, then loaded again", func() []llm.ChatMessage {
			msgs := appendLoadSkillExchange(t, agent, base(), "call-1", deliveringLoadResult())
			msgs = appendSkillToolExchange(t, agent, msgs, UnloadSkillToolName, "call-2", tool.OK("Unloaded skill 'docs'."))
			return appendLoadSkillExchange(t, agent, msgs, "call-3", deliveringLoadResult())
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := &taskRunContext{skillState: &fakeSkillState{loaded: map[string]bool{"docs": true}}}
			state := &TaskExecutionState{Messages: tc.history()}
			var cache promptToolCache
			agent.refreshToolsAndPrompt(t.Context(), state, rc, &cache)
			prompt := state.Messages[0].Content
			if got := strings.Contains(prompt, docsInstructions); got != tc.want {
				t.Errorf("system prompt carries the skill's instructions = %v, want %v", got, tc.want)
			}
			if got := strings.Contains(prompt, renderInstructions); got != tc.want {
				t.Errorf("system prompt carries the skill's tool instructions = %v, want %v: they go wherever its instructions do", got, tc.want)
			}
			if tc.want && !strings.Contains(prompt, "supersede any that earlier load_skill results gave") {
				t.Error("prompt-carried instructions do not say they supersede the history's")
			}
		})
	}
}

func TestASkillWithoutInstructionsIsDeliveredByItsLoadResult(t *testing.T) {
	agent := newSkillCacheAgent(t)
	agent.SetSkillManager(&fakeSkillManager{instructions: map[string]string{"docs": ""}, toolOwner: map[string]string{"docs__render": "docs"}})
	rc := &taskRunContext{skillState: &fakeSkillState{loaded: map[string]bool{"docs": true}}}
	toolsOnly := &tool.Result{Status: tool.StatusSuccess, Message: "Loaded skill 'docs'."}
	toolsOnly.LLMData = map[string]any{LoadedSkillDataKey: "docs", ToolInstructionsDataKey: "**docs__render**: " + renderInstructions}
	state := &TaskExecutionState{Messages: appendLoadSkillExchange(t, agent,
		[]llm.ChatMessage{{Role: "system"}, {Role: "user", Content: "draft the memo"}}, "call-1", toolsOnly)}
	var cache promptToolCache

	agent.refreshToolsAndPrompt(t.Context(), state, rc, &cache)

	if strings.Contains(state.Messages[0].Content, renderInstructions) {
		t.Error("the skill's tool instructions are in the system prompt as well as its load result")
	}
	for _, notice := range []string{"Unavailable Skills", "Retracted Skill Instructions"} {
		if strings.Contains(state.Messages[0].Content, notice) {
			t.Errorf("a loaded skill delivered by its result gets a %q notice", notice)
		}
	}
}

const disregardDocsNotice = "These skills are no longer loaded: docs."

func TestRetractedInstructionsOfAHeldSkillAreFlaggedWithoutDroppingItsTools(t *testing.T) {
	agent := newSkillCacheAgent(t)
	agent.SetSkillManager(&fakeSkillManager{instructions: map[string]string{"docs": ""}})
	state := &TaskExecutionState{Messages: appendLoadSkillExchange(t, agent,
		[]llm.ChatMessage{{Role: "system"}, {Role: "user", Content: "draft the memo"}}, "call-1", deliveringLoadResult())}
	var cache promptToolCache

	agent.refreshToolsAndPrompt(t.Context(), state, &taskRunContext{skillState: &fakeSkillState{loaded: map[string]bool{"docs": true}}}, &cache)

	prompt := state.Messages[0].Content
	if !strings.Contains(prompt, "The instructions earlier load_skill results gave for these skills no longer apply: docs.") {
		t.Errorf("prompt does not retract docs' delivered instructions:\n%s", prompt)
	}
	if strings.Contains(prompt, disregardDocsNotice) {
		t.Error("a loaded, held skill is reported as no longer loaded")
	}
}

func TestDeliveredInstructionsAreDisregardedWhenTheAgentHasNoSkillsLeft(t *testing.T) {
	agent, _, _ := newTestAgent()
	state := &TaskExecutionState{Messages: appendLoadSkillExchange(t, agent,
		[]llm.ChatMessage{{Role: "system"}, {Role: "user", Content: "draft the memo"}}, "call-1", deliveringLoadResult())}
	var cache promptToolCache

	agent.refreshToolsAndPrompt(t.Context(), state, &taskRunContext{}, &cache)

	if !strings.Contains(state.Messages[0].Content, disregardDocsNotice) {
		t.Errorf("prompt lacks %q", disregardDocsNotice)
	}
}

func TestThePromptRebuildsWhenOnlyTheSkillStateBehindItChanges(t *testing.T) {
	agent := newSkillCacheAgent(t)
	manager := &fakeSkillManager{instructions: map[string]string{}, toolOwner: map[string]string{"docs__render": "docs"}}
	agent.SetSkillManager(manager)
	rc := &taskRunContext{skillState: &fakeSkillState{loaded: map[string]bool{"docs": true}}}
	state := &TaskExecutionState{Messages: appendLoadSkillExchange(t, agent,
		[]llm.ChatMessage{{Role: "system"}, {Role: "user", Content: "draft the memo"}}, "call-1", deliveringLoadResult())}
	var cache promptToolCache

	agent.refreshToolsAndPrompt(t.Context(), state, rc, &cache)
	if !strings.Contains(state.Messages[0].Content, disregardDocsNotice) {
		t.Fatalf("setup: a delivered skill the agent does not hold should be flagged")
	}

	manager.instructions["docs"] = docsInstructions
	agent.refreshToolsAndPrompt(t.Context(), state, rc, &cache)
	if strings.Contains(state.Messages[0].Content, disregardDocsNotice) {
		t.Error("the notice outlived the skill being held again")
	}

	manager.instructions["docs"] = "Write in the new house style."
	agent.refreshToolsAndPrompt(t.Context(), state, rc, &cache)
	if !strings.Contains(state.Messages[0].Content, "Write in the new house style.") {
		t.Error("a changed held copy did not reach the cached prompt")
	}
}

func TestDeliveredInstructionsOfASkillNoLongerLoadedAreDisregarded(t *testing.T) {
	agent := newSkillCacheAgent(t)
	user := llm.ChatMessage{Role: "user", Content: "draft the memo"}
	delivered := func() []llm.ChatMessage {
		return appendLoadSkillExchange(t, agent, []llm.ChatMessage{{Role: "system"}, user}, "call-1", deliveringLoadResult())
	}
	cases := []struct {
		name    string
		loaded  bool
		manager *fakeSkillManager
		history func() []llm.ChatMessage
		want    bool
	}{
		{"delivered, no longer loaded", false, nil, delivered, true},
		{"delivered and loaded", true, nil, delivered, false},
		{"never delivered, not loaded", false, nil, func() []llm.ChatMessage { return []llm.ChatMessage{{Role: "system"}, user} }, false},
		{"delivered, unloaded by unload_skill", false, nil, func() []llm.ChatMessage {
			return appendSkillToolExchange(t, agent, delivered(), UnloadSkillToolName, "call-2", tool.OK("Unloaded skill 'docs'."))
		}, false},
		{"delivered and listed as loaded, but the agent no longer holds it", true,
			&fakeSkillManager{toolOwner: map[string]string{"docs__render": "docs"}}, delivered, true},
		{"delivered and held, but its current copy has no instructions", true,
			&fakeSkillManager{instructions: map[string]string{"docs": ""}}, delivered, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject := agent
			if tc.manager != nil {
				subject = newSkillCacheAgent(t)
				subject.SetSkillManager(tc.manager)
			}
			assertDisregardNotice(t, subject, map[string]bool{"docs": tc.loaded}, tc.history(), tc.want)
		})
	}
}

func assertDisregardNotice(t *testing.T, agent *Agent, loaded map[string]bool, history []llm.ChatMessage, want bool) {
	t.Helper()
	state := &TaskExecutionState{Messages: history}
	var cache promptToolCache
	agent.refreshToolsAndPrompt(t.Context(), state, &taskRunContext{skillState: &fakeSkillState{loaded: loaded}}, &cache)
	if got := strings.Contains(state.Messages[0].Content, disregardDocsNotice); got != want {
		t.Errorf("system prompt carries %q = %v, want %v", disregardDocsNotice, got, want)
	}
}

type failingRehydrator struct{}

func (failingRehydrator) RehydrateSkills(_ context.Context, _ *tool.Context, names []string) []string {
	return names
}

func TestASkillThatFailedToRehydrateIsDisregardedInLaterTasksToo(t *testing.T) {
	agent := newSkillCacheAgent(t)
	agent.SetSkillRehydrator(failingRehydrator{})
	skills := &fakeSkillState{loaded: map[string]bool{"docs": true}}
	history := appendLoadSkillExchange(t, agent, []llm.ChatMessage{{Role: "system"}, {Role: "user", Content: "draft the memo"}}, "call-1", deliveringLoadResult())

	for task := 1; task <= 2; task++ {
		agent.rehydrateSkills(t.Context(), &tool.Context{SkillState: skills})
		state := &TaskExecutionState{Messages: slices.Clone(history)}
		var cache promptToolCache
		agent.refreshToolsAndPrompt(t.Context(), state, &taskRunContext{skillState: skills}, &cache)
		if !strings.Contains(state.Messages[0].Content, disregardDocsNotice) {
			t.Errorf("task %d: system prompt lacks %q", task, disregardDocsNotice)
		}
	}
}

func TestSystemPromptRegainsInstructionsCompactedOutOfTheHistory(t *testing.T) {
	agent := newSkillCacheAgent(t)
	rc := &taskRunContext{skillState: &fakeSkillState{loaded: map[string]bool{"docs": true}}}
	user := llm.ChatMessage{Role: "user", Content: "draft the memo"}
	state := &TaskExecutionState{Messages: appendLoadSkillExchange(t, agent, []llm.ChatMessage{{Role: "system"}, user}, "call-1", deliveringLoadResult())}
	var cache promptToolCache
	agent.refreshToolsAndPrompt(t.Context(), state, rc, &cache)

	state.Messages = []llm.ChatMessage{{Role: "system"}, {Role: "assistant", Content: "summary of earlier turns"}, user}
	agent.refreshToolsAndPrompt(t.Context(), state, rc, &cache)

	if !strings.Contains(state.Messages[0].Content, docsInstructions) {
		t.Error("instructions compacted out of the history are in neither the history nor the system prompt")
	}
}

func TestASkillToolIsRefusedToASessionThatHasNotLoadedTheSkill(t *testing.T) {
	agent := newSkillCacheAgent(t)
	ran := false
	if err := agent.Tools().Register(&testBuiltinTool{name: "docs__lint", description: "lint",
		execFn: func(context.Context, map[string]any, *tool.Context) (*tool.Result, error) {
			ran = true
			return tool.OK("linted"), nil
		}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	agent.skillManager.(*fakeSkillManager).toolOwner["docs__lint"] = "docs"
	sp := NewStatusPublisher(nil, StatusPublisherConfig{TaskID: "task-1"}, slog.Default())
	call := []llm.ToolCall{{ID: "c1", Name: "docs__lint", Arguments: `{}`}}

	results := agent.dispatchToolCallsWithStatus(t.Context(), call,
		&tool.Context{SkillState: &fakeSkillState{loaded: map[string]bool{}}}, sp, "")
	if ran || results[0].Status != tool.StatusError || !strings.Contains(results[0].Message, "load_skill") {
		t.Errorf("ran = %v, result = %+v; want the call refused with a pointer to load_skill", ran, results[0])
	}

	results = agent.dispatchToolCallsWithStatus(t.Context(), call,
		&tool.Context{SkillState: &fakeSkillState{loaded: map[string]bool{"docs": true}}}, sp, "")
	if !ran || results[0].Status != tool.StatusSuccess {
		t.Errorf("ran = %v, result = %+v; want the call run for a session that loaded the skill", ran, results[0])
	}
}

func TestAToolOfTwoSkillsServesASessionThatLoadedEither(t *testing.T) {
	agent := newSkillCacheAgent(t)
	if err := agent.Tools().Register(&testBuiltinTool{name: "get_current_time", description: "time"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	agent.skillManager.(*fakeSkillManager).toolOwners = map[string][]string{"get_current_time": {"docs", "notes"}}
	sp := NewStatusPublisher(nil, StatusPublisherConfig{TaskID: "task-1"}, slog.Default())
	call := []llm.ToolCall{{ID: "c1", Name: "get_current_time", Arguments: `{}`}}

	notesOnly := &fakeSkillState{loaded: map[string]bool{"notes": true}}
	if tools := toolNames(agent.buildLLMTools(t.Context(), nil, notesOnly, "")); !slices.Contains(tools, "get_current_time") {
		t.Errorf("tools = %v, want get_current_time for a session that loaded notes", tools)
	}
	results := agent.dispatchToolCallsWithStatus(t.Context(), call, &tool.Context{SkillState: notesOnly}, sp, "")
	if results[0].Status != tool.StatusSuccess {
		t.Errorf("result = %+v, want the call run for a session that loaded notes", results[0])
	}

	neither := &fakeSkillState{loaded: map[string]bool{}}
	if tools := toolNames(agent.buildLLMTools(t.Context(), nil, neither, "")); slices.Contains(tools, "get_current_time") {
		t.Errorf("tools = %v, want get_current_time hidden from a session that loaded neither skill", tools)
	}
	results = agent.dispatchToolCallsWithStatus(t.Context(), call, &tool.Context{SkillState: neither}, sp, "")
	if results[0].Status != tool.StatusError || !strings.Contains(results[0].Message, `"docs", "notes"`) {
		t.Errorf("result = %+v, want a refusal naming both skills", results[0])
	}
}

func TestASkillToolAddedAfterTheLoadHasItsInstructionsInTheSystemPrompt(t *testing.T) {
	agent := newSkillCacheAgent(t)
	const exportInstructions = "Export as PDF only."
	if err := agent.Tools().Register(&testBuiltinTool{name: "docs__export", description: "export", instructions: exportInstructions}); err != nil {
		t.Fatalf("register: %v", err)
	}
	agent.skillManager.(*fakeSkillManager).toolOwner["docs__export"] = "docs"
	rc := &taskRunContext{skillState: &fakeSkillState{loaded: map[string]bool{"docs": true}}}
	state := &TaskExecutionState{Messages: appendLoadSkillExchange(t, agent,
		[]llm.ChatMessage{{Role: "system"}, {Role: "user", Content: "draft the memo"}}, "call-1", deliveringLoadResult())}
	var cache promptToolCache

	agent.refreshToolsAndPrompt(t.Context(), state, rc, &cache)

	prompt := state.Messages[0].Content
	if !strings.Contains(prompt, ToolInstructionLine("docs__export", exportInstructions)) {
		t.Error("the system prompt lacks the instructions of a tool the load result did not deliver")
	}
	if strings.Contains(prompt, renderInstructions) {
		t.Error("the system prompt repeats a tool's instructions the load result delivered")
	}
}
