// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Workflow } from "@/types/workflow";

import type { Agent, AgentSkillFE, Connector, RemoteAgent, RuntimeStatus, Skill, Toolset } from "@/types";

export const toV2Instant = (value: string | number | null | undefined): string | null => (value || value === 0 ? new Date(value).toISOString() : null);

const runtimeStatusV2 = (runtimeStatus: RuntimeStatus | undefined) => ({
    status: runtimeStatus?.status ?? "stopped",
    problems: runtimeStatus?.status === "degraded" ? runtimeStatus.problems.map(p => ({ key: p.id, severity: p.severity, detail: p.detail, sinceTime: toV2Instant(p.since) })) : [],
});

const skillV2 = (skill: AgentSkillFE) => ({ ...skill, id: skill.id ?? null });

export function agentToV2(agent: Agent) {
    const deployed = agent.deployed;
    return {
        id: agent.id ?? "",
        type: "agent",
        agentType: agent.type ?? "standard",
        name: agent.name,
        instanceName: agent.instanceName ?? "",
        description: agent.description,
        systemPrompt: agent.systemPrompt ?? "",
        toolsets: agent.toolsets ?? [],
        toolsetExcludedTools: agent.toolsetExcludedTools ?? null,
        toolsetConfigs: (agent.toolsetConfigs ?? []).map(c => ({ volumeBindings: null, networkBindings: null, ...c, configValues: c.configValues ?? null })),
        skills: (agent.skills ?? []).map(skillV2),
        skillRefs: agent.skillRefs ?? [],
        skillIds: agent.skillIds ?? [],
        skillConfigs: (agent.skillConfigs ?? []).map(c => ({ skillName: null, ...c, configValues: c.configValues ?? null })),
        inputModes: agent.inputModes ?? [],
        outputModes: agent.outputModes ?? [],
        connectorIds: agent.connectors ?? [],
        modelProvider: agent.modelProvider ?? [],
        additionalConfigurations: agent.additionalConfigurations ?? null,
        deploymentStatus: agent.deploymentStatus ?? "not_deployed",
        syncStatus: agent.syncStatus ?? null,
        deployed: deployed
            ? {
                  name: deployed.name,
                  description: deployed.description,
                  systemPrompt: deployed.systemPrompt ?? "",
                  toolsets: deployed.toolsets ?? [],
                  inputModes: deployed.inputModes ?? [],
                  outputModes: deployed.outputModes ?? [],
                  connectorIds: deployed.connectors ?? [],
                  additionalConfigurations: deployed.additionalConfigurations ?? null,
                  skillIds: deployed.skillIds ?? [],
                  skillRefs: deployed.skillRefs ?? [],
                  skills: (deployed.skills ?? []).map(skillV2),
                  deployedTime: toV2Instant(deployed.deployedAt),
                  deployedBy: deployed.deployedBy ?? "",
              }
            : null,
        runtimeStatus: runtimeStatusV2(agent.runtimeStatus),
        toolRegistrations: (agent.toolRegistrations ?? []).map(r => ({ connectorId: null, name: null, error: null, ...r })),
        isEphemeral: agent.isEphemeral ?? false,
        sourceSessionId: agent.sourceSessionId ?? null,
        sourceComponentName: agent.sourceComponentName ?? null,
        sourceComponentKind: agent.sourceComponentKind ?? null,
        expiresTime: toV2Instant(agent.expiresAt),
        createdBy: agent.createdBy ?? "",
        updatedBy: agent.updatedBy ?? "",
        createdTime: toV2Instant(agent.createdAt),
        updatedTime: toV2Instant(agent.updatedAt),
    };
}

export function connectorToV2(connector: Connector) {
    return {
        id: connector.id,
        type: "connector",
        connectorType: connector.type,
        connectorSubtype: connector.subtype,
        name: connector.name,
        description: connector.description || null,
        values: connector.values,
        agentCount: connector.agentCount ?? 0,
        hasResource: false,
        contentHash: null,
        warnings: connector.warnings ?? [],
        agentRegistrations: [],
        createdBy: connector.createdBy ?? null,
        updatedBy: connector.updatedBy ?? null,
        createdTime: toV2Instant(connector.createdAt),
        updatedTime: toV2Instant(connector.updatedAt),
    };
}

export function toolsetToV2(toolset: Toolset) {
    const tools = toolset.tools?.length
        ? toolset.tools.map(t => ({
              toolName: t.toolName,
              description: t.toolDescription ?? null,
              timeoutSeconds: t.timeoutSeconds ?? null,
              sandboxProfile: t.sandboxProfile ?? null,
              parameters: t.parameters ?? null,
              configSchema: t.configSchema ?? null,
          }))
        : toolset.files.map(f => ({ toolName: f.toolName, description: f.toolDescription || null, timeoutSeconds: f.timeoutSeconds ?? null, sandboxProfile: f.sandboxProfile || null, parameters: null, configSchema: f.configSchema ?? null }));
    return {
        id: toolset.id,
        type: "toolset",
        origin: toolset.type,
        name: toolset.name,
        description: toolset.description || null,
        runtime: toolset.runtime ?? null,
        discoveryStatus: toolset.discoveryStatus ?? null,
        discoveryErrors: toolset.discoveryErrors ?? [],
        bundle: toolset.zipFilename ? { filename: toolset.zipFilename, sizeBytes: toolset.zipFileSize ?? null, contentHash: toolset.contentHash ?? null } : null,
        tools,
        agents: (toolset.agents ?? []).map(a => ({ id: a.id, name: a.name, description: a.description ?? null })),
        configSummary: toolset.configSummary ?? null,
        capabilities: {},
        default: toolset.default ?? false,
        aliases: toolset.aliases ?? [],
        createdBy: toolset.createdBy || null,
        updatedBy: toolset.updatedBy || null,
        createdTime: toV2Instant(toolset.createdAt),
        updatedTime: toV2Instant(toolset.updatedAt),
    };
}

type BundledToolV1 = { tool_name?: string; description?: string; instructions?: string; parameters?: unknown; artifact_params?: Record<string, { is_list?: boolean; is_optional?: boolean; filename_key?: string }>; config_schema?: unknown };

const bundledToolV2 = (tool: BundledToolV1) => ({
    toolName: tool.tool_name ?? "",
    description: tool.description ?? null,
    instructions: tool.instructions ?? null,
    parameters: tool.parameters ?? null,
    artifactParams: Object.fromEntries(Object.entries(tool.artifact_params ?? {}).map(([name, p]) => [name, { isList: p.is_list ?? false, isOptional: p.is_optional ?? false, filenameKey: p.filename_key ?? null }])),
    configSchema: tool.config_schema ?? null,
});

export function skillToV2(skill: Skill) {
    return {
        id: skill.id,
        type: "skill",
        origin: skill.origin === "builtin" ? "builtin" : "custom",
        name: skill.name,
        label: skill.label ?? null,
        description: skill.description || null,
        tags: skill.tags ?? [],
        discoveryStatus: skill.discoveryStatus,
        hasReferences: skill.hasReferences,
        hasAssets: skill.hasAssets,
        hasTools: skill.hasTools,
        resourceCount: skill.resourceCount,
        referenceCount: skill.referenceCount,
        assetCount: skill.assetCount,
        bundledTools: ((skill.bundledTools ?? []) as BundledToolV1[]).map(bundledToolV2),
        toolsets: [],
        bundle: skill.contentHash ? { contentHash: skill.contentHash } : null,
        agents: (skill.agents ?? []).map(a => ({ id: a.id, name: a.name, description: a.description ?? null })),
        configSummary: null,
        createdBy: skill.createdBy || null,
        updatedBy: skill.updatedBy || null,
        createdTime: toV2Instant(skill.createdAt),
        updatedTime: toV2Instant(skill.updatedAt),
    };
}

export function workflowToV2(workflow: Workflow) {
    return {
        id: workflow.id,
        type: "workflow",
        name: workflow.name,
        description: workflow.description,
        appConfig: workflow.appConfig,
        deploymentStatus: workflow.deploymentStatus,
        runtimeStatus: runtimeStatusV2(workflow.runtimeStatus),
        createdBy: workflow.createdBy,
        updatedBy: workflow.updatedBy,
        createdTime: toV2Instant(workflow.createdTime),
        updatedTime: toV2Instant(workflow.updatedTime),
    };
}

export function remoteAgentToV2(agent: RemoteAgent) {
    const { deployed, runtimeStatus, createdAt, updatedAt, ...fields } = agent;
    return {
        type: "remoteAgent",
        instanceName: "",
        description: null,
        agentCardUrl: null,
        agentCardHeaders: null,
        agentCardAuthType: null,
        agentCardAuthConfig: null,
        agentAuthConfig: null,
        taskHeaders: null,
        protocolVersion: "auto",
        trustedCardJwksUrls: [],
        requireSignedCard: false,
        skills: [],
        inputModes: [],
        outputModes: [],
        deploymentStatus: "not_deployed",
        syncStatus: null,
        cardData: null,
        cardFetchError: null,
        ...fields,
        runtimeStatus: runtimeStatusV2(runtimeStatus),
        deployed: deployed ? (({ deployedAt, ...rest }) => ({ description: null, agentCardUrl: null, agentCardAuthType: null, ...rest, deployedTime: toV2Instant(deployedAt) }))(deployed) : null,
        createdTime: toV2Instant(createdAt),
        updatedTime: toV2Instant(updatedAt),
    };
}
