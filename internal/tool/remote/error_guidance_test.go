// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestGuidanceForCode(t *testing.T) {
	t.Parallel()

	processFailures := []remotetool.ErrorCode{
		remotetool.ErrToolTimeout,
		remotetool.ErrToolExitNonzero,
		remotetool.ErrToolSignaled,
		remotetool.ErrToolExecFailed,
		remotetool.ErrToolMemoryExceeded,
	}
	for _, code := range processFailures {
		t.Run(string(code), func(t *testing.T) {
			t.Parallel()
			got := guidanceForCode(code)
			if got == "" {
				t.Fatalf("%s is a process failure but carries no guidance", code)
			}
			if !strings.Contains(got, "Do NOT retry") && !strings.Contains(got, "Do NOT retry this call") {
				t.Errorf("guidance for %s lacks an explicit non-retry directive: %q", code, got)
			}
			for _, banned := range []string{"transient", "temporary"} {
				if strings.Contains(strings.ToLower(got), banned) {
					if !strings.Contains(strings.ToLower(got), "not a "+banned) {
						t.Errorf("guidance for %s characterizes the failure as %s: %q", code, banned, got)
					}
				}
			}
			if !isToolProcessFailure(code) {
				t.Errorf("%s has guidance but isToolProcessFailure says it is recoverable; the two must agree", code)
			}
		})
	}

	recoverable := []remotetool.ErrorCode{
		remotetool.ErrExecutionError,
		remotetool.ErrToolError,
		remotetool.ErrToolNotAvailable,
	}
	for _, code := range recoverable {
		t.Run("recoverable/"+string(code), func(t *testing.T) {
			t.Parallel()
			if got := guidanceForCode(code); got != "" {
				t.Errorf("guidanceForCode(%s) = %q, want empty so the LLM may still recover", code, got)
			}
			if isToolProcessFailure(code) {
				t.Errorf("%s has no guidance but isToolProcessFailure says it is fatal; the two must agree", code)
			}
		})
	}

	t.Run("sandbox timeout guidance explicitly denies transience without being fatal", func(t *testing.T) {
		t.Parallel()
		got := guidanceForCode(remotetool.ErrSandboxTimeout)
		if got == "" {
			t.Fatal("SANDBOX_TIMEOUT carries no guidance; a wait-window expiry would read as transient")
		}
		lower := strings.ToLower(got)
		if !strings.Contains(lower, "not a transient") && !strings.Contains(lower, "not describe it as a temporary") {
			t.Errorf("guidance does not explicitly deny that the failure is transient: %q", got)
		}
		if isToolProcessFailure(remotetool.ErrSandboxTimeout) {
			t.Error("SANDBOX_TIMEOUT must stay LLM-recoverable (StatusError), not become StatusFailed")
		}
	})
}

func TestTool_Execute_Timeout_PopulatesGuidance(t *testing.T) {
	t.Parallel()
	exe, brk := newTestExecutor(t)
	startExecutor(t, exe)

	simulateSTR(t, brk, "slow_tool", func(corrID, replyTo, _ string) {
		simulateSTRError(brk, corrID, replyTo, remotetool.InvokeError{
			Code:    remotetool.ErrToolTimeout,
			Message: `tool "slow_tool" timed out after 300s`,
		})
	})

	remoteTool := NewTool(&ToolConfig{ToolName: "slow_tool", TimeoutSeconds: 5}, exe)
	result, err := remoteTool.Execute(t.Context(), map[string]any{}, &tool.Context{
		AgentName: "test-agent",
		UserID:    "user-1",
		SessionID: "sess-1",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Status != tool.StatusFailed {
		t.Fatalf("status = %q, want %q", result.Status, tool.StatusFailed)
	}
	if result.FailedDetail == nil {
		t.Fatal("FailedDetail is nil")
	}
	if result.FailedDetail.Guidance == "" {
		t.Error("TOOL_TIMEOUT result carries no Guidance; the LLM has nothing telling it not to retry")
	}
	if !strings.Contains(result.FailedDetail.Guidance, "Do NOT retry") {
		t.Errorf("guidance lacks a non-retry directive: %q", result.FailedDetail.Guidance)
	}
}
