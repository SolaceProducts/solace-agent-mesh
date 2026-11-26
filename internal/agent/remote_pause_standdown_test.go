// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
)

func TestStandingDownAtThePauseOfARemoteCallLeavesNothingTrackedInItsExecutor(t *testing.T) {
	replica, mock, _ := replicaOf(t, correlationsRefusingStore{Store: session.NewMemoryStore()}, "frozen")
	exe := registerRemoteTool(t, replica, strBroker(t))
	mock.responses = []*llm.ChatResponse{singleToolCallResponse("call-r1", remoteReportTool)}

	resp, err := replica.HandleTask(t.Context(), "task-standdown", concUserMessage("build the report", idgen.New()), concurrentUser)

	if err != nil || resp != nil {
		t.Fatalf("HandleTask() = %v, %v, want a stand-down with no response", resp, err)
	}
	if n := exe.PausedCalls(); n != 0 {
		t.Errorf("paused calls tracked by the executor after the stand-down = %d, want 0", n)
	}
}
