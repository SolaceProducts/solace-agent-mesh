// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const rehydrateUserToken = "user-token"

type tokenAuthenticator struct{}

func (tokenAuthenticator) Authenticate(_ context.Context, token, _ string) (*auth.Identity, error) {
	return &auth.Identity{UserID: inlineTestUser, AuthToken: token}, nil
}

type recordingRehydrator struct{ toolCtx *tool.Context }

func (r *recordingRehydrator) RehydrateSkills(_ context.Context, toolCtx *tool.Context, _ []string) []string {
	r.toolCtx = toolCtx
	return nil
}

func withDocsLoadedSessions(t *testing.T) {
	t.Helper()
	prev := SkillStateFactory
	SkillStateFactory = func(*session.Session) tool.SkillStateProvider {
		return &fakeSkillState{loaded: map[string]bool{"docs": true}}
	}
	t.Cleanup(func() { SkillStateFactory = prev })
}

func requireRehydratedAsTheUser(t *testing.T, rehydrator *recordingRehydrator) {
	t.Helper()
	if rehydrator.toolCtx == nil {
		t.Fatal("the session's skills were not rehydrated")
	}
	if id := rehydrator.toolCtx.Identity; id == nil || id.AuthToken != rehydrateUserToken {
		t.Fatalf("rehydrated with identity %+v, want the task's token", id)
	}
}

func TestATaskRehydratesItsSessionsSkillsAsItsUser(t *testing.T) {
	withDocsLoadedSessions(t)
	agent := newSkillCacheAgent(t)
	svc := agent.svc.(*mockServices)
	svc.authn = tokenAuthenticator{}
	svc.arts = &artifact.ScopedService{Inner: artmem.New(), Scope: artifact.ScopeNamespace, Namespace: "test-ns"}
	rehydrator := &recordingRehydrator{}
	agent.SetSkillRehydrator(rehydrator)

	props := a2a.UserProperties{UserID: inlineTestUser, AuthToken: rehydrateUserToken}
	_, _, earlyResp, err := agent.setupTask(t.Context(), "task-1", newUserMessage("hi"), props, taskDelivery{})
	if err != nil || earlyResp != nil {
		t.Fatalf("setupTask: err=%v earlyResp=%+v", err, earlyResp)
	}
	requireRehydratedAsTheUser(t, rehydrator)
}

func TestAResumedTaskRehydratesItsSessionsSkillsAsItsUser(t *testing.T) {
	withDocsLoadedSessions(t)
	agent := newSkillCacheAgent(t)
	agent.runCtx = t.Context()
	rehydrator := &recordingRehydrator{}
	agent.SetSkillRehydrator(rehydrator)
	sess := &session.Session{AppName: agent.cfg.Name, UserID: inlineTestUser, SessionID: "s-resume", ContextID: "s-resume"}
	if err := agent.svc.Sessions().Create(t.Context(), sess); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	state := newTestTaskState(agent)
	state.TaskID = "task-resume"
	state.EffectiveSessionID = "s-resume"
	state.UserProps.UserID = inlineTestUser
	state.Identity = &auth.Identity{UserID: inlineTestUser, AuthToken: rehydrateUserToken}
	state.PausedAssistantMsg = assistantMsgWithOneToolCall("call-r1", "peer", `{}`)
	state.SyncResults[0] = tool.OK("peer answered")
	state.Messages = []llm.ChatMessage{{Role: "user", Content: "ask peer"}, state.PausedAssistantMsg}
	agent.pausedTasks.Store(state)

	agent.resumeTaskExecution(state)
	requireRehydratedAsTheUser(t, rehydrator)
}

func TestATaskThatEndsDuringRehydrationKeepsTheSessionsSkills(t *testing.T) {
	agent := newSkillCacheAgent(t)
	agent.SetSkillRehydrator(failingRehydrator{})
	skills := &fakeSkillState{loaded: map[string]bool{"docs": true}}
	var endedTask context.Context
	t.Run("task", func(t *testing.T) { endedTask = t.Context() })

	agent.rehydrateSkills(endedTask, &tool.Context{SkillState: skills})

	if !skills.IsLoaded("docs") {
		t.Error("a load cut short by the task ending unloaded the skill from the session")
	}
}
