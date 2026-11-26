// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func promptingAuthExecFn(credKey string, afterSend func()) stubExecFn {
	return func(_ context.Context, args map[string]any, toolCtx *tool.Context) (*tool.Result, error) {
		if err := toolCtx.SendSignal(&a2a.AuthenticationRequiredData{
			Type:                      string(a2a.SignalAuthenticationRequired),
			CredentialKey:             credKey,
			AuthRequestFunctionCallID: toolCtx.ToolCallID,
			UserID:                    toolCtx.UserID,
		}); err != nil {
			return nil, err
		}
		if afterSend != nil {
			afterSend()
		}
		return toolauth.NewPendingAuthResult(toolCtx.ToolCallID, args, credKey), nil
	}
}

func TestAuthPromptIsSentOnlyOnceTheTaskCanTakeTheAnswer(t *testing.T) {
	agent := newTestAgentWithStubTool(t, "authtool", promptingAuthExecFn("cred", nil))
	state := newTestTaskState(agent)
	pca := defaultPauseCallArgs(agent, state)

	var prompts int
	var answerable bool
	pca.toolCtx.SendSignal = func(signal any) error {
		if _, ok := signal.(*a2a.AuthenticationRequiredData); !ok {
			return nil
		}
		prompts++
		if paused := agent.pausedTasks.Load(state.TaskID); paused != nil {
			paused.mu.Lock()
			_, answerable = paused.PendingCalls["call-auth"]
			paused.mu.Unlock()
		}
		return nil
	}

	_, paused := agent.dispatchToolCallsMayPause(t.Context(), state,
		singleCall("authtool", "call-auth", "{}"),
		pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, agent.logger)

	if !paused {
		t.Fatal("expected the task to pause on the pending auth call")
	}
	if prompts != 1 {
		t.Fatalf("authentication_required prompts sent = %d, want 1", prompts)
	}
	if !answerable {
		t.Error("the prompt left the agent before the task was in pausedTasks with its pending auth call — an immediate auth_response would find nothing to resume")
	}
}

func TestAuthPromptIsDroppedWhenTheTaskIsCancelledBeforeItPauses(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	agent := newTestAgentWithStubTool(t, "authtool", promptingAuthExecFn("cred", cancel))
	state := newTestTaskState(agent)
	pca := defaultPauseCallArgs(agent, state)

	var prompts int
	pca.toolCtx.SendSignal = func(signal any) error {
		if _, ok := signal.(*a2a.AuthenticationRequiredData); ok {
			prompts++
		}
		return nil
	}

	agent.dispatchToolCallsMayPause(ctx, state,
		singleCall("authtool", "call-auth", "{}"),
		pca.toolCtx, pca.sp, pca.parallelGroupID, pca.sanitizeErrors, agent.logger)

	if prompts != 0 {
		t.Errorf("authentication_required prompts sent = %d, want 0 for a task cancelled before it paused", prompts)
	}
}
