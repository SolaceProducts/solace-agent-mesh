// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { AgentCard, AgentExtension, AgentSkill } from "@/types/a2a";
import { type AgentWelcomeConfig, type ResolvedAgentSkill, type WelcomeSuggestion } from "@/types/agent";

import type { AgentCardInfo, RemoteAgentProtocolStatus, RuntimeProblem } from "@/types";

const DISPLAY_NAME_EXTENSION_URI = "https://solace.com/a2a/extensions/display-name";
const PEER_AGENT_TOPOLOGY_EXTENSION_URI = "https://solace.com/a2a/extensions/peer-agent-topology";
const TOOL_EXTENSION_URI = "https://solace.com/a2a/extensions/sam/tools";
const SAM_SKILLS_EXTENSION_URI = "https://solace.com/a2a/extensions/sam/skills";
const AGENT_TYPE_EXTENSION_URI = "https://solace.com/a2a/extensions/agent-type";
const SCHEMAS_EXTENSION_URI = "https://solace.com/a2a/extensions/sam/schemas";
const WELCOME_EXTENSION_URI = "https://solace.com/a2a/extensions/sam/welcome";
const INTERNAL_EXTENSION_URI = "https://solace.com/a2a/extensions/sam/internal";
const PRIORITY_EXTENSION_URI = "https://solace.com/a2a/extensions/sam/priority";
const EPHEMERAL_EXTENSION_URI = "https://solace.com/a2a/extensions/sam/ephemeral";
const REMOTE_PROTOCOL_EXTENSION_URI = "https://solace.com/a2a/extensions/sam/remote-protocol";
const HEALTH_EXTENSION_URI = "https://solace.com/a2a/extensions/sam/health";

const EXTENSION_VERSIONS = new Map<unknown, RemoteAgentProtocolStatus["selectedVersion"]>([
    ["1.0", "v1_0"],
    ["0.3", "v0_3"],
]);
const EXTENSION_BINDINGS = new Map<unknown, RemoteAgentProtocolStatus["binding"]>([
    ["JSONRPC", "jsonrpc"],
    ["HTTP+JSON", "http_json"],
]);
const CARD_SIGNATURES = new Map<unknown, RemoteAgentProtocolStatus["cardSignature"]>([
    ["verified", "verified"],
    ["unsigned", "unsigned"],
    ["unchecked", "unchecked"],
    ["refused", "refused"],
]);
const UNREPORTED_CARD_SIGNATURES = new Set<unknown>([undefined, ""]);

function remoteProtocolFromExtension(params: Record<string, unknown> | undefined): RemoteAgentProtocolStatus | undefined {
    const selectedVersion = EXTENSION_VERSIONS.get(params?.protocol_version);
    const binding = EXTENSION_BINDINGS.get(params?.protocol_binding);
    const reported = params?.card_signature;
    const cardSignature = UNREPORTED_CARD_SIGNATURES.has(reported) ? "unchecked" : CARD_SIGNATURES.get(reported);
    if (!selectedVersion || !binding || !cardSignature) return undefined;
    return { selectedVersion, binding, deprecated: params?.deprecated === true, cardSignature };
}

export const transformAgentCard = (card: AgentCard): AgentCardInfo => {
    const extensions = card.capabilities?.extensions ?? [];
    const params = (uri: string) => extensions.find((ext: AgentExtension) => ext.uri === uri)?.params;

    const displayName = (params(DISPLAY_NAME_EXTENSION_URI)?.display_name as string | undefined) ?? card.name;
    const peerAgents = (params(PEER_AGENT_TOPOLOGY_EXTENSION_URI)?.peer_agent_names as string[] | undefined) ?? [];

    let tools = (params(TOOL_EXTENSION_URI)?.tools as AgentSkill[] | undefined) ?? [];
    tools = tools.filter(t => !t.name.startsWith("_"));

    const rawSkills = params(SAM_SKILLS_EXTENSION_URI)?.skills;
    const resolvedSkills = (Array.isArray(rawSkills) ? (rawSkills as ResolvedAgentSkill[]) : []).filter((s): s is ResolvedAgentSkill => typeof s?.name === "string").toSorted((a, b) => a.name.localeCompare(b.name));

    const isWorkflow = params(AGENT_TYPE_EXTENSION_URI)?.type === "workflow";
    const isInternal = params(INTERNAL_EXTENSION_URI)?.internal === true;
    const isEphemeral = extensions.some((ext: AgentExtension) => ext.uri === EPHEMERAL_EXTENSION_URI);
    const remoteProtocol = remoteProtocolFromExtension(params(REMOTE_PROTOCOL_EXTENSION_URI));

    const inputSchemaRaw = params(SCHEMAS_EXTENSION_URI)?.input_schema;
    const inputSchema = inputSchemaRaw !== null && typeof inputSchemaRaw === "object" && !Array.isArray(inputSchemaRaw) ? (inputSchemaRaw as Record<string, unknown>) : undefined;

    const priorityRaw = params(PRIORITY_EXTENSION_URI)?.priority;
    const priority = typeof priorityRaw === "number" ? priorityRaw : undefined;

    const welcomeParams = params(WELCOME_EXTENSION_URI);
    const welcome: AgentWelcomeConfig | undefined = welcomeParams
        ? {
              welcome_message: welcomeParams.welcome_message as string | undefined,
              suggestions: welcomeParams.suggestions as WelcomeSuggestion[] | undefined,
          }
        : undefined;

    const rawProblems = params(HEALTH_EXTENSION_URI)?.problems;
    const runtimeProblems = (Array.isArray(rawProblems) ? (rawProblems as RuntimeProblem[]) : []).filter((p): p is RuntimeProblem => typeof p?.id === "string");

    return {
        ...card,
        displayName: displayName,
        peerAgents,
        tools: tools.toSorted((a, b) => a.name.localeCompare(b.name)),
        resolvedSkills,
        connectors: [],
        connectorObjects: [],
        type: "discovered",
        createdBy: "—",
        isWorkflow,
        isInternal,
        isEphemeral,
        remoteProtocol,
        inputSchema,
        priority,
        welcome,
        runtimeProblems: runtimeProblems.length ? runtimeProblems : undefined,
    };
};
