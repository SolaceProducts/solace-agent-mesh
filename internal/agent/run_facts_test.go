// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestHandleTask_FinalRowCarriesFinalAndUsage(t *testing.T) {
	agent, _, store := newTestAgent(&llm.ChatResponse{
		Content:      "done",
		FinishReason: "stop",
		Usage:        &llm.Usage{PromptTokens: 120, CompletionTokens: 7, TotalTokens: 127},
	})

	if _, err := agent.HandleTask(t.Context(), "task-facts", newUserMessage("hi"),
		a2a.UserProperties{ClientID: "user-1", UserID: "user-1"}); err != nil {
		t.Fatalf("HandleTask: %v", err)
	}

	sess, err := store.Get(t.Context(), agent.cfg.Name, "user-1", "ctx-1")
	if err != nil || sess == nil {
		t.Fatalf("load session: %v", err)
	}
	last := sess.Messages[len(sess.Messages)-1]
	if last.Role != "assistant" || last.RunFacts == nil || !last.RunFacts.Final {
		t.Fatalf("last row = %+v, want the final answer marked Final", last)
	}
	if got := len(last.RunFacts.Usage); got != 1 {
		t.Fatalf("usage entries on the final row = %d, want 1", got)
	}
	var u llm.Usage
	if err := json.Unmarshal(last.RunFacts.Usage[0].Tokens, &u); err != nil || u.PromptTokens != 120 || u.CompletionTokens != 7 {
		t.Fatalf("stored usage = %+v (%v), want the call's usage", u, err)
	}
	for _, m := range sess.Messages[:len(sess.Messages)-1] {
		if m.RunFacts != nil {
			t.Fatalf("row %q carries facts that belong to the final row", m.Content)
		}
	}
}

func TestFlush_RunFactsRideOnTheLastRowOfTheBatch(t *testing.T) {
	agent, _, store := newTestAgent()
	sess := seedFlushSession(t, agent, store)
	state := newTestTaskState(agent)
	state.EffectiveSessionID, state.UserProps.UserID = "s1", "u1"

	state.recordCallUsage("m1", llm.Usage{PromptTokens: 10})
	state.toolLLMUsage.record("m2", llm.Usage{PromptTokens: 3})
	state.foldToolLLMUsage()
	state.recordToolSave(tool.ArtifactSavedInfo{Filename: "report.pdf", Version: 2})
	state.recordCreatedArtifacts([]artifactCreationRecord{{Filename: "notes.md", Version: 0}})
	recordToolFailure(state, &tool.Result{Status: tool.StatusFailed, FailedDetail: &tool.ToolFailedDetail{Code: "TOOL_EXEC_FAILED"}}, agent.logger)
	state.Messages = []llm.ChatMessage{
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "save"}}},
		{Role: "tool", ToolCallID: "c1", ToolName: "save", Content: "saved"},
	}

	if err := agent.flushSessionMessages(t.Context(), state, &taskRunContext{sess: sess}); err != nil {
		t.Fatalf("flush: %v", err)
	}

	saved := loadFlushSession(t, agent, store)
	if saved.Messages[0].RunFacts != nil {
		t.Fatal("facts landed on a row other than the last")
	}
	if saved.Messages[1].ToolName != "save" {
		t.Fatalf("tool row ToolName = %q, want save", saved.Messages[1].ToolName)
	}
	f := saved.Messages[1].RunFacts
	if f == nil {
		t.Fatal("last row carries no facts")
	}
	if len(f.Usage) != 2 || f.Usage[0].Source != usageSourceAgent || f.Usage[1].Source != usageSourceTool {
		t.Fatalf("usage = %+v, want one agent call then one tool call", f.Usage)
	}
	if len(f.Files) != 2 || f.Files[0].Filename != "report.pdf" || f.Files[0].Source != producedBySourceTool ||
		f.Files[1].Filename != "notes.md" || f.Files[1].Source != producedBySourceBlock {
		t.Fatalf("files = %+v", f.Files)
	}
	if f.Failure == nil || f.Failure.Code != "TOOL_EXEC_FAILED" {
		t.Fatalf("failure = %+v", f.Failure)
	}
	if f.Final {
		t.Fatal("a mid-task row is marked Final")
	}
}

func TestFlush_RunFactsOfAFailedAppendAreStoredOnceOnRetry(t *testing.T) {
	agent, _, _ := newTestAgent()
	store := newSQLiteSessionStore(t)
	sess := &session.Session{AppName: agent.cfg.Name, UserID: "u1", SessionID: "s1", ContextID: "s1"}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed: %v", err)
	}
	fs := &failingAppendStore{Store: store}
	fs.fail.Store(true)
	agent.svc.(*mockServices).sessions = fs

	state := newTestTaskState(agent)
	state.EffectiveSessionID, state.UserProps.UserID = "s1", "u1"
	state.recordCallUsage("m1", llm.Usage{PromptTokens: 10})
	state.Messages = []llm.ChatMessage{{Role: "user", Content: "q"}}
	rc := &taskRunContext{sess: sess}
	if err := agent.flushSessionMessages(t.Context(), state, rc); err == nil {
		t.Fatal("expected the first flush to fail")
	}

	fs.fail.Store(false)
	state.recordCallUsage("m1", llm.Usage{PromptTokens: 20})
	state.Messages = append(state.Messages, llm.ChatMessage{Role: "assistant", Content: "a"})
	if err := agent.flushSessionMessages(t.Context(), state, rc); err != nil {
		t.Fatalf("retry flush: %v", err)
	}

	saved, err := store.Get(t.Context(), agent.cfg.Name, "u1", "s1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var usage int
	for _, m := range saved.Messages {
		if m.RunFacts != nil {
			usage += len(m.RunFacts.Usage)
		}
	}
	if usage != 2 {
		t.Fatalf("stored usage entries = %d, want 2 (one per call, none lost or doubled)", usage)
	}
}

func TestSameMessagesIgnoresRunFacts(t *testing.T) {
	a := []session.Message{{Role: "tool", Content: "x", RunFacts: &session.RunFacts{Final: true}}}
	b := []session.Message{{Role: "tool", Content: "x"}}
	if !session.SameMessages(a, b) {
		t.Fatal("rows differing only in RunFacts must compare equal, or a landed append is written twice")
	}
}

func TestRunFacts_QuietIsStoredUnderItsOwnKeyAndOldRowsReadAsNotQuiet(t *testing.T) {
	stored, err := json.Marshal(session.RunFacts{Final: true, Quiet: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got, want := string(stored), `{"Final":true,"Quiet":true}`; got != want {
		t.Fatalf("stored facts = %s, want %s", got, want)
	}
	var older session.RunFacts
	if err := json.Unmarshal([]byte(`{"Final":true}`), &older); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if older.Quiet {
		t.Fatal(`a row stored without "Quiet" must read as not quiet`)
	}
}

func TestPendingRunFacts_GiveBackKeepsOrder(t *testing.T) {
	var p pendingRunFacts
	p.addUsage("m", usageSourceAgent, llm.Usage{PromptTokens: 1})
	first := p.take()
	p.addUsage("m", usageSourceAgent, llm.Usage{PromptTokens: 2})
	p.giveBack(first)
	got := p.take()
	if got == nil || len(got.Usage) != 2 {
		t.Fatalf("after giveBack: %+v", got)
	}
	var u llm.Usage
	_ = json.Unmarshal(got.Usage[0].Tokens, &u)
	if u.PromptTokens != 1 {
		t.Fatalf("given-back usage is not first: %+v", u)
	}
	if p.take() != nil {
		t.Fatal("take after take returned facts")
	}
}

func TestPendingRunFacts_GiveBackKeepsTheQuietFinalWhenItsAppendIsUnknown(t *testing.T) {
	var p pendingRunFacts
	p.markFinal("Nothing to report.", "Nothing to report.")
	p.markQuiet()
	unconfirmed := p.take()
	p.addUsage("m", usageSourceAgent, llm.Usage{PromptTokens: 2})

	p.giveBack(unconfirmed)

	got := p.take()
	if got == nil || !got.Final || !got.Quiet || len(got.Usage) != 1 {
		t.Fatalf("facts after giveBack = %+v, want the final and quiet markers kept beside the newer usage", got)
	}
}

func seedFlushSession(t *testing.T, agent *Agent, store session.Store) *session.Session {
	t.Helper()
	sess := &session.Session{AppName: agent.cfg.Name, UserID: "u1", SessionID: "s1", ContextID: "s1"}
	if err := store.Create(t.Context(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return sess
}

func loadFlushSession(t *testing.T, agent *Agent, store session.Store) *session.Session {
	t.Helper()
	saved, err := store.Get(t.Context(), agent.cfg.Name, "u1", "s1")
	if err != nil || saved == nil {
		t.Fatalf("load session: %v", err)
	}
	return saved
}
