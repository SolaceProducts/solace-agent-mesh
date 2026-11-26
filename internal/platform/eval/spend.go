// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const evalClientID = a2a.EvalGatewayID

func judgeSpend(task *pendingTask) *llm.SpendAttribution {
	attribution := &llm.SpendAttribution{
		UserID:         rbac.EvalSystemUserID,
		EntrypointName: evalClientID,
		Channel:        a2a.ChannelEval,
		TriggerKind:    string(spend.TriggerEval),
		Surface:        string(spend.SurfaceEvalJudge),
	}
	if task != nil {
		attribution.TaskID = task.taskID
		attribution.SessionID = task.taskID
		attribution.TraceID = task.traceID
	}
	return attribution
}
