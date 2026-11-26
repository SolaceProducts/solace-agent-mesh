// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package rbac

import (
	"slices"
	"strings"
)

type ScopeInfo struct {
	Scope       string
	Name        string
	Description string
}

type scopeMetaEntry struct {
	name        string
	description string
}

var unassignableScopes = map[string]bool{
	ScopeBudgetCreate: true,
	ScopeBudgetUpdate: true,
	ScopeBudgetDelete: true,
	ScopeBudgetAssign: true,
}

var toolScopes = []string{
	ScopeToolRead,
	ScopeToolEmailSend,
	ScopeToolDatadogLogs,
	ScopeToolAny,
}

func IsUnassignableScope(scope string) bool {
	return unassignableScopes[scope]
}

var scopeMeta = map[string]scopeMetaEntry{
	ScopeRBACRead: {"View roles & permissions", "View RBAC roles, assignments, and claim mappings."},

	ScopeEvaluationRead:   {"View evaluations", "View evaluation datasets, experiments, and runs. With the evidence evaluation sets feature on, these include the sets the evidence store drafts: a model writes their problem statements, requests and expected responses from agents' traces, and may paraphrase what the traces say."},
	ScopeEvaluationCreate: {"Create evaluations", "Create evaluation datasets and experiments. Also lets the holder run an experiment as any Agent Mesh service account, so its runs authorize — and reach connected tool credentials — as that principal. Grant only to operators trusted with access beyond their own scopes."},
	ScopeEvaluationUpdate: {"Update evaluations", "Update evaluation datasets and experiments, including the service account an experiment's runs execute as. Same trust bar as Create evaluations."},
	ScopeEvaluationDelete: {"Delete evaluations", "Delete evaluation datasets and experiments."},
	ScopeEvaluationInvoke: {"Run evaluations", "Trigger evaluation runs, and connect or disconnect the tool credentials their service account runs with."},

	ScopeDeploymentRead: {"View deployments", "View the deployment audit log."},

	ScopeAnalyticsRead:          {"View analytics", "View the admin observability analytics dashboard."},
	ScopeAnalyticsReadContent:   {"View analytics content", "View per-user prompt and feedback text in the analytics dashboard."},
	ScopeEvidenceRead:           {"View the evidence store", "View evidence population health, the ingestion quarantine, the content-free trace evidence and the findings grouped from it, the configuration drift findings computed from the deployment (skill ids and tool names, no trace content), the configuration change proposals drafted from it, and each evidence pass's daily limit and use; a proposal's summary and values also need the read scope of the agent or skill it changes. A finding's plain-language explanation and its evaluation set, which a model writes from its traces, also need agent:*:read_trace. It shows every agent's and workflow's name, usage and cost, and who decided each proposal, whichever agents the caller can otherwise read."},
	ScopeEvidenceUpdate:         {"Operate the evidence store", "Request quarantine retries, derivation rebuilds and bounded rewinds of evidence population, and raise an evidence pass's daily limit for the rest of the UTC day, by at most its configured limit and, for evaluation sets, no further than their daily maximum."},
	ScopeEvidenceProposalUpdate: {"Review configuration proposals", "Reject or dismiss the configuration change proposals drafted from evidence findings. A rejection holds its target off for the cooldown; a dismissal frees the target at once and holds only the proposal's finding off for a day. Either also takes evidence:*:read and the read scope of the agent or skill the proposal changes."},

	ScopeEventCatalogRead:   {"View the event catalog", "View the enabled events of the event catalog by name, description and payload schema, to choose what wakes an always-on agent. Topics, entrypoints and disabled events also need one of the event catalog's create, update or delete scopes. The event catalog is served only with the event_catalog feature on."},
	ScopeEventCatalogCreate: {"Create event catalog entries", "Add named business events to the event catalog, each mapped to a topic on an event mesh entrypoint's broker. Also shows every entry's topic and entrypoint, and disabled entries."},
	ScopeEventCatalogUpdate: {"Update event catalog entries", "Change, enable and disable the events of the event catalog, including their topics. Also shows every entry's topic and entrypoint, and disabled entries."},
	ScopeEventCatalogDelete: {"Delete event catalog entries", "Delete events from the event catalog. Also shows every entry's topic and entrypoint, and disabled entries."},

	ScopeActivityRead: {"View all users' activity", "View task activity across all users through the Activity Monitor — grants tenant-wide reports, cross-user failure lists, and full task traces. Content read is bundled: a holder can read any user's prompts, tool arguments, tool results and system prompt, including the raw task export. It grants reads only — it never permits cancelling or modifying another user's task. Users without this scope see their own task activity and the activity of the agents they hold a trace read on."},

	ScopeProfileProviderRead:   {"View profile provider", "View which toolset is wired as the identity profile provider."},
	ScopeProfileProviderUpdate: {"Manage profile provider", "Configure the identity profile provider."},

	ScopeWebuiSettingsRead:   {"View web UI settings", "View the instance-level web UI settings overrides (branding, assistant defaults, and feedback handling)."},
	ScopeWebuiSettingsUpdate: {"Update web UI settings", "Set or clear instance-level web UI settings overrides, including the system purpose injected into web UI tasks and whether user feedback is published to the event mesh."},

	ScopeBudgetRead: {"View budgets", "View LLM spend budgets, which budget is each tier's default, and which budget and top-up apply to the Agent Mesh and to each user who has signed in and each service account, with their names and emails."},

	ScopeReadUsage: {"View usage", "View every user's LLM spend and token usage on the Cost and Token Usage dashboard: totals, the breakdowns by workflow, agent, user and model, each user's name and email, and the budget that applies to each user."},

	ScopeBuilderUse: {"Use builders", "Access the agent and workflow builder section of the UI."},

	ScopeNotifySend:       {"Send notifications", "Let agents send notifications with the notify_user tool."},
	ScopeNotifySendOthers: {"Send notifications to others", "Send notifications to other users, and change, delete or answer the notifications and notification settings of other users."},
	ScopeNotifyReadOthers: {"Read others' notifications", "Read other users' notifications and notification settings, and mark their notifications read."},

	ScopePromptShare: {"Share prompts", "Share saved prompt templates with other users."},

	ScopeAgentBuilderCreate:    {"Create agents", "Create new agents."},
	ScopeConnectorCreate:       {"Create connectors", "Create new connectors."},
	ScopeEntrypointCreate:      {"Create entrypoints", "Create new entrypoints."},
	ScopeSkillCreate:           {"Create skills", "Create new skills."},
	ScopeToolsetCreate:         {"Create toolsets", "Create new toolsets."},
	ScopeModelConfigCreate:     {"Create model configurations", "Create new model configurations."},
	ScopeProjectShare:          {"Share projects", "Share projects with other users."},
	ScopeWorkflowBuilderCreate: {"Create workflows", "Create new workflows."},

	ScopeAgentBuilderRead:   {"View agents", "View agent configuration."},
	ScopeAgentBuilderUpdate: {"Update agents", "Update agent configuration."},
	ScopeAgentBuilderDelete: {"Delete agents", "Delete agents."},
	ScopeAgentBuilderDeploy: {"Deploy agents", "Deploy agents."},
	ScopeAgentBuilderTest:   {"Test agents", "Run agents in test mode."},

	ScopeConnectorRead:   {"View connectors", "View connector configuration."},
	ScopeConnectorUpdate: {"Update connectors", "Update connector configuration."},
	ScopeConnectorDelete: {"Delete connectors", "Delete connectors."},

	ScopeEntrypointRead:   {"View entrypoints", "View entrypoint configuration."},
	ScopeEntrypointUpdate: {"Update entrypoints", "Update entrypoint configuration."},
	ScopeEntrypointDelete: {"Delete entrypoints", "Delete entrypoints."},
	ScopeEntrypointDeploy: {"Deploy entrypoints", "Deploy entrypoints."},

	ScopeSkillRead:   {"View skills", "View skill configuration."},
	ScopeSkillUpdate: {"Update skills", "Update skill configuration."},
	ScopeSkillDelete: {"Delete skills", "Delete skills."},

	ScopeToolsetRead:   {"View toolsets", "View toolset configuration."},
	ScopeToolsetUpdate: {"Update toolsets", "Update toolset configuration."},
	ScopeToolsetDelete: {"Delete toolsets", "Delete toolsets."},

	ScopeModelConfigRead:   {"View model configurations", "View model configuration."},
	ScopeModelConfigUpdate: {"Update model configurations", "Update model configuration."},
	ScopeModelConfigDelete: {"Delete model configurations", "Delete model configurations."},

	ScopeWorkflowBuilderRead:   {"View workflows", "View workflow configuration."},
	ScopeWorkflowBuilderUpdate: {"Update workflows", "Update workflow configuration."},
	ScopeWorkflowBuilderDelete: {"Delete workflows", "Delete workflows."},
	ScopeWorkflowBuilderDeploy: {"Deploy workflows", "Deploy workflows."},

	ScopeAgentInvokeAny:    {"Invoke agents", "Invoke (chat with or delegate to) agents over A2A."},
	ScopeWorkflowInvokeAny: {"Invoke workflows", "Invoke workflows over A2A."},

	ScopeAgentReadFeedbackAny:    {"View agent feedback", "View thumbs feedback on tasks handled by any agent."},
	ScopeWorkflowReadFeedbackAny: {"View workflow feedback", "View thumbs feedback on tasks handled by any workflow."},
	ScopeAgentReadTraceAny:       {"View agent traces", "Open the full contents of task traces, including prompts, tool inputs and outputs, for every agent. A trace opens only when every agent it ran is covered. Also lists every agent's top-level tasks, whoever ran them, including runs attributed to service accounts, and opens them. With evidence:*:read, the agent:*:read_trace grant also opens the evidence store's plain-language explanations of findings and the evaluation sets drafted for them, which a model writes from traces of any agent."},
}

func UnassignableScopes() []string {
	out := make([]string, 0, len(unassignableScopes))
	for scope := range unassignableScopes {
		out = append(out, scope)
	}
	slices.Sort(out)
	return out
}

func Catalog() []ScopeInfo {
	out := make([]ScopeInfo, 0, len(scopeMeta))
	for scope, meta := range scopeMeta {
		out = append(out, ScopeInfo{Scope: scope, Name: meta.name, Description: meta.description})
	}
	slices.SortFunc(out, func(a, b ScopeInfo) int { return strings.Compare(a.Scope, b.Scope) })
	return out
}

func DeclaredScopes() []string {
	out := make([]string, 0, len(scopeMeta)+len(unassignableScopes)+len(toolScopes))
	for scope := range scopeMeta {
		out = append(out, scope)
	}
	for scope := range unassignableScopes {
		out = append(out, scope)
	}
	out = append(out, toolScopes...)
	slices.Sort(out)
	return out
}
