// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import "github.com/SolaceProducts/solace-agent-mesh/internal/a2a"

const (
	ScopeRBACRead = "rbac:_:read"

	ScopeEvaluationRead   = "evaluation:_:read"
	ScopeEvaluationCreate = "evaluation:_:create"
	ScopeEvaluationUpdate = "evaluation:_:update"
	ScopeEvaluationDelete = "evaluation:_:delete"
	ScopeEvaluationInvoke = "evaluation:_:invoke"

	ScopeToolRead = "tool:_:read"

	ScopeDeploymentRead = "deployment:_:read"

	ScopeAnalyticsRead        = "analytics:_:read"
	ScopeAnalyticsReadContent = "analytics:_:read_content"

	ScopeEvidenceRead   = "evidence:*:read"
	ScopeEvidenceUpdate = "evidence:*:update"

	ScopeEvidenceProposalUpdate = "evidence_proposal:*:update"

	ScopeEventCatalogRead   = "event_catalog:*:read"
	ScopeEventCatalogCreate = "event_catalog:*:create"
	ScopeEventCatalogUpdate = "event_catalog:*:update"
	ScopeEventCatalogDelete = "event_catalog:*:delete"

	ScopeActivityRead = "activity:*:read"

	ScopeProfileProviderRead   = "profile_provider:*:read"
	ScopeProfileProviderUpdate = "profile_provider:_:update"

	ScopeWebuiSettingsRead   = "webui_settings:*:read"
	ScopeWebuiSettingsUpdate = "webui_settings:*:update"

	ScopeBudgetCreate = "budget:*:create"
	ScopeBudgetRead   = "budget:*:read"
	ScopeBudgetUpdate = "budget:*:update"
	ScopeBudgetDelete = "budget:*:delete"
	ScopeBudgetAssign = "budget:*:assign"

	ScopeReadUsage = "*:*:read_usage"

	ScopeBuilderUse = "builder:_:use"

	ScopeNotifySend       = "notify:_:send"
	ScopeNotifySendOthers = "notify:_:send_others"
	ScopeNotifyReadOthers = "notify:_:read_others"

	ScopePromptShare  = "prompt:_:share"
	ScopeProjectShare = "project:_:share"

	ScopeAgentBuilderCreate = "agent_builder:_:create"
	ScopeConnectorCreate    = "connector:_:create"
	ScopeEntrypointCreate   = "entrypoint:_:create"
	ScopeSkillCreate        = "skill:_:create"
	ScopeToolsetCreate      = "toolset:_:create"
	ScopeModelConfigCreate  = "model_config:_:create"

	ScopeAgentBuilderRead   = "agent_builder:*:read"
	ScopeAgentBuilderUpdate = "agent_builder:*:update"
	ScopeAgentBuilderDelete = "agent_builder:*:delete"
	ScopeAgentBuilderDeploy = "agent_builder:*:deploy"
	ScopeAgentBuilderTest   = "agent_builder:*:test"

	ScopeConnectorRead   = "connector:*:read"
	ScopeConnectorUpdate = "connector:*:update"
	ScopeConnectorDelete = "connector:*:delete"

	ScopeEntrypointRead   = "entrypoint:*:read"
	ScopeEntrypointUpdate = "entrypoint:*:update"
	ScopeEntrypointDelete = "entrypoint:*:delete"
	ScopeEntrypointDeploy = "entrypoint:*:deploy"

	ScopeSkillRead   = "skill:*:read"
	ScopeSkillUpdate = "skill:*:update"
	ScopeSkillDelete = "skill:*:delete"

	ScopeToolsetRead   = "toolset:*:read"
	ScopeToolsetUpdate = "toolset:*:update"
	ScopeToolsetDelete = "toolset:*:delete"

	ScopeModelConfigRead   = "model_config:*:read"
	ScopeModelConfigUpdate = "model_config:*:update"
	ScopeModelConfigDelete = "model_config:*:delete"

	ScopeWorkflowBuilderCreate = "workflow_builder:_:create"
	ScopeWorkflowBuilderRead   = "workflow_builder:*:read"
	ScopeWorkflowBuilderUpdate = "workflow_builder:*:update"
	ScopeWorkflowBuilderDelete = "workflow_builder:*:delete"
	ScopeWorkflowBuilderDeploy = "workflow_builder:*:deploy"

	ScopeToolEmailSend = "tool:email:send"

	ScopeToolDatadogLogs = "tool:datadog_logs:invoke"

	ScopeAgentInvokeAny = "agent:*:invoke"

	ScopeWorkflowInvokeAny = "workflow:*:invoke"

	ScopeAgentReadFeedbackAny    = "agent:*:read_feedback"
	ScopeWorkflowReadFeedbackAny = "workflow:*:read_feedback"
	ScopeAgentReadTraceAny       = "agent:*:read_trace"

	ScopeToolAny = "tool:*:*"
)

func BudgetAdminScopes() []string {
	return []string{ScopeBudgetUpdate, ScopeBudgetAssign}
}

var PlatformIdentityScopes = []string{
	"tool:*__enrich_authenticated_user:invoke",
	"tool:*__search_users:invoke",
	"tool:*__get_user_profile:invoke",
	"tool:*__get_time_off:invoke",
	"tool:*__get_user_profile_picture:invoke",
}

const invokeSuffix = ":invoke"

func AgentInvokeScope(agentName string) string {
	return "agent:" + agentName + invokeSuffix
}

func WorkflowInvokeScope(name string) string {
	return "workflow:" + name + invokeSuffix
}

func InvokeScopeForInstance(cardName string) string {
	if id, kind, ok := a2a.IDFromInstanceName(cardName); ok {
		if kind == a2a.KindWorkflow {
			return WorkflowInvokeScope(id)
		}
		return AgentInvokeScope(id)
	}
	return AgentInvokeScope(cardName)
}

func WorkflowInvokeScopeForInstance(cardName string) string {
	if id, kind, ok := a2a.IDFromInstanceName(cardName); ok && kind == a2a.KindWorkflow {
		return WorkflowInvokeScope(id)
	}
	return WorkflowInvokeScope(cardName)
}

func UserAgentInvokeScope(userID, agentName string) string {
	return "agent:" + userID + "__" + agentName + invokeSuffix
}

func UserWorkflowInvokeScope(userID, workflowName string) string {
	return "workflow:" + userID + "__" + workflowName + invokeSuffix
}
