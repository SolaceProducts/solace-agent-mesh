// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type z from "zod";

import type { AgentCardInfoFE, RemoteAgentProtocolStatus } from "./agent";
import type { OpenAPISpecFile } from "./openapi";
import type { Tool } from "./tools";

export interface ToolsetConfigBinding {
    toolsetName: string;
    configValues: Record<string, Record<string, unknown>>;
    volumeBindings?: Record<string, string>;
    networkBindings?: Record<string, string>;
}

export type ToolsetConfigValues = ToolsetConfigBinding[];

export interface SkillConfigBinding {
    skillId?: string;
    skillName?: string;
    configValues: Record<string, Record<string, unknown>>;
}

export interface Agent {
    id?: string;
    type?: "standard" | "a2a_proxy" | "workflow";
    name: string;
    instanceName?: string;
    description: string;
    systemPrompt?: string;
    version?: string;
    deployed?: Agent;
    deploymentStatus?: AgentStatus;
    syncStatus?: SyncStatus;
    connectors?: string[];
    toolsets?: string[];
    toolsetConfigs?: ToolsetConfigValues;
    toolsetExcludedTools?: Record<string, string[]>;
    modelProvider?: string[];
    skills?: AgentSkillFE[];
    skillIds?: string[];
    skillRefs?: string[];
    skillConfigs?: SkillConfigBinding[];
    inputModes?: string[];
    outputModes?: string[];
    createdBy?: string;
    updatedBy?: string;
    createdAt?: string;
    updatedAt?: string;
    runtimeStatus?: RuntimeStatus;

    connectorSnapshots?: Connector[];
    deployedAt?: string;
    deployedBy?: string;

    toolRegistrations?: ToolRegistration[];

    isEphemeral?: boolean;
    sourceSessionId?: string;
    sourceComponentName?: string;
    sourceComponentKind?: string;
    expiresAt?: string;
    additionalConfigurations?: Record<string, unknown>;
}

export interface ToolRegistration {
    toolType: string;
    connectorId?: string;
    name?: string;
    status: "ready" | "failed";
    error?: string;
    toolCount: number;
    toolNames?: string[];
}

export interface ConfigSchemaField {
    key: string;
    type: "string" | "integer" | "boolean";
    description?: string;
    required?: boolean;
    secret?: boolean;
    default?: string | number | boolean;
    options?: (string | number)[];
}

export interface ToolsetFile {
    id: string;
    filename: string;
    toolName: string;
    toolFunction: string | null;
    toolDescription: string;
    timeoutSeconds: number;
    sandboxProfile: string;
    modulePath: string | null;
    fileSize: number;
    configSchema: ConfigSchemaField[] | null;
    createdAt: string;
}

export interface Toolset {
    id: string;
    name: string;
    description: string;
    type: "builtin" | "custom";
    default?: boolean;
    aliases?: string[];
    runtime?: string;
    discoveryStatus?: "created" | "pending" | "ready" | "failed" | "removed";
    discoveryErrors: string[] | null;
    zipFilename: string | null;
    zipFileSize: number | null;
    contentHash?: string;
    files: ToolsetFile[];
    tools?: ToolsetTool[];
    agents?: Array<{ id: string; name: string; description?: string }>;
    configSummary?: ConfigSummary | null;
    createdBy: string;
    updatedBy: string;
    createdAt: string;
    updatedAt: string;
}

export interface ConfigSummary {
    requiredFields: string[];
    requiredCoveredByDefaults: string[];
    requiresAgentConfig: boolean;
}

export interface ToolsetConfigResponse {
    values: Record<string, Record<string, unknown>>;
    updatedBy: string;
    updatedAt: string;
}

export { REDACTED_SENTINEL } from "@/api/models/types";

export interface AgentSkillFE {
    id?: string;
    name: string;
    description: string;
    tags?: string[];
    examples?: string[];
    inputModes?: string[];
    outputModes?: string[];
}

export interface Skill {
    id: string;
    name: string;
    label?: string;
    description: string;
    tags: string[] | null;
    origin: string;
    discoveryStatus: "created" | "pending" | "ready" | "failed" | "removed";
    hasReferences: boolean;
    hasAssets: boolean;
    hasTools: boolean;
    resourceCount: number;
    referenceCount: number;
    assetCount: number;
    bundledTools?: Array<Record<string, unknown>>;
    contentHash?: string;
    agents?: Array<{ id: string; name: string; description?: string }>;
    createdBy: string;
    updatedBy: string;
    createdAt: string;
    updatedAt: string;
}

export interface SkillFileRef {
    path: string;
    sizeBytes: number;
}

export interface SkillFilesResponse {
    references: SkillFileRef[];
    assets: SkillFileRef[];
}

export interface ToolsetTool {
    toolName: string;
    toolDescription?: string;
    timeoutSeconds?: number;
    sandboxProfile?: string;
    configSchema?: ConfigSchemaField[];
    parameters?: Record<string, any>;
}

export type DeploymentStatus = "not_deployed" | "deployed" | "deploy_failed";

export type RuntimeStatusValue = "running" | "degraded" | "starting" | "disconnected" | "stopped";

export interface RuntimeProblem {
    id: string;
    severity: "degraded" | "fatal";
    detail: string;
    since: string;
}

export type RuntimeStatus = { status: "degraded"; problems: RuntimeProblem[] } | { status: Exclude<RuntimeStatusValue, "degraded">; problems?: never };

export type AgentStatus = DeploymentStatus | "stopped" | "active" | "disconnected" | "unknown" | "degraded";

export type AgentCardInfoType = "discovered" | "user" | "external";

export type SyncStatus = "in_sync" | "out_of_sync";

export interface AgentCardInfo extends AgentCardInfoFE {
    status?: AgentStatus;
    syncStatus?: SyncStatus;
    type?: AgentCardInfoType;
    deployed?: AgentCardInfo;
    connectors?: string[];
    connectorObjects?: Connector[];
    toolRegistrations?: ToolRegistration[];
    runtimeProblems?: RuntimeProblem[];
    skillIds?: string[];
    skillRefs?: string[];
    createdBy?: string;
    updatedBy?: string;
    createdAt?: string;
    updatedAt?: string;
    agentCardLocation?: "well_known" | "custom" | "none";

    isWorkflow?: boolean;
}

export interface Connector {
    id: string;
    name: string;
    description: string;
    type: ConnectorType;
    subtype: ConnectorSubType;
    values: Record<string, string | number | string[] | number[] | Record<string, string> | Array<{ key: string; value: string }> | FileList | null>;
    agentCount?: number;
    createdBy?: string;
    createdAt?: string;
    updatedBy?: string;
    updatedAt?: string;

    warnings?: string[];

    openAPISpec?: OpenAPISpecFile | null;
    resources?: FileList;
    tools?: Tool[];

    mcpTools?: any[];
}

export type ConnectorFieldType = "text" | "number" | "password" | "textarea" | "select" | "string" | "key_value" | "operations" | "hil";

export interface ConnectorFieldSchema {
    label: string;
    type: ConnectorFieldType;
    required?: boolean;
    secret?: boolean;
    hidden?: boolean;
    placeholder?: string;
    hint?: string;
    default?: string | number;
    order?: number;
    maxLength?: number;
    conditionalDisplay?: Record<string, string | string[]>;
    options?: { value: string; label: string; description?: string }[];
    validation?: {
        minLength?: number;
        maxLength?: number;
        pattern?: string;
        min?: number;
        max?: number;
        errorMsg?: string;
    };
    array?: {
        minItems?: number;
        maxItems?: number;
        uniqueItems?: boolean;
    };
}

export const ConnectorType = {
    KnowledgeBase: "knowledge_base",
    Sql: "sql",
    Api: "api",
    Mcp: "mcp",
    DocumentDb: "document_db",
    GraphDb: "graph_db",
    Search: "search",
    WebSearch: "web_search",
    Slack: "slack",
    Email: "email",
    EventMesh: "event_mesh",
} as const;

export type ConnectorType = (typeof ConnectorType)[keyof typeof ConnectorType];

export const CONNECTOR_TYPE_MAP = {
    [ConnectorType.KnowledgeBase]: "knowledgeBase",
    [ConnectorType.Sql]: "sql",
    [ConnectorType.Api]: "api",
    [ConnectorType.Mcp]: "mcp",
    [ConnectorType.DocumentDb]: "documentDb",
    [ConnectorType.GraphDb]: "graphDb",
    [ConnectorType.Search]: "search",
    [ConnectorType.WebSearch]: "webSearch",
    [ConnectorType.Slack]: "slack",
    [ConnectorType.Email]: "email",
    [ConnectorType.EventMesh]: "eventMesh",
} as const;

export const CONNECTOR_SUBTYPE = ["bedrock", "postgres", "mysql", "mariadb", "mssql", "oracle", "openapi", "remote", "mongodb", "dynamodb", "neo4j", "neptune", "elasticsearch", "opensearch", "tavily", "exa", "bot", "smtp", "solace"] as const;

export type ConnectorSubType = (typeof CONNECTOR_SUBTYPE)[number];

export const CONNECTOR_APP_TYPE: readonly ConnectorType[] = [
    ConnectorType.KnowledgeBase,
    ConnectorType.Sql,
    ConnectorType.DocumentDb,
    ConnectorType.GraphDb,
    ConnectorType.Search,
    ConnectorType.WebSearch,
    ConnectorType.Slack,
    ConnectorType.Email,
    ConnectorType.EventMesh,
];

export interface ConnectorSubtypeSchema {
    subtype: ConnectorSubType;
    name: string;
    description: string;
    usageGuidance?: string;
    fieldSchema: Record<string, ConnectorFieldSchema>;
    connectionTemplate?: string;
    warning?: string;
    experimental?: boolean;
}

export interface ConnectorSchema extends ConnectorSubtypeSchema {
    type: ConnectorType;
    schemaValidator: z.ZodObject<any>;
    icon?: string;
}

export interface Gateway {
    id?: string;
    slug?: string;
    name: string;
    description: string;
    type: string;
    namespace?: string;
    values: Record<string, any>;
    deploymentStatus?: DeploymentStatus;
    syncStatus?: SyncStatus;
    runtimeStatus?: RuntimeStatus;
    deployed?: Gateway & {
        deployedAt: string;
        deployedBy: string;
    };
    createdBy?: string;
    updatedBy?: string;
    createdAt?: string;
    updatedAt?: string;
}

export interface CreateGatewayRequest {
    name: string;
    slug?: string;
    description: string;
    type: string;
    publicUrl?: string;
    values: Record<string, unknown>;
    deploy: boolean;
}

export interface GatewayFieldSchema {
    label: string;
    type: "string" | "number" | "password" | "boolean" | "text" | "select" | "textarea" | "array" | "array<object>" | "event_rules" | "agent_select" | "workflow_select" | "system_user_select" | "radio";
    required?: boolean;
    itemKey?: string;
    itemLabel?: string;
    itemValidation?: {
        minLength?: number;
        maxLength?: number;
        pattern?: string;
    };
    secret?: boolean;
    writeOnly?: boolean;
    placeholder?: string;
    hint?: string;
    default?: any;
    order?: number;
    options?: { value: string; label: string }[];
    conditionalDisplay?: Record<string, string | string[]>;
    rowGroup?: string;
    clearWhenHidden?: boolean;
    defaultFromField?: string;
    showTestConnection?: boolean;
    showTemplateLookup?: boolean;
    section?: string;
    validation?: {
        minLength?: number;
        maxLength?: number;
        pattern?: string;
        errorMsg?: string;
        requiredMsg?: string;
        min?: number;
        max?: number;
        minItems?: number;
        maxItems?: number;
        ruleNamePattern?: string;
        ruleNamePatternMessage?: string;
        uniqueNames?: boolean;
        caseInsensitiveNames?: boolean;
    };
}

export interface GatewaySchema {
    type: string;
    name: string;
    description: string;
    warning?: string;
    schema: Record<string, GatewayFieldSchema>;
    yamlTemplatePath?: string;
    requiredEnvVars: string[];
    schemaValidator?: z.ZodObject<any>;
    icon?: string;
}

export interface GatewayDeployment {
    id: string;
    gatewayId: string;
    action: "deploy" | "update" | "undeploy";
    status: "success" | "failed";
    gatewayConfigSnapshot?: string;
    errorMessage?: string;
    createdBy: string;
    updatedBy: string;
    createdAt: string;
    updatedAt: string;
}

export interface GatewayApiPath {
    path: string;
    description: string;
}

export interface GatewayNetworkingDetails {
    dnsName?: string;
    port?: number;
    url: string;
    webhookUrl?: string;
    publicUrl?: string | null;
    apiPaths: GatewayApiPath[];
}

export interface GatewayConnectionTestRequest {
    type?: string;
    values?: Record<string, any>;
    gatewayId?: string;
}

export interface GatewayConnectionTestResponse {
    success: boolean;
    message: string;
}

export interface WhatsAppMessageTemplate {
    name: string;
    language: string;
    status: string;
    body: string;
    usable: boolean;
    blockers: WhatsAppTemplateBlocker[];
}

export type WhatsAppTemplateBlocker = "no_contact_button" | "has_variables" | "not_approved";

export interface WhatsAppMessageTemplatesRequest {
    businessAccountId: string;
    values?: Record<string, unknown>;
    gatewayId?: string;
}

export interface WhatsAppMessageTemplatesResponse {
    data: WhatsAppMessageTemplate[];
    hasMore?: boolean;
}

export const AGENT_STATES = {
    active: "active",
    inactive: "inactive",
} as const;
export type AgentState = (typeof AGENT_STATES)[keyof typeof AGENT_STATES];

export interface EventRuleSubscription {
    topic: string;
}

export interface EventRuleOutput {
    enabled: boolean;
    topic: string;
    topicType?: "static" | "dynamic";
}

export interface StructuredInvocation {
    inputSchema?: Record<string, unknown>;
    outputSchema?: Record<string, unknown>;
}

export interface AcknowledgmentPolicy {
    mode: "on_receive" | "on_completion";
    onFailure?: {
        action: "nack" | "ack";
        nackOutcome?: "rejected" | "failed";
    };
    timeoutSeconds?: number;
}

export interface EventRule {
    name: string;
    subscriptions: EventRuleSubscription[];
    messageFormat: "json" | "text";
    targetAgent: string;
    promptTemplate: string;
    targetWorkflowName?: string;
    structuredInvocation?: StructuredInvocation;
    acknowledgmentPolicy?: AcknowledgmentPolicy;
    successOutput: EventRuleOutput;
    errorOutput: EventRuleOutput;
    defaultUserIdentity?: string;
    userIdentityExpression?: string;
    forwardContext?: Record<string, string>;
    runAs?: string;
    existingQueueName?: string;
    eventFilter?: Record<string, unknown>;
}

export const DEFAULT_EVENT_RULE: EventRule = {
    name: "",
    subscriptions: [{ topic: "" }],
    messageFormat: "json",
    targetAgent: "",
    promptTemplate: "Process this event {payload}",
    successOutput: {
        enabled: true,
        topic: "",
        topicType: "static",
    },
    errorOutput: {
        enabled: true,
        topic: "",
        topicType: "static",
    },
};

export interface ExternalAgentCard {
    name: string;
    description: string;
    skills?: AgentSkillFE[];
    inputModes?: string[];
    outputModes?: string[];
    interfaces?: RemoteAgentInterface[];
    signed?: boolean;
}

export interface RemoteAgentInterface {
    version: "v1_0" | "v0_3";
    binding: "jsonrpc" | "http_json";
}

export interface DeployedRemoteAgentInfo {
    agentUrl: string;
    name: string;
    description?: string;
    agentCardLocation: string;
    agentCardUrl?: string;
    agentCardAuthType?: string;
    agentAuthType: string;
    requestTimeoutSeconds: number;
    skills?: AgentSkillFE[];
    inputModes?: string[];
    outputModes?: string[];
    deployedAt: string;
    deployedBy: string;
}

export interface RemoteAgent {
    id: string;
    agentUrl: string;
    name: string;
    instanceName?: string;

    description?: string;
    agentCardLocation: "well_known" | "custom" | "none";
    agentCardUrl?: string;
    agentCardAuthType?: string;
    agentAuthType: string;
    agentCardHeaders?: Record<string, string>;
    taskHeaders?: Record<string, string>;
    requestTimeoutSeconds: number;
    protocolVersion?: "auto" | "v1_0" | "v0_3";
    trustedCardJwksUrls?: string[];
    requireSignedCard?: boolean;
    remoteProtocol?: RemoteAgentProtocolStatus;
    skills?: AgentSkillFE[];
    inputModes?: string[];
    outputModes?: string[];
    deploymentStatus?: AgentStatus;
    syncStatus?: SyncStatus;
    runtimeStatus?: RuntimeStatus;
    deployed?: DeployedRemoteAgentInfo;
    createdBy: string;
    updatedBy: string;
    createdAt: string;
    updatedAt: string;
}

export function isRemoteAgent(agent: Agent | RemoteAgent): agent is RemoteAgent {
    return "agentUrl" in agent;
}
