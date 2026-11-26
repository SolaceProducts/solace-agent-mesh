// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type deliveredCall struct {
	state        *TaskExecutionState
	pc           *PendingToolCall
	toolCtx      *tool.Context
	dispatchedAt time.Time
	result       *tool.Result
	remaining    int32
}

func (a *Agent) settleDeliveredCall(ctx context.Context, d deliveredCall) {
	resolvedElsewhere := a.bankDeliveredResult(ctx, d)
	if d.remaining == 0 && d.state.passPublishHandoff() {
		a.ResumeTask(d.state.TaskID)
	}
	if d.pc.Kind == PendingCallRemoteTool && !resolvedElsewhere {
		a.reportDeliveredCall(ctx, d)
	}
}

func (a *Agent) bankDeliveredResult(ctx context.Context, d deliveredCall) (resolvedElsewhere bool) {
	defer d.state.durableWrites.Done()
	defer a.logDeliveryPanic("recording a delivered tool result panicked; resuming the task with the result in hand", d)
	resolvedElsewhere = a.checkpoints.recordResult(ctx, d.state.TaskID, d.pc, d.result)
	if resolvedElsewhere {
		a.useRecordedResult(ctx, d.state, d.pc)
	}
	return resolvedElsewhere
}

func (a *Agent) reportDeliveredCall(ctx context.Context, d deliveredCall) {
	defer a.logDeliveryPanic("reporting a delivered remote tool call panicked; its audit event or metric may be missing", d)
	a.reportDeliveredRemoteToolCall(ctx, d.pc.ToolName, d.toolCtx.Caller(), d.toolCtx.SessionID, d.toolCtx.TraceID, d.dispatchedAt, d.result)
}

func (a *Agent) logDeliveryPanic(msg string, d deliveredCall) {
	recovered := recover()
	if recovered == nil {
		return
	}
	a.logger.Error(msg,
		"traceID", d.state.UserProps.TraceID, "taskID", d.state.TaskID,
		"toolCallID", d.pc.ToolCallID, "toolName", d.pc.ToolName,
		"panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
}
