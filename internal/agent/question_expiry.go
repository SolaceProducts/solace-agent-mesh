// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"log/slog"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func expiredQuestionResult(pending *session.PendingCall, cp *session.TaskCheckpoint, logger *slog.Logger) *tool.Result {
	if pending.Kind != session.PendingKindHILApproval {
		return nil
	}
	state, err := unmarshalTaskState(cp.State)
	if err != nil {
		logger.Warn("could not decode the checkpoint of an expired call; giving it the generic timeout result", "error", err)
		return nil
	}
	question := state.PendingCalls[pending.ToolCallID]
	if question == nil || question.Kind != PendingCallHILAsk {
		return nil
	}
	return hilResultFor(logger, question, &a2a.UserInputResponseData{
		RequestID: question.HILRequestID,
		Status:    "timed_out",
		Data:      map[string]any{},
	})
}
