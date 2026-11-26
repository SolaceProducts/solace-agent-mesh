// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import yaml from "js-yaml";

import { getAllAgentsV2 } from "@/api/agents/v2";
import { getAllConnectorsV2 } from "@/api/connectors/v2";
import { gatewaysApi } from "@/api/gateways/versioned";
import { type CreateRemoteAgentRequest } from "@/api/remote-agents";
import { getAllRemoteAgentsV2 } from "@/api/remote-agents/v2";
import type { SkillBundleConfig, SkillConfigAsset, SkillConfigReference } from "@/api/skills";
import { skillsApi } from "@/api/skills/versioned";
import { toolsetsApi } from "@/api/toolsets";
import { getAllWorkflowsV2 } from "@/api/workflows/v2";

import { workflowBody, type WorkflowConfig } from "@/utils/agentUtils";
import { agentInstructionLength, DEFAULT_AGENT_INSTRUCTION_MAX } from "@/utils/instructionLimits";

import type { CreateWorkflowRequest } from "@/types/workflow";

import { hasVisibleActivityUpdates } from "@/features/chat/activityIndicator";

import type { Agent, AgentSkillFE, CreateGatewayRequest } from "@/types";
import { ConnectorType } from "@/types";

import { type ArtifactVersions, hasUndeployedEdits, hasUnsavedSaveOnlyEdits } from "./componentDirtyState";
import type { BuildManifest } from "./hooks/useBuilderManifest";

const CONNECTOR_CONFIG_TYPES = new Set<string>(Object.values(ConnectorType));

import { type AgentDiagramConfig } from "./types";

export interface PlatformLookupEntry {
    id: string;
    deploymentStatus?: string;
    name?: string;
    displayName?: string;
    agentName?: string;
}

export interface PlatformDeploymentLookup {
    agentsByName: Map<string, PlatformLookupEntry>;
    workflowsByName: Map<string, PlatformLookupEntry>;
    gatewaysByName: Map<string, PlatformLookupEntry>;
    connectorsByName: Map<string, PlatformLookupEntry>;
    remoteAgentsByName: Map<string, PlatformLookupEntry>;
    fetchedKinds: { agent: boolean; workflow: boolean; gateway: boolean; connector: boolean; a2aAgent: boolean };
}

export function normalizeComponentName(s: string): string {
    return s.toLowerCase().replace(/[^a-z0-9]/g, "");
}

export function resolveByIdentity<T extends { id?: string; name?: string; displayName?: string }>(comp: { id?: string; name: string }, records: readonly T[]): T | undefined {
    const exact = records.find(r => r.name === comp.name);
    if (exact) return exact;
    const normalized = normalizeComponentName(comp.name);
    const sameNormalized = (n?: string) => !!n && normalizeComponentName(n) === normalized;
    const byManifestId = comp.id ? records.find(r => r.id === comp.id) : undefined;
    if (byManifestId && (sameNormalized(byManifestId.name) || sameNormalized(byManifestId.displayName))) return byManifestId;
    return records.find(r => sameNormalized(r.name)) ?? records.find(r => sameNormalized(r.displayName)) ?? byManifestId;
}

export const SECRET_KEY_PATTERNS = ["password", "secret", "token", "api_key", "apikey", "private_key", "access_key", "external_id", "s3_key"];

export function isSecretKey(key: string): boolean {
    const lower = key.toLowerCase();
    return SECRET_KEY_PATTERNS.some(p => lower.includes(p));
}

export function humanizeComponentName(name: string): string {
    const words = name
        .split(/[-_\s]+/)
        .filter(Boolean)
        .map(word => word.charAt(0).toUpperCase() + word.slice(1));
    return words.length > 0 ? words.join(" ") : name;
}

const COMPONENT_TYPE_LABELS: Record<string, string> = {
    gateway: "Entrypoint",
    a2a_agent: "Remote Agent",
};

export function componentTypeLabel(type: string): string {
    return (Object.hasOwn(COMPONENT_TYPE_LABELS, type) ? COMPONENT_TYPE_LABELS[type] : undefined) ?? type.charAt(0).toUpperCase() + type.slice(1);
}

export function operationNoun(type: string | undefined): string {
    return type ? componentTypeLabel(type).toLowerCase() : "component";
}

export function isComponentLiveOnMesh(name: string, cards: ReadonlyArray<{ name?: string; displayName?: string }> | undefined): boolean {
    if (!cards?.length) return false;
    const target = normalizeComponentName(name);
    return cards.some(c => (!!c.name && normalizeComponentName(c.name) === target) || (!!c.displayName && normalizeComponentName(c.displayName) === target));
}

const BROKER_KIND_BY_COMPONENT_TYPE: Readonly<Record<string, string>> = { agent: "agent", a2a_agent: "agent", workflow: "workflow" };

export function brokerInstanceName(type: string | undefined, id: string): string | undefined {
    const kind = type !== undefined && Object.hasOwn(BROKER_KIND_BY_COMPONENT_TYPE, type) ? BROKER_KIND_BY_COMPONENT_TYPE[type] : undefined;
    return kind ? `${kind}_${id.replaceAll("-", "_")}` : undefined;
}

export function validatedButLiveComponents<T extends { id?: string; type?: string; origin?: string; status?: string }>(components: readonly T[], cards: ReadonlyArray<{ name?: string }> | undefined): T[] {
    return components.filter(c => c.origin !== "platform" && c.status === "validated" && isSessionComponentLive(c, cards));
}

export function isSessionComponentLive(comp: { id?: string; type?: string }, cards: ReadonlyArray<{ name?: string }> | undefined): boolean {
    if (!comp.id || !cards?.length) return false;
    const instanceName = brokerInstanceName(comp.type, comp.id);
    return !!instanceName && cards.some(c => c.name === instanceName);
}

export function indexEntries(map: Map<string, PlatformLookupEntry>, rec: { id?: string; name?: string; displayName?: string; deploymentStatus?: string; appConfig?: unknown }) {
    if (!rec?.id) return;
    const entry: PlatformLookupEntry = { id: rec.id, deploymentStatus: rec.deploymentStatus, name: rec.name, displayName: rec.displayName };
    const agentName = storedAgentName(rec.appConfig);
    if (agentName) entry.agentName = agentName;
    if (rec.name) {
        map.set(rec.name, entry);
        setAlias(map, normalizeComponentName(rec.name), entry);
    }
    if (rec.displayName) {
        map.set(rec.displayName, entry);
        setAlias(map, normalizeComponentName(rec.displayName), entry);
    }
}

function setAlias(map: Map<string, PlatformLookupEntry>, key: string, entry: PlatformLookupEntry) {
    const holder = map.get(key);
    if (holder && holder.id !== entry.id && (holder.name === key || holder.displayName === key)) return;
    map.set(key, entry);
}

function storedAgentName(appConfig: unknown): string | undefined {
    if (!appConfig || typeof appConfig !== "object" || Array.isArray(appConfig)) return undefined;
    const config = appConfig as Record<string, unknown>;
    return stringField(config.agent_name) ?? stringField(workflowBody(config).agent_name);
}

export async function fetchPlatformDeploymentLookup(): Promise<PlatformDeploymentLookup> {
    const settle = async (load: () => Promise<unknown[]>): Promise<{ ok: boolean; data: unknown[] }> => {
        try {
            return { ok: true, data: await load() };
        } catch {
            return { ok: false, data: [] };
        }
    };
    const [agentsRes, workflowsRes, gatewaysRes, connectorsRes, remoteAgentsRes] = await Promise.all([settle(getAllAgentsV2), settle(getAllWorkflowsV2), settle(gatewaysApi.getAll), settle(getAllConnectorsV2), settle(getAllRemoteAgentsV2)]);
    const agentsByName = new Map<string, PlatformLookupEntry>();
    const workflowsByName = new Map<string, PlatformLookupEntry>();
    const gatewaysByName = new Map<string, PlatformLookupEntry>();
    const connectorsByName = new Map<string, PlatformLookupEntry>();
    const remoteAgentsByName = new Map<string, PlatformLookupEntry>();
    for (const a of agentsRes.data) indexEntries(agentsByName, a as Parameters<typeof indexEntries>[1]);
    for (const w of workflowsRes.data) indexEntries(workflowsByName, w as Parameters<typeof indexEntries>[1]);
    for (const g of gatewaysRes.data) indexEntries(gatewaysByName, g as Parameters<typeof indexEntries>[1]);
    for (const c of connectorsRes.data) indexEntries(connectorsByName, c as Parameters<typeof indexEntries>[1]);
    for (const a of remoteAgentsRes.data) indexEntries(remoteAgentsByName, a as Parameters<typeof indexEntries>[1]);
    return { agentsByName, workflowsByName, gatewaysByName, connectorsByName, remoteAgentsByName, fetchedKinds: { agent: agentsRes.ok, workflow: workflowsRes.ok, gateway: gatewaysRes.ok, connector: connectorsRes.ok, a2aAgent: remoteAgentsRes.ok } };
}

const UNFILLED_DEPLOY_FIELDS_MESSAGE = "cannot be deployed until all required fields are provided";

export const HTTP_SERVICE_UNAVAILABLE = 503;

export function infrastructureError(message: string, cause?: unknown): Error & { status: number } {
    return Object.assign(new Error(message, { cause }), { status: HTTP_SERVICE_UNAVAILABLE });
}

function isMissingRouteStatus(status: number | undefined): boolean {
    return status === 404 || status === 405;
}

export function isAgentFixableError(err: unknown): boolean {
    if (!err || typeof err !== "object") return false;
    const anyErr = err as { response?: { status?: number }; status?: number; code?: string; message?: string };
    const status = anyErr.response?.status ?? anyErr.status;
    if (isMissingRouteStatus(status)) return false;
    if (anyErr.message?.includes(UNFILLED_DEPLOY_FIELDS_MESSAGE)) return false;
    if (typeof status === "number") return status >= 400 && status < 500 && status !== 401 && status !== 403;
    const code = anyErr.code ?? "";
    if (code === "ERR_NETWORK" || code === "ECONNABORTED" || code === "ETIMEDOUT") return false;
    return true;
}

export function findLastAiMessageId(messages: ReadonlyArray<{ isUser?: boolean; metadata?: { messageId?: string } }>): string | undefined {
    for (let i = messages.length - 1; i >= 0; i--) {
        const m = messages[i];
        if (m.metadata?.messageId && !m.isUser) return m.metadata.messageId;
    }
    return undefined;
}

export function hasLiveBuildActivity(lastMessage: { isUser?: boolean; isComplete?: boolean; progressUpdates?: readonly { type: string }[] } | undefined, componentData: ReadonlyMap<string, { isGenerating: boolean }>, showThinking: boolean): boolean {
    for (const d of componentData.values()) {
        if (d.isGenerating) return true;
    }
    return hasVisibleActivityUpdates(lastMessage, showThinking);
}

export function shouldShowCreationProgress(planApproved: boolean, manifest: BuildManifest | null): boolean {
    if (!planApproved || !manifest) return false;
    return !!manifest.components?.some(c => c.origin !== "platform");
}

export function resolveEffectiveManifest(manifest: BuildManifest | null, planApproved: boolean, approvedSnapshot: BuildManifest | null): BuildManifest | null {
    if (manifest) return manifest;
    return planApproved ? approvedSnapshot : null;
}

export function isInlineProgressActive(args: { isResponding: boolean; showCreationProgress: boolean; isApplyingChanges: boolean; currentTurnHasActivity: boolean; inBuildTab: boolean }): boolean {
    if (!args.isResponding) return false;
    if (args.isApplyingChanges) return args.inBuildTab;
    return args.showCreationProgress && args.currentTurnHasActivity;
}

export function computeSuppressedActivityMessageId(args: { showCreationProgress: boolean; isApplyingChanges: boolean; isProgressCollapsed: boolean; progressAnchorMessageId: string | null; lastAiMessageId: string | undefined }): string | undefined {
    if (!args.showCreationProgress || args.isApplyingChanges) return undefined;
    return args.isProgressCollapsed && args.progressAnchorMessageId ? args.progressAnchorMessageId : args.lastAiMessageId;
}

export function shouldShowPlanCard(args: { loadedForEdit: boolean; hasManifest: boolean; planApproved: boolean; hasPlanAnchor: boolean; isAnchored: () => boolean; isLastMessage: boolean }): boolean {
    if (args.loadedForEdit || !args.hasManifest) return false;
    return args.planApproved && args.hasPlanAnchor ? args.isAnchored() : args.isLastMessage;
}

export function shouldShowCreationProgressCard(args: {
    showCreationProgress: boolean;
    hasManifest: boolean;
    isApplyingChanges: boolean;
    hasProgressAnchor: boolean;
    isProgressCollapsed: boolean;
    isAnchored: () => boolean;
    isLastMessage: boolean;
}): boolean {
    if (!args.showCreationProgress || !args.hasManifest || args.isApplyingChanges) return false;
    return args.isProgressCollapsed && args.hasProgressAnchor ? args.isAnchored() : args.isLastMessage;
}

export function shouldShowEditingProgressCard(args: { isLastMessage: boolean; isApplyingChanges: boolean; hasManifest: boolean; inBuildTab: boolean }): boolean {
    return args.isLastMessage && args.isApplyingChanges && args.hasManifest && args.inBuildTab;
}

export function shouldShowBuildCompleteCTA(args: { hasApplyComplete: boolean; anchored: boolean; inBuildTab: boolean; isApplyingChanges: boolean; isResponding: boolean }): boolean {
    if (args.hasApplyComplete || args.isApplyingChanges || args.isResponding) return false;
    return args.anchored && args.inBuildTab;
}

export function shouldShowBuildCompleteCTAAddon(args: { hasManifest: boolean; hasDeployFailure: boolean; loadedForEdit: boolean } & Parameters<typeof shouldShowBuildCompleteCTA>[0]): boolean {
    if (!args.hasManifest || args.hasDeployFailure || args.loadedForEdit) return false;
    return shouldShowBuildCompleteCTA(args);
}

export function shouldShowEditCompleteCard(args: { hasApplyComplete: boolean; anchored: boolean; inBuildTab: boolean }): boolean {
    return args.hasApplyComplete && args.anchored && args.inBuildTab;
}

export function editCompleteCtaMode(args: { pendingDeploy: boolean }): "deploy" | "test" {
    return args.pendingDeploy ? "deploy" : "test";
}

export function testableSessionComponents<T extends { origin?: string; type?: string }>(components: readonly T[] | undefined): T[] {
    return (components ?? []).filter(c => c.origin !== "platform" && c.type !== "gateway" && c.type !== "skill" && c.type !== "connector");
}

export function deployableSessionComponents<T extends { origin?: string; type?: string }>(components: readonly T[] | undefined): T[] {
    return (components ?? []).filter(c => c.origin !== "platform" && c.type !== "skill" && c.type !== "connector");
}

export function isSaveOnlyBuild<T extends { origin?: string; type?: string }>(components: readonly T[] | undefined): boolean {
    const session = (components ?? []).filter(c => c.origin !== "platform");
    return session.length > 0 && deployableSessionComponents(components).length === 0;
}

export function shouldAutoSaveOnly(s: {
    saveOnlyBuild: boolean;
    readyToSave: boolean;
    agentResponded: boolean;
    allSaved: boolean;
    isSaving: boolean;
    isResponding: boolean;
    hasUnsavedEdits: boolean;
    allMatchLastSave: boolean;
    hasSkill: boolean;
    declined: boolean;
    saveOnlyTriggered: boolean;
    autoSaveTriggered: boolean;
}): boolean {
    if (!s.saveOnlyBuild || !s.readyToSave || !s.agentResponded || s.isSaving || s.saveOnlyTriggered || s.autoSaveTriggered || s.declined) return false;
    if (s.hasUnsavedEdits || s.hasSkill) return !s.isResponding;
    return !s.allSaved && !s.allMatchLastSave;
}

export const BLOCKED_STATUS = "blocked";

export function isBlockedSessionComponent(c: { origin?: string; status?: string }): boolean {
    return c.origin !== "platform" && c.status === BLOCKED_STATUS;
}

export interface BuildCompleteComponent {
    origin?: string;
    type?: string;
    status?: string;
    filename?: string;
    deployed_version?: number;
}

export interface DeployEditsContext {
    versions: ArtifactVersions | undefined;
    serverApplyEnabled: boolean;
}

function hasSaveOnlyAwaitingSave(components: readonly BuildCompleteComponent[] | undefined, edits: DeployEditsContext): boolean {
    return (components ?? []).some(c => hasUnsavedSaveOnlyEdits(c, edits.versions, edits.serverApplyEnabled));
}

export function isBuildReadyToTest(components: readonly BuildCompleteComponent[] | undefined, edits: DeployEditsContext): boolean {
    if ((components ?? []).some(isBlockedSessionComponent)) return false;
    if (testableSessionComponents(components).length === 0) return false;
    if (hasSaveOnlyAwaitingSave(components, edits)) return false;
    return deployableSessionComponents(components).every(c => c.status === "deployed" && !hasUndeployedEdits(c, edits.versions, edits.serverApplyEnabled));
}

function isComponentBuilt(status?: string): boolean {
    return status === "validated" || status === "deployed";
}

export function isSkillOnlyBuildComplete(components: readonly { origin?: string; type?: string; status?: string }[] | undefined): boolean {
    if (testableSessionComponents(components).length > 0) return false;
    if (!deployableSessionComponents(components).every(c => c.status === "deployed")) return false;
    const skills = (components ?? []).filter(c => c.origin !== "platform" && c.type === "skill");
    return skills.length > 0 && skills.every(c => isComponentBuilt(c.status));
}

export function isConnectorOnlyBuildComplete(components: readonly { origin?: string; type?: string; status?: string }[] | undefined): boolean {
    if (testableSessionComponents(components).length > 0) return false;
    if (!deployableSessionComponents(components).every(c => c.status === "deployed")) return false;
    const session = (components ?? []).filter(c => c.origin !== "platform");
    if (session.some(c => c.type === "skill")) return false;
    const connectors = session.filter(c => c.type === "connector");
    return connectors.length > 0 && connectors.every(c => isComponentBuilt(c.status));
}

export function isEntrypointOnlyBuildComplete(components: readonly BuildCompleteComponent[] | undefined, edits: DeployEditsContext): boolean {
    if (testableSessionComponents(components).length > 0) return false;
    const session = (components ?? []).filter(c => c.origin !== "platform");
    if (session.some(c => c.type === "skill" || c.type === "connector")) return false;
    const entrypoints = session.filter(c => c.type === "gateway");
    return entrypoints.length > 0 && entrypoints.every(c => c.status === "deployed" && !hasUndeployedEdits(c, edits.versions, edits.serverApplyEnabled));
}

export function isBuildCompleteCtaShowing(components: readonly BuildCompleteComponent[] | undefined, edits: DeployEditsContext): boolean {
    if ((components ?? []).some(isBlockedSessionComponent)) return true;
    return isBuildReadyToTest(components, edits) || isSkillOnlyBuildComplete(components) || isConnectorOnlyBuildComplete(components) || isEntrypointOnlyBuildComplete(components, edits);
}

type AddonMessage = { isUser?: boolean; metadata?: { messageId?: string }; userInputRequest?: { responded?: boolean; timedOut?: boolean; cancelled?: boolean } | null };

export function isLastNonUserMessage<T extends AddonMessage>(msgs: ReadonlyArray<T>, message: T): boolean {
    for (let i = msgs.length - 1; i >= 0; i--) {
        const m = msgs[i];
        if (m.metadata?.messageId && !m.isUser) return m === message;
    }
    return false;
}

export function isFirstNonUserMessageWithId<T extends AddonMessage>(msgs: ReadonlyArray<T>, message: T, targetId: string): boolean {
    for (const m of msgs) {
        if (m.metadata?.messageId === targetId && !m.isUser) return m === message;
    }
    return false;
}

export function resolvePlanAnchorId(msgs: ReadonlyArray<AddonMessage>): string | null {
    let anchorId: string | null = null;
    for (const m of msgs) {
        if (m.isUser || !m.metadata?.messageId || m.userInputRequest) continue;
        anchorId = m.metadata.messageId;
    }
    return anchorId;
}

export function collectPendingHilInputMessages<T extends AddonMessage>(msgs: ReadonlyArray<T>): T[] {
    return msgs.filter(m => !!m.userInputRequest && !m.userInputRequest.responded && !m.userInputRequest.timedOut && !m.userInputRequest.cancelled);
}

export const SAM_REQUIRED_PLACEHOLDER = "<<__SAM_REQUIRED__>>";

function countPlaceholderAuthSecrets(auth: unknown): number {
    if (!auth || typeof auth !== "object") return 0;
    return Object.values(auth as Record<string, unknown>).filter(v => v === SAM_REQUIRED_PLACEHOLDER).length;
}

export interface CredentialCandidate {
    connectorMetadata?: { name?: string; type?: string; subtype?: string; values?: Record<string, unknown> | null } | null;
    a2aAgentMetadata?: CreateRemoteAgentRequest | null;
}

export function detectComponentsWithMissingCredentials(
    manifest: { components?: Array<{ name: string; type?: string; origin?: string }> } | null | undefined,
    componentData: ReadonlyMap<string, CredentialCandidate> | null | undefined
): Map<string, number> {
    const missing = new Map<string, number>();
    if (!manifest?.components || !componentData) return missing;
    for (const comp of manifest.components) {
        if (comp.origin === "platform") continue;
        const data = componentData.get(comp.name);
        if (comp.type === "connector") {
            const values = data?.connectorMetadata?.values;
            if (!values) continue;
            const count = Object.values(values).filter(v => v === SAM_REQUIRED_PLACEHOLDER).length;
            if (count > 0) missing.set(comp.name, count);
        } else if (comp.type === "a2a_agent") {
            const meta = data?.a2aAgentMetadata;
            if (!meta) continue;
            const count = countPlaceholderAuthSecrets(meta.agentAuthConfig) + countPlaceholderAuthSecrets(meta.agentCardAuthConfig);
            if (count > 0) missing.set(comp.name, count);
        }
    }
    return missing;
}

export function formatMissingCredentials(missing: ReadonlyMap<string, number>): string {
    return [...missing.entries()].map(([name, n]) => `${name} (${n} credential${n === 1 ? "" : "s"})`).join(", ");
}

export function buildBlockedByCredsMessage(missing: ReadonlyMap<string, number>, action: "building" | "testing" = "building"): string {
    const blocked = action === "building" ? "Building" : "Testing";
    return `Credentials are missing for ${formatMissingCredentials(missing)}. ${blocked} is blocked until they are filled in. Ask the builder for them in the chat. Secret values such as passwords and API keys can also be entered on the Connectors page if the connector is already saved. A component with placeholder credentials won't work.`;
}

export const SAM_CONFIG_MIME_TYPE = "application/vnd.sam-config+yaml";

export const SAM_AGENT_CONFIG_MIME_TYPE = "application/vnd.sam-agent-config+yaml";
export const SAM_WORKFLOW_CONFIG_MIME_TYPE = "application/vnd.sam-workflow-config+yaml";
export const SAM_GATEWAY_CONFIG_MIME_TYPE = "application/vnd.sam-gateway-config+yaml";

export const SAM_CONNECTOR_CONFIG_MIME_TYPE = "application/vnd.sam-connector-config+json";

export const SAM_SKILL_CONFIG_MIME_TYPE = "application/vnd.sam-skill-config+yaml";

export const SAM_A2A_AGENT_CONFIG_MIME_TYPE = "application/vnd.sam-a2a-agent-config+json";

export const SAM_BUILDER_MIME_TYPES = [
    SAM_CONFIG_MIME_TYPE,
    SAM_AGENT_CONFIG_MIME_TYPE,
    SAM_WORKFLOW_CONFIG_MIME_TYPE,
    SAM_GATEWAY_CONFIG_MIME_TYPE,
    SAM_CONNECTOR_CONFIG_MIME_TYPE,
    SAM_SKILL_CONFIG_MIME_TYPE,
    SAM_A2A_AGENT_CONFIG_MIME_TYPE,
] as const;

export function isBuilderConfigMime(mime: string): boolean {
    return (SAM_BUILDER_MIME_TYPES as readonly string[]).includes(mime);
}

export type ConfigType = "agent" | "workflow" | "gateway" | "connector" | "skill" | "a2a_agent" | "unknown";

interface ParsedAppConfig {
    name: string;
    appConfig: Record<string, unknown>;
}

function parseFirstApp(yamlText: string): ParsedAppConfig | null {
    try {
        const doc = yaml.load(yamlText, { json: true }) as Record<string, unknown> | null;
        if (!doc || typeof doc !== "object") return null;

        const apps = doc.apps as Array<Record<string, unknown>> | undefined;
        if (!Array.isArray(apps) || apps.length === 0) return null;

        const firstApp = apps[0];
        const name = stringField(firstApp.name) ?? "";
        const appConfig = (firstApp.app_config as Record<string, unknown>) || {};

        return { name, appConfig };
    } catch {
        return null;
    }
}

export function detectConfigType(text: string): ConfigType {
    if (detectA2AAgentJson(text)) return "a2a_agent";

    if (detectConnectorJson(text)) return "connector";

    if (detectSkillYaml(text)) return "skill";

    const parsed = parseFirstApp(text);
    if (!parsed) return "unknown";

    const { appConfig } = parsed;

    if (appConfig.agent_type === "workflow" || Array.isArray(workflowBody(appConfig).nodes)) {
        return "workflow";
    }

    if (Array.isArray(appConfig.event_handlers)) {
        return "gateway";
    }

    if (appConfig.agent_name || appConfig.instruction) {
        return "agent";
    }

    return "unknown";
}

const EXPECTED_CONFIG_SHAPE: Partial<Record<ConfigType, string>> = {
    agent: "a YAML file whose apps[0].app_config sets agent_name or instruction",
    workflow: "a YAML file whose apps[0].app_config sets agent_type: workflow or a nodes list",
    gateway: "a YAML file whose apps[0].app_config has an event_handlers list (a kind: entrypoint file is for sam config apply, not the Builder)",
    connector: "a JSON object with a supported connector type and a values object",
    a2a_agent: "a JSON object with agentUrl and agentCardLocation",
    skill: "a YAML file with top-level name and instruction_content",
};

export function unrecognizedConfigMessage(name: string, manifestType: string | undefined): string {
    const base = `The config for "${name}" is not a recognized component config, so it could not be saved.`;
    const expected = manifestType && Object.hasOwn(EXPECTED_CONFIG_SHAPE, manifestType) ? EXPECTED_CONFIG_SHAPE[manifestType as ConfigType] : undefined;
    return expected ? `${base} The ${operationNoun(manifestType)} config must be ${expected}.` : base;
}

function detectSkillYaml(text: string): boolean {
    try {
        const doc = yaml.load(text, { json: true }) as Record<string, unknown> | null;
        if (!doc || typeof doc !== "object") return false;
        return typeof doc.name === "string" && typeof doc.instruction_content === "string";
    } catch {
        return false;
    }
}

function detectA2AAgentJson(text: string): boolean {
    try {
        const doc = JSON.parse(text);
        if (!doc || typeof doc !== "object") return false;
        return typeof doc.agentUrl === "string" && typeof doc.agentCardLocation === "string";
    } catch {
        return false;
    }
}

function detectConnectorJson(text: string): boolean {
    try {
        const doc = JSON.parse(text);
        if (!doc || typeof doc !== "object") return false;
        return CONNECTOR_CONFIG_TYPES.has(doc.type) && doc.values && typeof doc.values === "object";
    } catch {
        return false;
    }
}

export function extractWorkflowConfig(yamlText: string): WorkflowConfig | null {
    const parsed = parseFirstApp(yamlText);
    if (!parsed) return null;

    const body = workflowBody(parsed.appConfig);

    if (!Array.isArray(body.nodes) || !body.nodes.every(node => node && typeof node === "object" && !Array.isArray(node))) {
        return null;
    }

    return body as unknown as WorkflowConfig;
}

function stringField(value: unknown): string | undefined {
    return typeof value === "string" && value ? value : undefined;
}

function objectEntries(value: unknown): Array<Record<string, unknown>> | undefined {
    if (!Array.isArray(value)) return undefined;
    return value.filter((entry): entry is Record<string, unknown> => !!entry && typeof entry === "object" && !Array.isArray(entry));
}

function stringEntries(value: unknown): string[] | undefined {
    return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === "string" && entry !== "") : undefined;
}

export function extractWorkflowMetadata(yamlText: string): CreateWorkflowRequest | null {
    const parsed = parseFirstApp(yamlText);
    if (!parsed) return null;

    const { name, appConfig } = parsed;
    const body = workflowBody(appConfig);

    const platformName = stringField(appConfig.name) || stringField(appConfig.agent_name) || name;

    const agentCard = body.agent_card as Record<string, unknown> | undefined;
    const displayName = (agentCard?.display_name as string) || (body.display_name as string) || platformName;
    const description = (agentCard?.description as string) || (body.description as string) || "";

    return {
        name: platformName,
        displayName,
        description,
        appConfig,
    };
}

export function legacyWorkflowName(yamlText: string | null | undefined): string | undefined {
    if (!yamlText) return undefined;
    const parsed = parseFirstApp(yamlText);
    if (!parsed) return undefined;
    const legacyName = stringField(parsed.appConfig.name) || parsed.name;
    return legacyName && legacyName !== extractWorkflowMetadata(yamlText)?.name ? legacyName : undefined;
}

export interface ExtractedAgentMetadata extends Partial<Agent> {
    loadableSkillNames?: string[];
    builtinGroupNames?: string[];
    builtinToolNames?: string[];
}

function nonEmptyString(value: unknown): string | undefined {
    return typeof value === "string" && value !== "" ? value : undefined;
}

function advertisedCardSkill(entry: unknown): AgentSkillFE | undefined {
    if (typeof entry !== "object" || entry === null) return undefined;
    const s = entry as Record<string, unknown>;
    const id = nonEmptyString(s.id);
    const name = nonEmptyString(s.name) ?? id;
    if (!name) return undefined;
    const tags = stringArrayOrUndefined(s.tags);
    const examples = stringArrayOrUndefined(s.examples);
    const inputModes = stringArrayOrUndefined(s.inputModes);
    const outputModes = stringArrayOrUndefined(s.outputModes);
    return {
        ...(id ? { id } : {}),
        name,
        description: typeof s.description === "string" ? s.description : "",
        ...(tags ? { tags } : {}),
        ...(examples ? { examples } : {}),
        ...(inputModes ? { inputModes } : {}),
        ...(outputModes ? { outputModes } : {}),
    };
}

export function extractAgentMetadata(yamlText: string): ExtractedAgentMetadata | null {
    const parsed = parseFirstApp(yamlText);
    if (!parsed) return null;

    const { name, appConfig } = parsed;

    const instruction = (appConfig.instruction as string) || "";
    const agentName = (appConfig.agent_name as string) || name;

    const agentCard = appConfig.agent_card as Record<string, unknown> | undefined;
    const description = (agentCard?.description as string) || (appConfig.description as string) || "";

    const skills = Array.isArray(agentCard?.skills) ? agentCard.skills.map(advertisedCardSkill).filter((s): s is AgentSkillFE => s !== undefined) : undefined;

    const loadableSkillName = (entry: Record<string, unknown> | string): string => {
        if (typeof entry === "string") return entry;
        return typeof entry?.name === "string" ? entry.name : "";
    };
    const loadableSkillsRaw = appConfig.skills as Array<Record<string, unknown> | string> | undefined;
    const loadableSkillNames = Array.isArray(loadableSkillsRaw)
        ? loadableSkillsRaw
              .map(loadableSkillName)
              .map(n => n.trim())
              .filter(Boolean)
        : undefined;

    const handAuthoredToolsets = appConfig.toolsets as string[] | undefined;
    const { builtinGroupNames, builtinToolNames } = extractBuiltinToolRefs(appConfig);

    const inputModes = (agentCard?.defaultInputModes as string[] | undefined) || (appConfig.input_modes as string[] | undefined);
    const outputModes = (agentCard?.defaultOutputModes as string[] | undefined) || (appConfig.output_modes as string[] | undefined);

    return {
        name: agentName,
        description,
        systemPrompt: instruction,
        skills,
        ...(loadableSkillNames?.length ? { loadableSkillNames } : {}),
        toolsets: handAuthoredToolsets,
        ...(builtinGroupNames.length ? { builtinGroupNames } : {}),
        ...(builtinToolNames.length ? { builtinToolNames } : {}),
        inputModes,
        outputModes,
    };
}

function extractBuiltinToolRefs(appConfig: Record<string, unknown>): { builtinGroupNames: string[]; builtinToolNames: string[] } {
    const toolsRaw = appConfig.tools as Array<Record<string, unknown>> | undefined;
    const entries = Array.isArray(toolsRaw) ? toolsRaw.filter(tool => tool && typeof tool === "object") : [];
    const trimmedString = (value: unknown) => (typeof value === "string" ? value.trim() : "");
    const namesOfType = (toolType: string, field: string) => entries.filter(tool => tool.tool_type === toolType).map(tool => trimmedString(tool[field]));

    return {
        builtinGroupNames: [...new Set(namesOfType("builtin-group", "group_name").filter(Boolean))],
        builtinToolNames: [...new Set(namesOfType("builtin", "tool_name").filter(Boolean))],
    };
}

export interface BuiltinToolsetLookup {
    toolsetIdsByToolName: Map<string, string[]>;
    toolNamesByToolsetId: Map<string, string[]>;
    toolsetIdByGroupName: Map<string, string>;
    loadError?: unknown;
}

export async function fetchBuiltinToolsetLookup(): Promise<BuiltinToolsetLookup> {
    const lookup: BuiltinToolsetLookup = { toolsetIdsByToolName: new Map(), toolNamesByToolsetId: new Map(), toolsetIdByGroupName: new Map() };
    try {
        for (const toolset of await toolsetsApi.getBuiltin()) {
            lookup.toolsetIdByGroupName.set(toolset.id, toolset.id);
            for (const alias of toolset.aliases ?? []) lookup.toolsetIdByGroupName.set(alias, toolset.id);
            const toolNames = (toolset.tools ?? []).map(t => t.toolName).filter(Boolean);
            if (!toolNames.length) continue;
            lookup.toolNamesByToolsetId.set(toolset.id, toolNames);
        }
        for (const toolName of new Set([...lookup.toolNamesByToolsetId.values()].flat())) {
            lookup.toolsetIdsByToolName.set(toolName, rankOwningToolsets(toolName, lookup.toolNamesByToolsetId));
        }
    } catch (err) {
        console.error("[builder] built-in toolset catalog fetch failed", err);
        lookup.loadError = err;
    }
    return lookup;
}

const NO_ACCESS_STATUSES: ReadonlySet<number> = new Set([401, 403]);

function catalogUnavailableError(cause: unknown): Error {
    const causeStatus = (cause as { response?: { status?: number }; status?: number } | undefined)?.response?.status ?? (cause as { status?: number } | undefined)?.status;
    const noAccess = typeof causeStatus === "number" && NO_ACCESS_STATUSES.has(causeStatus);
    const reason = cause instanceof Error && cause.message ? ` (${cause.message})` : "";
    const advice = noAccess ? "Check the signed-in account's access to toolsets." : "Try again in a moment.";
    return Object.assign(new Error(`The built-in toolset catalog could not be loaded${reason}, so this agent's tools cannot be bound. ${advice}`, { cause }), { status: noAccess ? causeStatus : 503 });
}

function rankOwningToolsets(toolName: string, toolNamesByToolsetId: Map<string, string[]>): string[] {
    return [...toolNamesByToolsetId.entries()]
        .filter(([, toolNames]) => toolNames.includes(toolName))
        .sort(([idA, toolsA], [idB, toolsB]) => toolsA.length - toolsB.length || idA.localeCompare(idB))
        .map(([id]) => id);
}

export function resolveToolsetBindings(groupNames: string[], toolNames: string[], lookup: BuiltinToolsetLookup): { toolsets: string[]; toolsetExcludedTools?: Record<string, string[]> } {
    const grantedAsGroup = new Set(groupNames.map(name => lookup.toolsetIdByGroupName.get(name) ?? name));
    const toolsets: string[] = [...grantedAsGroup];

    if (toolNames.length > 0 && (lookup.loadError !== undefined || lookup.toolNamesByToolsetId.size === 0)) {
        throw catalogUnavailableError(lookup.loadError);
    }

    const unresolved: string[] = [];
    const stillNeeded = new Set<string>();
    for (const toolName of toolNames) {
        const owners = lookup.toolsetIdsByToolName.get(toolName) ?? [];
        if (!owners.length) {
            unresolved.push(toolName);
            continue;
        }
        if (owners.some(id => grantedAsGroup.has(id))) continue;
        stillNeeded.add(toolName);
    }

    if (unresolved.length > 0) {
        const quotedNames = unresolved.map(n => `"${n}"`).join(", ");
        throw new Error(
            `Cannot bind tool(s) ${quotedNames} to this agent: no built-in toolset available on this deployment provides them. ` +
                `A skill's tool (xlsx_read, docx_create_document) comes with the built-in skill listed under skills:, a connector's tool (execute_sql_query) with a connector linked to the agent, ` +
                `and anything else through the built-in group that owns it (\`tool_type: builtin-group\`). Check the tool name against the groups.`
        );
    }

    const toolsetExcludedTools: Record<string, string[]> = {};
    for (const [toolsetId, requested] of assignToolsToToolsets(stillNeeded, lookup)) {
        toolsets.push(toolsetId);
        const excluded = (lookup.toolNamesByToolsetId.get(toolsetId) ?? []).filter(name => !requested.has(name));
        if (excluded.length) toolsetExcludedTools[toolsetId] = excluded;
    }

    return {
        toolsets,
        ...(Object.keys(toolsetExcludedTools).length ? { toolsetExcludedTools } : {}),
    };
}

function assignToolsToToolsets(needed: Set<string>, lookup: BuiltinToolsetLookup): Array<[string, Set<string>]> {
    const remaining = new Set(needed);
    const assigned = new Map<string, Set<string>>();

    while (remaining.size > 0) {
        const [toolsetId, covered] = bestCoveringToolset(remaining, lookup);
        assigned.set(toolsetId, new Set(covered));
        for (const name of covered) remaining.delete(name);
    }

    return [...assigned.entries()].sort(([idA], [idB]) => idA.localeCompare(idB));
}

function bestCoveringToolset(remaining: Set<string>, lookup: BuiltinToolsetLookup): [string, string[]] {
    const candidates = new Set<string>();
    for (const toolName of remaining) {
        for (const id of lookup.toolsetIdsByToolName.get(toolName) ?? []) candidates.add(id);
    }

    const ranked = [...candidates]
        .map(id => {
            const members = lookup.toolNamesByToolsetId.get(id) ?? [];
            return { id, members: members.length, covered: members.filter(name => remaining.has(name)) };
        })
        .sort((a, b) => b.covered.length - a.covered.length || a.members - b.members || a.id.localeCompare(b.id));

    return [ranked[0].id, ranked[0].covered];
}

export interface PlatformSkillLookup {
    customIdsByName: Map<string, string>;
    builtinNames: Set<string>;
}

export async function fetchPlatformSkillLookup(): Promise<PlatformSkillLookup> {
    const lookup: PlatformSkillLookup = { customIdsByName: new Map(), builtinNames: new Set() };
    try {
        const [custom, builtin] = await Promise.all([skillsApi.listCustom(), skillsApi.listBuiltin()]);
        for (const s of custom) {
            if (s.name && s.id) lookup.customIdsByName.set(s.name, s.id);
        }
        for (const s of builtin) {
            if (s.name) lookup.builtinNames.add(s.name);
        }
    } catch {}
    return lookup;
}

export function addressableComponentName(type: string | undefined, yamlText: string | null | undefined, fallbackName: string): string {
    if (!yamlText) return fallbackName;
    if (type === "workflow") return extractWorkflowMetadata(yamlText)?.name || fallbackName;
    if (type === "agent") return extractAgentMetadata(yamlText)?.name || fallbackName;
    return fallbackName;
}

export function resolveTestTargetName(type: string | undefined, yamlText: string | null | undefined, fallbackName: string, storedWorkflowNames: ReadonlySet<string> | undefined): string {
    const name = addressableComponentName(type, yamlText, fallbackName);
    if (type !== "workflow" || !storedWorkflowNames || storedWorkflowNames.has(name)) return name;
    const legacy = legacyWorkflowName(yamlText);
    return legacy && storedWorkflowNames.has(legacy) ? legacy : name;
}

export function validateAgentMetadata(agent: Partial<Agent>): string[] {
    const errors: string[] = [];

    const name = agent.name || "";
    if (name.length < 3 || name.length > 40) {
        errors.push(`Agent name must be between 3 and 40 characters (got ${name.length}: "${name}").`);
    }

    const description = agent.description || "";
    if (description.length < 10 || description.length > 1000) {
        errors.push(`Agent description must be between 10 and 1000 characters (got ${description.length}).`);
    }

    const systemPrompt = agent.systemPrompt || "";
    const promptLength = agentInstructionLength(systemPrompt);
    if (!systemPrompt) {
        errors.push(`System prompt (app_config.instruction) is required for standard agents.`);
    } else if (promptLength > DEFAULT_AGENT_INSTRUCTION_MAX) {
        errors.push(`System prompt must be ${DEFAULT_AGENT_INSTRUCTION_MAX.toLocaleString()} characters or fewer (got ${promptLength.toLocaleString()}).`);
    }

    if (agent.skills && agent.skills.length > 20) {
        errors.push(`Maximum 20 skills per agent (got ${agent.skills.length}).`);
    }

    return errors;
}

export function extractAgentDiagramConfig(yamlText: string): AgentDiagramConfig | null {
    const parsed = parseFirstApp(yamlText);
    if (!parsed) return null;

    const { name, appConfig } = parsed;

    const agentName = stringField(appConfig.agent_name) ?? name;
    const agentCardDiag = appConfig.agent_card as Record<string, unknown> | undefined;
    const description = stringField(agentCardDiag?.description) ?? stringField(appConfig.description) ?? "";
    const instruction = stringField(appConfig.instruction) ?? "";

    const skills = objectEntries(appConfig.skills)?.map(s => ({
        name: stringField(s.name) ?? "",
        description: stringField(s.description) ?? "",
    }));

    const toolsets = stringEntries(appConfig.toolsets);

    const tools = objectEntries(appConfig.tools)?.map(t => ({
        tool_type: stringField(t.tool_type) ?? "",
        tool_name: stringField(t.tool_name),
        group_name: stringField(t.group_name),
        tool_description: stringField(t.tool_description) ?? stringField(t.description),
    }));

    const inputModes = stringEntries(appConfig.input_modes);
    const outputModes = stringEntries(appConfig.output_modes);

    return {
        name: agentName,
        description,
        instruction,
        skills,
        tools,
        toolsets,
        inputModes,
        outputModes,
    };
}

export function extractConfigName(text: string): string {
    const connector = extractConnectorMetadata(text);
    if (connector) return connector.name || "Untitled";

    const parsed = parseFirstApp(text);
    return parsed?.name || "Untitled";
}

export interface ConnectorMetadata {
    name: string;
    type: string;
    subtype: string;
    description?: string;
    icon?: string;
    values?: Record<string, unknown>;
}

export function extractSkillMetadata(yamlText: string): { name: string; description: string } | null {
    try {
        const doc = yaml.load(yamlText, { json: true });
        if (!doc || typeof doc !== "object") return null;
        const obj = doc as Record<string, unknown>;
        const name = typeof obj.name === "string" ? obj.name.trim() : "";
        const description = typeof obj.description === "string" ? obj.description.trim() : "";
        if (!name || !description) return null;
        return { name, description };
    } catch {
        return null;
    }
}

function stringArrayOrUndefined(value: unknown): string[] | undefined {
    if (!Array.isArray(value)) return undefined;
    const strings = value.filter((v): v is string => typeof v === "string");
    return strings.length > 0 ? strings : undefined;
}

function skillEntryList<T>(value: unknown, toEntry: (item: Record<string, unknown>) => T | null): T[] | undefined {
    if (!Array.isArray(value)) return undefined;
    const entries: T[] = [];
    for (const item of value) {
        if (!item || typeof item !== "object") continue;
        const entry = toEntry(item as Record<string, unknown>);
        if (entry) entries.push(entry);
    }
    return entries.length > 0 ? entries : undefined;
}

export function extractSkillBundleConfig(yamlText: string): SkillBundleConfig | null {
    const identity = extractSkillMetadata(yamlText);
    if (!identity) return null;
    const obj = yaml.load(yamlText, { json: true }) as Record<string, unknown>;

    const config: SkillBundleConfig = { ...identity };
    if (typeof obj.instruction_content === "string" && obj.instruction_content.trim()) {
        config.instruction_content = obj.instruction_content;
    }
    config.tags = stringArrayOrUndefined(obj.tags);
    config.examples = stringArrayOrUndefined(obj.examples);
    config.required_scopes = stringArrayOrUndefined(obj.required_scopes);
    config.required_builtins = stringArrayOrUndefined(obj.required_builtins);
    if (Array.isArray(obj.tools)) {
        const tools = obj.tools.filter((t): t is Record<string, unknown> => !!t && typeof t === "object");
        if (tools.length > 0) config.tools = tools;
    }
    config.references = skillEntryList<SkillConfigReference>(obj.references, item => {
        if (typeof item.name !== "string" || !item.name) return null;
        return {
            name: item.name,
            ...(typeof item.content === "string" ? { content: item.content } : {}),
            ...(typeof item.artifact === "string" ? { artifact: item.artifact } : {}),
        };
    });
    config.assets = skillEntryList<SkillConfigAsset>(obj.assets, item => {
        if (typeof item.name !== "string" || !item.name) return null;
        return {
            name: item.name,
            artifact: typeof item.artifact === "string" ? item.artifact : "",
        };
    });
    return config;
}

export interface SkillDiagramConfig {
    references: string[];
    tools: string[];
}

export function extractSkillConfig(yamlText: string): SkillDiagramConfig | null {
    try {
        const doc = yaml.load(yamlText, { json: true });
        if (!doc || typeof doc !== "object") return null;
        const obj = doc as Record<string, unknown>;
        const names = (value: unknown, keys: string[]): string[] => {
            if (!Array.isArray(value)) return [];
            const out: string[] = [];
            for (const item of value) {
                if (typeof item === "string") {
                    if (item.trim()) out.push(item.trim());
                    continue;
                }
                if (item && typeof item === "object") {
                    for (const k of keys) {
                        const n = (item as Record<string, unknown>)[k];
                        if (typeof n === "string" && n.trim()) {
                            out.push(n.trim());
                            break;
                        }
                    }
                }
            }
            return out;
        };
        return {
            references: names(obj.references, ["name"]),
            tools: names(obj.tools, ["tool_name", "name"]),
        };
    } catch {
        return null;
    }
}

export function skillToolNames(componentTools: string[] | undefined, configTools: string[] | undefined): string[] {
    return [...new Set([...(componentTools ?? []), ...(configTools ?? [])])];
}

export function extractConnectorMetadata(jsonText: string): ConnectorMetadata | null {
    try {
        const doc = JSON.parse(jsonText);
        if (!doc || typeof doc !== "object") return null;
        if (!CONNECTOR_CONFIG_TYPES.has(doc.type)) return null;
        return {
            name: doc.name || "",
            type: doc.type,
            subtype: doc.subtype || "",
            description: doc.description,
            icon: typeof doc.icon === "string" ? doc.icon : undefined,
            values: doc.values,
        };
    } catch {
        return null;
    }
}

export function extractA2AAgentMetadata(jsonText: string): CreateRemoteAgentRequest | null {
    try {
        const doc = JSON.parse(jsonText);
        if (!doc || typeof doc !== "object") return null;
        if (typeof doc.agentUrl !== "string" || typeof doc.agentCardLocation !== "string" || typeof doc.name !== "string") {
            return null;
        }
        if (!doc.agentAuthConfig || typeof doc.agentAuthConfig !== "object") {
            doc.agentAuthConfig = { type: "none" };
        }
        return doc as CreateRemoteAgentRequest;
    } catch {
        return null;
    }
}

export interface GatewayEventHandler {
    name: string;
    target_agent_name?: string;
    target_workflow_name?: string;
    on_success?: string;
    on_error?: string;
    subscriptions?: Array<{ topic: string; qos?: number }>;
    input_expression?: string;
    payload_format?: string;
}

export interface GatewayOutputHandler {
    name: string;
    topic_expression?: string;
}

export interface GatewayMetadata {
    name: string;
    eventHandlers: GatewayEventHandler[];
    outputHandlers: GatewayOutputHandler[];
    hasCustomBroker: boolean;
}

export function extractGatewayMetadata(yamlText: string): GatewayMetadata | null {
    const parsed = parseFirstApp(yamlText);
    if (!parsed) return null;

    const { name, appConfig } = parsed;
    const eventHandlers = appConfig.event_handlers as GatewayEventHandler[] | undefined;
    if (!Array.isArray(eventHandlers)) return null;

    const outputHandlers = (appConfig.output_handlers as GatewayOutputHandler[] | undefined) ?? [];
    const hasCustomBroker = !!appConfig.event_mesh_broker_config;

    return { name, eventHandlers, outputHandlers, hasCustomBroker };
}

function toPascalCase(s: string): string {
    return s
        .split(/[^a-zA-Z0-9]+/)
        .filter(Boolean)
        .map(part => part.charAt(0).toUpperCase() + part.slice(1))
        .join("");
}

const isPlainObject = (v: unknown): v is Record<string, unknown> => typeof v === "object" && v !== null && !Array.isArray(v);

function withAckPolicyDefault(handlers: unknown[], policy: unknown): unknown[] {
    if (!isPlainObject(policy) || !["mode", "timeout_seconds", "on_failure"].some(key => key in policy)) return handlers;
    return handlers.map(h => (isPlainObject(h) && !isPlainObject(h.acknowledgment_policy) ? { ...h, acknowledgment_policy: { ...policy } } : h));
}

export function extractGatewayCreateRequest(yamlText: string, componentName?: string): CreateGatewayRequest | null {
    const parsed = parseFirstApp(yamlText);
    if (!parsed) return null;

    const { name, appConfig } = parsed;
    const eventHandlers = appConfig.event_handlers;
    if (!Array.isArray(eventHandlers)) return null;

    const rawName = [appConfig.gateway_id, appConfig.agent_name, componentName, name].map(v => (typeof v === "string" ? v.trim() : "")).find(v => v !== "") ?? "";
    const platformName = toPascalCase(rawName) || rawName;

    const agentCard = appConfig.agent_card as Record<string, unknown> | undefined;
    const rawDescription = ((agentCard?.description as string) || (appConfig.description as string) || "").trim();
    const description = rawDescription.length >= 10 ? rawDescription : `Event mesh entrypoint ${platformName}`;

    const values: Record<string, unknown> = { event_handlers: withAckPolicyDefault(eventHandlers, appConfig.acknowledgment_policy) };
    if (Array.isArray(appConfig.output_handlers)) values.output_handlers = appConfig.output_handlers;
    if (appConfig.event_mesh_broker_config) values.event_mesh_broker_config = appConfig.event_mesh_broker_config;
    if (typeof appConfig.default_user_identity === "string") values.default_user_identity = appConfig.default_user_identity;

    return {
        name: platformName,
        description,
        type: "event_mesh",
        values,
        deploy: false,
    };
}
