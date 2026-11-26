// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"errors"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions/sessioncontrol"
	"github.com/SolaceProducts/solace-agent-mesh/internal/awe/bindings"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

const (
	metaKeyWorkflowNodeID = "node_id"
	metaKeyParentTaskID   = "parentTaskId"
)

type SpendOrigin struct {
	ScheduledTaskID string `json:"scheduled_task_id,omitempty"`
	WorkflowID      string `json:"workflow_id,omitempty"`
	WorkflowName    string `json:"workflow_name,omitempty"`
	WorkflowNodeID  string `json:"workflow_node_id,omitempty"`
	ParentTaskID    string `json:"parent_task_id,omitempty"`

	Unrecorded bool `json:"-"`
}

func PersistedSpendOrigin(origin SpendOrigin) *SpendOrigin {
	if origin.Unrecorded {
		return nil
	}
	return &origin
}

func RestoredSpendOrigin(origin *SpendOrigin) SpendOrigin {
	if origin == nil {
		return SpendOrigin{Unrecorded: true}
	}
	return *origin
}

func SpendOriginOf(msg a2a.Message, userProps a2a.UserProperties) SpendOrigin {
	var origin SpendOrigin
	origin.ParentTaskID, _ = msg.Metadata[metaKeyParentTaskID].(string)
	if userProps.Trigger == a2a.TriggerScheduled {
		origin.ScheduledTaskID, _ = msg.Metadata[a2a.MetadataScheduledTaskID].(string)
	}
	workflowName, _ := msg.Metadata[metaKeyWorkflowName].(string)
	if workflowName != "" && workflowName == userProps.DelegatingAgentName {
		origin.WorkflowID, origin.WorkflowName = ComponentIdentity(workflowName)
		origin.WorkflowNodeID, _ = msg.Metadata[metaKeyWorkflowNodeID].(string)
	}
	return origin
}

func (a *Agent) withRoot(userProps a2a.UserProperties) a2a.UserProperties {
	return WithRoot(userProps, a.cfg.Name)
}

func WithRoot(userProps a2a.UserProperties, agentName string) a2a.UserProperties {
	if !namesWholeRoot(userProps) {
		userProps.RootKind = a2a.RootKindAgent
		userProps.RootID = a2a.CanonicalComponentID(agentName)
		userProps.RootNodeID = ""
	}
	return userProps
}

func namesWholeRoot(userProps a2a.UserProperties) bool {
	return userProps.RootKind != "" && userProps.RootID != ""
}

func SpendTrigger(userProps a2a.UserProperties, origin SpendOrigin) (spend.TriggerKind, string) {
	switch {
	case origin.Unrecorded && userProps.DelegatingAgentName != "":
		return "", ""
	case origin.WorkflowID != "":
		return spend.TriggerWorkflow, origin.WorkflowID
	case origin.WorkflowName != "":
		return spend.TriggerWorkflow, origin.WorkflowName
	case userProps.DelegatingAgentName != "":
		return spend.TriggerAgent, a2a.CanonicalComponentID(userProps.DelegatingAgentName)
	}
	switch userProps.Trigger {
	case a2a.TriggerScheduled:
		return spend.TriggerSchedule, origin.ScheduledTaskID
	case a2a.TriggerEval:
		return spend.TriggerEval, ""
	case a2a.TriggerEvent:
		return spend.TriggerEvent, ""
	case a2a.TriggerInteractive:
		return spend.TriggerChat, ""
	default:
		return "", ""
	}
}

func (a *Agent) taskModelRef(ctx context.Context, state *TaskExecutionState) (key, configID string) {
	if a.overrideInForce(state) {
		return state.ModelOverrideAlias, state.ModelOverrideConfigID
	}
	return a.agentModelRef(ctx)
}

func (a *Agent) agentModelRef(ctx context.Context) (key, configID string) {
	return a.cfg.ModelAlias, a.modelConfigID(ctx, a.cfg.ModelAlias)
}

func (a *Agent) overrideInForce(state *TaskExecutionState) bool {
	return state.ModelOverride != nil && state.ModelOverrideAlias != ""
}

func (a *Agent) modelConfigID(ctx context.Context, key string) string {
	if key == "" {
		return ""
	}
	reg := a.svc.Bindings()
	if reg == nil {
		return ""
	}
	raw, err := reg.ResolveRawOrFetch(ctx, key)
	if err != nil {
		a.warnModelConfigIDUnavailable(ctx, key, err)
		return ""
	}
	return samapp.ModelConfigID(raw)
}

func (a *Agent) warnModelConfigIDUnavailable(ctx context.Context, key string, err error) {
	if errors.Is(err, bindings.ErrAliasUnbound) || ctx.Err() != nil {
		return
	}
	a.logger.Warn("model configuration id unavailable; spend is filed without it", "alias", key, "error", err)
}

func (a *Agent) taskSpendAttribution(ctx context.Context, state *TaskExecutionState, surface spend.Surface) *llm.SpendAttribution {
	props := state.UserProps
	triggerKind, triggerID := SpendTrigger(props, state.SpendOrigin)
	agentID, agentName := ComponentIdentity(a.cfg.Name)
	rootID, rootName := RootIdentity(props.RootID)
	userID, userSub := spend.SpendUser(props, state.Identity)
	entrypointID, entrypointName := spend.StampedRowOr(props.EntrypointUUID, props.ResolveEntrypointName())
	modelKey, modelConfigID := a.taskModelRef(ctx, state)
	return &llm.SpendAttribution{
		UserID:         userID,
		UserSub:        userSub,
		AgentID:        agentID,
		AgentName:      agentName,
		WorkflowID:     state.SpendOrigin.WorkflowID,
		WorkflowName:   state.SpendOrigin.WorkflowName,
		WorkflowNodeID: state.SpendOrigin.WorkflowNodeID,
		RootKind:       props.RootKind,
		RootID:         rootID,
		RootName:       rootName,
		RootNodeID:     props.RootNodeID,
		EntrypointID:   entrypointID,
		EntrypointName: entrypointName,
		Channel:        props.Channel,
		TriggerKind:    string(triggerKind),
		TriggerID:      triggerID,
		Surface:        string(surface),
		ModelKey:       modelKey,
		ModelConfigID:  modelConfigID,
		TaskID:         state.TaskID,
		ParentTaskID:   state.SpendOrigin.ParentTaskID,
		TraceID:        props.TraceID,
		SessionID:      state.OriginalSessionID,
	}
}

func RemoteToolSpend(a *llm.SpendAttribution) *remotetool.SpendAttribution {
	return &remotetool.SpendAttribution{
		UserID:         a.UserID,
		UserSub:        a.UserSub,
		AgentID:        a.AgentID,
		AgentName:      a.AgentName,
		WorkflowID:     a.WorkflowID,
		WorkflowName:   a.WorkflowName,
		WorkflowNodeID: a.WorkflowNodeID,
		RootKind:       a.RootKind,
		RootID:         a.RootID,
		RootName:       a.RootName,
		RootNodeID:     a.RootNodeID,
		EntrypointID:   a.EntrypointID,
		EntrypointName: a.EntrypointName,
		Channel:        a.Channel,
		TriggerKind:    a.TriggerKind,
		TriggerID:      a.TriggerID,
		TaskID:         a.TaskID,
		ParentTaskID:   a.ParentTaskID,
		TraceID:        a.TraceID,
		SessionID:      a.SessionID,
	}
}

func (a *Agent) onAgentDefaultModel(ctx context.Context, attribution *llm.SpendAttribution) *llm.SpendAttribution {
	attribution.ModelKey, attribution.ModelConfigID = a.agentModelRef(ctx)
	return attribution
}

func (a *Agent) manualCompactionSpend(ctx context.Context, userID string, info extensions.RequestInfo, params sessioncontrol.CompactParams) *llm.SpendAttribution {
	if params.TaskID == "" || params.TraceID == "" {
		return nil
	}
	agentID, agentName := ComponentIdentity(a.cfg.Name)
	userRowID, userSub := spend.StampedRowOr(params.UserUUID, userID)
	entrypointID, entrypointName := spend.StampedRowOr(params.EntrypointUUID, info.ClientID)
	modelKey, modelConfigID := a.agentModelRef(ctx)
	return &llm.SpendAttribution{
		UserID:         userRowID,
		UserSub:        userSub,
		AgentID:        agentID,
		AgentName:      agentName,
		RootKind:       a2a.RootKindAgent,
		RootID:         agentID,
		RootName:       agentName,
		EntrypointID:   entrypointID,
		EntrypointName: entrypointName,
		Channel:        params.Channel,
		TriggerKind:    string(spend.TriggerChat),
		Surface:        string(spend.SurfaceCompaction),
		ModelKey:       modelKey,
		ModelConfigID:  modelConfigID,
		TaskID:         params.TaskID,
		TraceID:        params.TraceID,
		SessionID:      params.SessionID,
	}
}

func ComponentIdentity(instanceName string) (id, name string) {
	if id, _, ok := a2a.IDFromInstanceName(instanceName); ok && spend.IsCanonicalID(id) {
		return id, ""
	}
	return "", instanceName
}

func RootIdentity(rootID string) (id, name string) {
	if spend.IsCanonicalID(rootID) {
		return rootID, ""
	}
	return "", rootID
}
