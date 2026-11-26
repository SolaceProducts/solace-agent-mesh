// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

func (e *DAGExecutor) metered(call decisionCall) decision.Evaluator {
	var attribution *llm.SpendAttribution
	if e.instance.spendPublisher != nil || e.instance.budgetGate() != nil {
		attribution = e.decisionSpendAttribution(call)
	}
	return spend.NewMeteredEvaluator(call.evaluator, e.instance.spendPublisher, attribution, e.instance.budgetGate())
}

func (e *DAGExecutor) decisionSpendAttribution(call decisionCall) *llm.SpendAttribution {
	attribution := e.nodeSpendAttribution(call.node.ID, call.topLevelNodeID)
	attribution.Surface = string(spend.SurfaceDecision)
	attribution.ModelKey = e.instance.decisionModelKey()
	return attribution
}

func (e *DAGExecutor) toolNodeSpend(nodeID, topLevelNodeID string) *remotetool.SpendAttribution {
	return agent.RemoteToolSpend(e.nodeSpendAttribution(nodeID, topLevelNodeID))
}

func (e *DAGExecutor) nodeSpendAttribution(nodeID, topLevelNodeID string) *llm.SpendAttribution {
	props := e.execCtx.UserProps
	origin := e.execCtx.SpendOrigin
	triggerKind, triggerID := agent.SpendTrigger(props, origin)
	workflowID, workflowName := agent.ComponentIdentity(e.cfg.Name)
	rootKind, root, rootNodeID := e.root(topLevelNodeID)
	rootID, rootName := agent.RootIdentity(root)
	userID, userSub := spend.SpendUser(props, e.execCtx.Identity)
	entrypointID, entrypointName := spend.StampedRowOr(props.EntrypointUUID, props.ResolveEntrypointName())
	return &llm.SpendAttribution{
		UserID:         userID,
		UserSub:        userSub,
		WorkflowID:     workflowID,
		WorkflowName:   workflowName,
		WorkflowNodeID: nodeID,
		RootKind:       rootKind,
		RootID:         rootID,
		RootName:       rootName,
		RootNodeID:     rootNodeID,
		EntrypointID:   entrypointID,
		EntrypointName: entrypointName,
		Channel:        props.Channel,
		TriggerKind:    string(triggerKind),
		TriggerID:      triggerID,
		TaskID:         e.execCtx.TaskID,
		ParentTaskID:   origin.ParentTaskID,
		TraceID:        props.TraceID,
		SessionID:      e.execCtx.ContextID,
	}
}
