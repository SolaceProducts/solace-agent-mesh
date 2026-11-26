// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { toolsetRefLabel } from "@/utils/toolsetRefLabel";

import type { AgentSkill } from "@/types/a2a";
import { type AgentWelcomeConfig, type WelcomeSuggestion } from "@/types/agent";

import type { Agent, AgentCardInfo, AgentStatus, Connector, Toolset } from "@/types";

function agentDisplayStatus(deploymentStatus: Agent["deploymentStatus"], runtimeStatus: Agent["runtimeStatus"]): AgentStatus | undefined {
    if (deploymentStatus !== "deployed") return deploymentStatus;
    if (runtimeStatus?.status === "degraded") return "degraded";
    if (runtimeStatus?.status !== "running") return "stopped";
    return deploymentStatus;
}

const extractWelcomeFromAdditionalConfigurations = (additionalConfigurations: unknown): AgentWelcomeConfig | undefined => {
    if (!additionalConfigurations || typeof additionalConfigurations !== "object") return undefined;
    const agentCard = (additionalConfigurations as { agentCard?: { welcome?: { message?: string; suggestions?: Array<{ label?: string; prompt?: string; autoSend?: boolean }> } } }).agentCard;
    const welcome = agentCard?.welcome;
    if (!welcome) return undefined;
    const suggestions: WelcomeSuggestion[] | undefined = welcome.suggestions?.map(s => ({
        label: s.label ?? "",
        prompt: s.prompt ?? "",
        auto_send: s.autoSend ?? false,
    }));
    if (!welcome.message && !suggestions?.length) return undefined;
    return {
        welcome_message: welcome.message,
        suggestions,
    };
};

const isInternalAgent = (additionalConfigurations: unknown): boolean => !!additionalConfigurations && typeof additionalConfigurations === "object" && (additionalConfigurations as { internal?: unknown }).internal === true;

export const transformAgent = (agent: Agent, toolsets: Toolset[], connectors: Connector[], customToolsets?: Toolset[]): AgentCardInfo => {
    const deployedAgentVersion = agent.deployed ? transformAgent(agent.deployed, toolsets, connectors, customToolsets) : undefined;
    const normalizedAgentName = agent.instanceName ?? agent.name ?? "";
    const displayStatus = agentDisplayStatus(agent.deploymentStatus, agent.runtimeStatus);
    const runtimeProblems = displayStatus === "degraded" ? agent.runtimeStatus?.problems : undefined;

    if (deployedAgentVersion) {
        deployedAgentVersion.name = normalizedAgentName;
        deployedAgentVersion.status = displayStatus;
        deployedAgentVersion.connectors = agent.deployed?.connectorSnapshots?.map(connector => connector.id) ?? [];
        deployedAgentVersion.connectorObjects =
            agent.deployed?.connectorSnapshots?.map(connector => {
                const currentConnector = connectors.find(c => c.id === connector.id);
                const snapshotName = connector.name ?? currentConnector?.name ?? "Unknown";
                const displayName = currentConnector ? snapshotName : `${snapshotName} (NOT FOUND)`;

                return {
                    ...connector,
                    type: connector.type ?? currentConnector?.type,
                    subtype: connector.subtype ?? currentConnector?.subtype,
                    name: displayName,
                    description: currentConnector?.description ?? connector.description ?? "N/A",
                    values: currentConnector?.values ?? {},
                };
            }) ?? [];
        deployedAgentVersion.connectorObjects.sort((a, b) => a.name.localeCompare(b.name));
        deployedAgentVersion.createdBy = agent.createdBy;
        deployedAgentVersion.toolRegistrations = agent.toolRegistrations;
        deployedAgentVersion.runtimeProblems = runtimeProblems;
    }

    const agentInfo: AgentCardInfo = {
        name: normalizedAgentName,
        description: agent.description ?? "N/A",
        url: "",
        version: agent.version ?? "N/A",
        protocolVersion: "",
        defaultInputModes: agent.inputModes || ["text"],
        defaultOutputModes: agent.outputModes || ["text"],
        skills:
            agent.skills?.map(
                skill =>
                    ({
                        id: skill.name,
                        name: skill.name,
                        description: skill.description,
                        tags: [],
                    }) as AgentSkill
            ) || [],
        connectors: agent.connectors || [],
        connectorObjects: [],
        skillIds: agent.skillIds,
        skillRefs: agent.skillRefs,
        capabilities: {},
        displayName: agent.name ?? "N/A",
        peerAgents: [],
        tools: [],
        status: displayStatus,
        syncStatus: agent.syncStatus,
        type: "user",
        deployed: deployedAgentVersion,
        createdBy: agent.createdBy,
        welcome: extractWelcomeFromAdditionalConfigurations(agent.additionalConfigurations),
        isWorkflow: agent.type === "workflow",
        isInternal: isInternalAgent(agent.additionalConfigurations),
        toolRegistrations: agent.toolRegistrations,
        runtimeProblems,
    };

    if (agent.skills && agent.skills.length > 0) {
        agentInfo.skills = agent.skills.map(skill => ({
            id: skill.name,
            name: skill.name,
            description: skill.description,
            tags: [],
        }));
        agentInfo.skills.sort((a, b) => a.name.localeCompare(b.name));
    }

    if (agent.toolsets && agent.toolsets.length > 0) {
        agent.toolsets.forEach(toolsetName => {
            const builtin = toolsets?.find(ts => ts.id === toolsetName);
            if (builtin) {
                agentInfo.tools?.push({ id: builtin.id, name: builtin.name, description: builtin.description || "", tags: [] });
                return;
            }
            const custom = customToolsets?.find(ts => ts.name === toolsetName);
            if (custom) {
                agentInfo.tools?.push({ id: custom.id, name: custom.name, description: custom.description || "", tags: [] });
                return;
            }
            agentInfo.tools?.push({ id: toolsetName, name: toolsetRefLabel(toolsetName), description: "", tags: [] });
        });
        agentInfo.tools?.sort((a, b) => a.name.localeCompare(b.name));
    }

    if (connectors && agent.connectors && agent.connectors.length > 0) {
        agentInfo.connectorObjects = [];
        agent.connectors.forEach(connectorId => {
            const connector = connectors.find(c => c.id === connectorId);
            if (connector) {
                agentInfo.connectorObjects?.push(connector);
            }
        });
        agentInfo.connectorObjects?.sort((a, b) => a.name.localeCompare(b.name));
    }

    return agentInfo;
};
