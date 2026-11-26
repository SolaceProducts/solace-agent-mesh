// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Agent, AgentSkillFE, AgentStatus, Connector, SkillConfigBinding, SyncStatus, ToolRegistration, ToolsetConfigBinding } from "@/types";

import { postAgentDeploymentV2 } from "../agent-deployments/v2";
import { fetchAllPages, instantToIso, pageQuery, platformV2, type RuntimeStatusV2, toRuntimeStatus, withoutNulls } from "../common";

import type { CreateAgentRequest, SavedAgent } from "./service";

interface AgentSkillV2 extends Omit<AgentSkillFE, "id"> {
    id: string | null;
}

interface ToolsetConfigV2 {
    toolsetName: string;
    configValues: ToolsetConfigBinding["configValues"] | null;
    volumeBindings: Record<string, string> | null;
    networkBindings: Record<string, string> | null;
}

interface SkillConfigV2 {
    skillId: string;
    skillName: string | null;
    configValues: SkillConfigBinding["configValues"] | null;
}

interface ToolRegistrationV2 extends Omit<ToolRegistration, "connectorId" | "name" | "error"> {
    connectorId: string | null;
    name: string | null;
    error: string | null;
}

interface DeployedAgentV2 {
    name: string;
    description: string;
    systemPrompt: string;
    toolsets: string[];
    inputModes: string[];
    outputModes: string[];
    connectorIds: string[];
    additionalConfigurations: Record<string, unknown> | null;
    skillIds: string[];
    skillRefs: string[];
    skills: AgentSkillV2[];
    deployedTime: string | null;
    deployedBy: string;
}

interface AgentV2 {
    id: string;
    agentType: NonNullable<Agent["type"]>;
    name: string;
    instanceName: string;
    description: string;
    systemPrompt: string;
    toolsets: string[];
    toolsetExcludedTools: Record<string, string[]> | null;
    toolsetConfigs: ToolsetConfigV2[];
    skills: AgentSkillV2[];
    skillRefs: string[];
    skillIds: string[];
    skillConfigs: SkillConfigV2[];
    inputModes: string[];
    outputModes: string[];
    connectorIds: string[];
    modelProvider: string[];
    additionalConfigurations: Record<string, unknown> | null;
    deploymentStatus: AgentStatus;
    syncStatus: SyncStatus | null;
    deployed: DeployedAgentV2 | null;
    runtimeStatus: RuntimeStatusV2;
    toolRegistrations: ToolRegistrationV2[];
    isEphemeral: boolean;
    sourceSessionId: string | null;
    sourceComponentName: string | null;
    sourceComponentKind: string | null;
    expiresTime: string | null;
    createdBy: string;
    updatedBy: string;
    createdTime: string | null;
    updatedTime: string | null;
}

const toSkill = (skill: AgentSkillV2): AgentSkillFE => withoutNulls(skill);

const toToolsetConfig = ({ configValues, ...binding }: ToolsetConfigV2): ToolsetConfigBinding => ({ ...withoutNulls(binding), configValues: configValues ?? {} });

const toSkillConfig = ({ configValues, ...binding }: SkillConfigV2): SkillConfigBinding => ({ ...withoutNulls(binding), configValues: configValues ?? {} });

function toDeployedAgent(d: DeployedAgentV2): Agent {
    return {
        name: d.name,
        description: d.description,
        systemPrompt: d.systemPrompt,
        toolsets: d.toolsets,
        inputModes: d.inputModes,
        outputModes: d.outputModes,
        connectors: d.connectorIds,
        connectorSnapshots: d.connectorIds.map(id => ({ id }) as Connector),
        additionalConfigurations: d.additionalConfigurations ?? undefined,
        skillIds: d.skillIds,
        skillRefs: d.skillRefs,
        skills: d.skills.map(toSkill),
        deployedAt: instantToIso(d.deployedTime),
        deployedBy: d.deployedBy,
    };
}

function toAgent(a: AgentV2): Agent & { id: string } {
    return {
        id: a.id,
        type: a.agentType,
        name: a.name,
        instanceName: a.instanceName,
        description: a.description,
        systemPrompt: a.systemPrompt,
        toolsets: a.toolsets,
        toolsetExcludedTools: a.toolsetExcludedTools ?? undefined,
        toolsetConfigs: a.toolsetConfigs.map(toToolsetConfig),
        skills: a.skills.map(toSkill),
        skillRefs: a.skillRefs,
        skillIds: a.skillIds,
        skillConfigs: a.skillConfigs.map(toSkillConfig),
        inputModes: a.inputModes,
        outputModes: a.outputModes,
        connectors: a.connectorIds,
        modelProvider: a.modelProvider,
        additionalConfigurations: a.additionalConfigurations ?? undefined,
        deploymentStatus: a.deploymentStatus,
        syncStatus: a.syncStatus ?? undefined,
        deployed: a.deployed ? toDeployedAgent(a.deployed) : undefined,
        runtimeStatus: toRuntimeStatus(a.runtimeStatus),
        toolRegistrations: a.toolRegistrations.map(r => withoutNulls(r)),
        isEphemeral: a.isEphemeral,
        sourceSessionId: a.sourceSessionId ?? undefined,
        sourceComponentName: a.sourceComponentName ?? undefined,
        sourceComponentKind: a.sourceComponentKind ?? undefined,
        expiresAt: a.expiresTime === null ? undefined : instantToIso(a.expiresTime),
        createdBy: a.createdBy,
        updatedBy: a.updatedBy,
        createdAt: instantToIso(a.createdTime),
        updatedAt: instantToIso(a.updatedTime),
    };
}

function toAgentWrite({ type: _type, connectors: connectorIds, deploy: _deploy, ...fields }: CreateAgentRequest) {
    return { ...fields, connectorIds };
}

async function deployAfterSave(saved: Agent & { id: string }, action: "deploy" | "update"): Promise<SavedAgent> {
    try {
        const deployment = await postAgentDeploymentV2({ agentId: saved.id, action });
        if (deployment.status !== "failed") return { ...saved, deploymentStatus: "deployed" };
        return { ...saved, deploymentStatus: "deploy_failed", deployError: deployment.errorMessage };
    } catch (error) {
        return { ...saved, deploymentStatus: "deploy_failed", deployError: error instanceof Error ? error.message : undefined };
    }
}

const agentsPage = (filter = "") => fetchAllPages(params => platformV2.list<AgentV2>(`/agents?${filter}${pageQuery(params)}`));

export async function getAllAgentsV2(): Promise<Agent[]> {
    const all = await agentsPage();
    return all.map(toAgent).filter(a => !a.isEphemeral);
}

export async function getAgentV2(id: string): Promise<Agent> {
    return toAgent(await platformV2.get<AgentV2>(`/agents/${id}`));
}

export async function createAgentV2(payload: CreateAgentRequest): Promise<SavedAgent> {
    const saved = toAgent(await platformV2.post<AgentV2>("/agents", { ...toAgentWrite(payload), agentType: payload.type }));
    return payload.deploy && saved.systemPrompt ? deployAfterSave(saved, "deploy") : saved;
}

export async function updateAgentV2(id: string, payload: CreateAgentRequest): Promise<SavedAgent> {
    const saved = toAgent(await platformV2.put<AgentV2>(`/agents/${id}`, toAgentWrite(payload)));
    if (!payload.deploy) return saved;
    return deployAfterSave(saved, saved.deploymentStatus === "deployed" ? "update" : "deploy");
}

export async function deleteAgentV2(id: string): Promise<void> {
    await platformV2.delete(`/agents/${id}`);
}

export async function getConnectorAgentsV2(connectorId: string): Promise<Agent[]> {
    const all = await agentsPage(`connectorId=${encodeURIComponent(connectorId)}&`);
    return all.map(toAgent);
}
