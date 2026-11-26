// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { AgentCardInfo, AgentExtension } from "@/types";

const EXTENSION_URI_WORKFLOW_VISUALIZATION = "https://solace.com/a2a/extensions/sam/workflow-visualization";
const EXTENSION_URI_AGENT_TYPE = "https://solace.com/a2a/extensions/agent-type";
const EXTENSION_URI_SCHEMAS = "https://solace.com/a2a/extensions/sam/schemas";

export interface WorkflowConfig {
    description?: string;
    version?: string;
    nodes: WorkflowNodeConfig[];
    input_schema?: Record<string, unknown>;
    output_schema?: Record<string, unknown>;
    output_mapping?: Record<string, unknown>;
}

export interface WorkflowNodeConfig {
    id: string;
    type: "agent" | "switch" | "map" | "loop" | "workflow" | "tool" | "decision";
    depends_on?: string[];
    agent_name?: string;
    workflow_name?: string;
    tool_name?: string;
    connector?: string;
    tool?: string;
    input?: Record<string, unknown>;
    instruction?: string;
    input_schema_override?: Record<string, unknown>;
    output_schema_override?: Record<string, unknown>;
    input_schema?: Record<string, unknown>;
    output_schema?: Record<string, unknown>;
    cases?: { condition: string; node: string }[];
    default?: string;
    node?: string;
    items?: string;
    condition?: string;
    max_iterations?: number;
    delay?: string;
    questions?: Record<string, DecisionQuestionConfig> | DecisionQuestionConfig[];
    route_on?: string;
    routes?: Record<string, string>;
    min_confidence?: number;
    escalate?: string;
    escalation_mode?: string | null;
    escalationMode?: string | null;
    on_provider_error?: string;
}

export interface DecisionQuestionConfig {
    key?: string;
    type: "boolean" | "choice" | "score";
    instructions?: string;
    criteria?: Record<string, string | null> | Array<string | Record<string, string | null>>;
}

function nestedWorkflowBody(appConfig: Record<string, unknown> | undefined | null): Record<string, unknown> | null {
    const nested = appConfig?.workflow;
    return nested && typeof nested === "object" && !Array.isArray(nested) ? (nested as Record<string, unknown>) : null;
}

export function workflowBody(appConfig: Record<string, unknown> | undefined | null): Record<string, unknown> {
    return nestedWorkflowBody(appConfig) ?? appConfig ?? {};
}

export function withWorkflowBodyFields(appConfig: Record<string, unknown> | undefined | null, fields: Record<string, unknown>): Record<string, unknown> {
    const nested = nestedWorkflowBody(appConfig);
    if (nested) {
        return { ...appConfig, workflow: { ...nested, ...fields } };
    }
    return { ...appConfig, ...fields };
}

export function getAgentType(agent: AgentCardInfo): "workflow" | "agent" | null {
    const extensions = agent.capabilities?.extensions;
    if (!extensions || extensions.length === 0) return null;

    const agentTypeExtension = extensions.find((ext: AgentExtension) => ext.uri === EXTENSION_URI_AGENT_TYPE);
    if (!agentTypeExtension?.params) return null;

    const type = agentTypeExtension.params.type;
    if (type === "workflow") return "workflow";

    return "agent";
}

export function isWorkflowAgent(agent: AgentCardInfo): boolean {
    return getAgentType(agent) === "workflow";
}

export function getWorkflowConfig(agent: AgentCardInfo): WorkflowConfig | null {
    const extensions = agent.capabilities?.extensions;
    if (!extensions || extensions.length === 0) return null;

    const vizExtension = extensions.find((ext: AgentExtension) => ext.uri === EXTENSION_URI_WORKFLOW_VISUALIZATION);
    if (!vizExtension?.params) return null;

    const workflowConfig = vizExtension.params.workflow_config;
    if (!workflowConfig || typeof workflowConfig !== "object") return null;

    return workflowConfig as WorkflowConfig;
}

export function getWorkflowNodeCount(agent: AgentCardInfo): number {
    const config = getWorkflowConfig(agent);
    if (!config || !config.nodes) return 0;
    return config.nodes.length;
}

export interface AgentSchemas {
    inputSchema?: Record<string, unknown>;
    outputSchema?: Record<string, unknown>;
}

export function getAgentSchemas(agent: AgentCardInfo): AgentSchemas {
    const extensions = agent.capabilities?.extensions;
    if (!extensions || extensions.length === 0) return {};

    const schemasExtension = extensions.find((ext: AgentExtension) => ext.uri === EXTENSION_URI_SCHEMAS);
    if (!schemasExtension?.params) return {};

    return {
        inputSchema: schemasExtension.params.input_schema as Record<string, unknown> | undefined,
        outputSchema: schemasExtension.params.output_schema as Record<string, unknown> | undefined,
    };
}

export const EPHEMERAL_TEST_AGENT_NAME_PREFIX = "__test__";

export function isEphemeralTestAgentName(agentId: string | undefined | null): boolean {
    return !!agentId && agentId.startsWith(EPHEMERAL_TEST_AGENT_NAME_PREFIX);
}

export function isEphemeralTestSession(session: { agentId?: string | null; isEphemeralTest?: boolean } | null | undefined): boolean {
    if (!session) return false;
    if (session.isEphemeralTest === true) return true;
    return isEphemeralTestAgentName(session.agentId);
}

export function resolveAgentSessionRoute(sessionOrAgentId: { agentId?: string | null; isEphemeralTest?: boolean } | string | undefined | null, agentSessionRoutes: Record<string, string> | undefined): string | undefined {
    const normalized = typeof sessionOrAgentId === "string" ? { agentId: sessionOrAgentId, isEphemeralTest: isEphemeralTestAgentName(sessionOrAgentId) } : sessionOrAgentId;
    if (!normalized) return undefined;
    const agentId = normalized.agentId;
    if (!agentId) return undefined;
    const direct = agentSessionRoutes?.[agentId];
    if (direct) return direct;
    if (isEphemeralTestSession(normalized)) {
        return agentSessionRoutes?.["Builder"];
    }
    return undefined;
}

export function pickDefaultChatAgent(agents: AgentCardInfo[]): AgentCardInfo | undefined {
    const sorted = agents
        .filter(agent => !agent.isInternal && !agent.isWorkflow)
        .toSorted((a, b) => {
            const pa = a.priority ?? 0;
            const pb = b.priority ?? 0;
            if (pa !== pb) return pb - pa;
            return (a.displayName ?? a.name ?? "").localeCompare(b.displayName ?? b.name ?? "");
        });
    return sorted[0] ?? agents[0];
}
