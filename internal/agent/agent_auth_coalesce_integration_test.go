// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func coalesceAuthExecFn(credKey string, signalCount *atomic.Int32, credentialReady *atomic.Bool) stubExecFn {
	return func(_ context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
		if credentialReady.Load() {
			return tool.OK("re-executed ok"), nil
		}
		if toolCtx.BeginAuthFlow != nil && !toolCtx.BeginAuthFlow(toolCtx.UserID, toolCtx.TraceID, credKey) {
			return toolauth.NewPendingAuthFollowerResult(toolCtx.ToolCallID, args, credKey), nil
		}
		if toolCtx.SendSignal != nil {
			_ = toolCtx.SendSignal(&a2a.AuthenticationRequiredData{
				Type:                      string(a2a.SignalAuthenticationRequired),
				CredentialKey:             credKey,
				AuthRequestFunctionCallID: toolCtx.ToolCallID,
				UserID:                    toolCtx.UserID,
			})
			signalCount.Add(1)
		}
		return toolauth.NewPendingAuthResult(toolCtx.ToolCallID, args, credKey), nil
	}
}

func TestConcurrentMCPAuthCoalescing_OnePromptThenBothResume(t *testing.T) {
	var resumed sync.Map
	useResumeHook(t, func(_ *Agent, s *TaskExecutionState) {
		v, _ := resumed.LoadOrStore(s.TaskID, new(atomic.Int32))
		v.(*atomic.Int32).Add(1)
	})

	var signalCount atomic.Int32
	var credentialReady atomic.Bool
	agent := newTestAgentWithStubTool(t, "authtool",
		coalesceAuthExecFn("shared-cred", &signalCount, &credentialReady))
	registerStubAuthHandler(t, agent, "shared-cred")

	state := newTestTaskState(agent)
	state.TaskID = "task-concurrent"
	state.UserProps.UserID = "u1"

	toolCtx := &tool.Context{
		AgentName:     agent.cfg.Name,
		UserID:        state.UserProps.UserID,
		SessionID:     state.EffectiveSessionID,
		BeginAuthFlow: agent.beginAuthFlow,
		SendSignal:    func(any) error { return nil },
	}
	sp := defaultPauseCallArgs(agent, state).sp

	calls := []llm.ToolCall{
		{ID: "call-A", Name: "authtool", Arguments: `{}`},
		{ID: "call-B", Name: "authtool", Arguments: `{}`},
	}

	_, paused := agent.dispatchToolCallsMayPause(t.Context(), state, calls, toolCtx, sp, "", nil, agent.logger)
	if !paused {
		t.Fatal("expected task to pause (both calls pending auth)")
	}

	if got := signalCount.Load(); got != 1 {
		t.Fatalf("authentication_required signals = %d, want exactly 1 (one leader, one coalesced follower)", got)
	}

	state.mu.Lock()
	if len(state.PendingCalls) != 2 {
		t.Fatalf("PendingCalls = %d, want 2", len(state.PendingCalls))
	}
	var leaders, followers int
	leaderCallID := ""
	for _, pc := range state.PendingCalls {
		if pc.Kind != PendingCallAuth {
			t.Errorf("call %s Kind = %v, want PendingCallAuth", pc.ToolCallID, pc.Kind)
		}
		if pc.CredentialKey != "shared-cred" {
			t.Errorf("call %s CredentialKey = %q, want shared-cred", pc.ToolCallID, pc.CredentialKey)
		}
		if pc.IsAuthFollower {
			followers++
		} else {
			leaders++
			leaderCallID = pc.ToolCallID
		}
	}
	state.mu.Unlock()
	if leaders != 1 || followers != 1 {
		t.Fatalf("want exactly 1 leader + 1 follower, got %d leaders / %d followers", leaders, followers)
	}

	state.PausedAssistantMsg = llm.ChatMessage{
		Role: "assistant",
		ToolCalls: []llm.ToolCall{
			{ID: "call-A", Name: "authtool", Arguments: `{}`},
			{ID: "call-B", Name: "authtool", Arguments: `{}`},
		},
	}

	credentialReady.Store(true)

	resp := authResp("task-concurrent", leaderCallID, "shared-cred")
	got, err := agent.handleAuthResponse(t.Context(), "task-concurrent", resp, a2a.UserProperties{UserID: "u1"})
	if err != nil {
		t.Fatalf("handleAuthResponse: %v", err)
	}
	if got != nil {
		t.Errorf("want nil response (task resumed in-place), got %+v", got)
	}

	state.mu.Lock()
	if len(state.PendingCalls) != 0 {
		t.Errorf("PendingCalls = %d after resume, want 0 (both claimed)", len(state.PendingCalls))
	}
	for i := range 2 {
		r := state.SyncResults[i]
		if r == nil || r.Status != tool.StatusSuccess {
			t.Errorf("SyncResults[%d] = %+v, want success", i, r)
		}
	}
	state.mu.Unlock()

	if n := atomic.LoadInt32(&state.TotalPending); n != 0 {
		t.Errorf("TotalPending = %d, want 0", n)
	}
	if v, ok := resumed.Load("task-concurrent"); !ok || v.(*atomic.Int32).Load() != 1 {
		t.Errorf("task should resume exactly once after both pending calls resolve")
	}
	if !agent.beginAuthFlow("u1", "", "shared-cred") {
		t.Error("in-flight flag should be cleared after the coalesced flow resolved")
	}
}
