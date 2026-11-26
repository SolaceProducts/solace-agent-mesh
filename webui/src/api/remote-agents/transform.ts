// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { AgentSkill } from "@/types/a2a";

import type { AgentCardInfo, RemoteAgent } from "@/types";

function deployedDisplayStatus(remoteAgent: RemoteAgent): AgentCardInfo["status"] {
    if (remoteAgent.deploymentStatus === "deployed" && remoteAgent.runtimeStatus?.status === "degraded") return "degraded";
    if (remoteAgent.agentCardLocation === "none") return remoteAgent.deploymentStatus;
    return remoteAgent.deploymentStatus === "deployed" && remoteAgent.runtimeStatus?.status !== "running" ? "stopped" : remoteAgent.deploymentStatus;
}

export function transformRemoteAgent(remoteAgent: RemoteAgent): AgentCardInfo {
    const normalizedAgentName = remoteAgent.instanceName ?? remoteAgent.name;
    const displayStatus = deployedDisplayStatus(remoteAgent);
    const runtimeProblems = displayStatus === "degraded" ? remoteAgent.runtimeStatus?.problems : undefined;
    const deployedRemoteProtocol = remoteAgent.deploymentStatus === "deployed" ? (remoteAgent.remoteProtocol ?? undefined) : undefined;
    const draftRemoteProtocol = remoteAgent.syncStatus === "out_of_sync" ? undefined : deployedRemoteProtocol;

    const agentInfo: AgentCardInfo = {
        name: normalizedAgentName,
        description: remoteAgent.description ?? "N/A",
        url: remoteAgent.agentUrl,
        version: "N/A",
        protocolVersion: "",
        defaultInputModes: remoteAgent.inputModes || ["text"],
        defaultOutputModes: remoteAgent.outputModes || ["text"],
        skills:
            remoteAgent.skills?.map(
                skill =>
                    ({
                        id: skill.name,
                        name: skill.name,
                        description: skill.description,
                        tags: [],
                    }) as AgentSkill
            ) || [],
        connectors: [],
        connectorObjects: [],
        capabilities: {},
        displayName: remoteAgent.name,
        peerAgents: [],
        tools: [],
        status: displayStatus,
        syncStatus: remoteAgent.syncStatus,
        type: "external",
        createdBy: remoteAgent.createdBy,
        agentCardLocation: remoteAgent.agentCardLocation,
        runtimeProblems,
        remoteProtocol: draftRemoteProtocol,
    };

    if (agentInfo.skills && agentInfo.skills.length > 0) {
        agentInfo.skills.sort((a, b) => a.name.localeCompare(b.name));
    }

    if (remoteAgent.deployed) {
        const deployedSnapshot = remoteAgent.deployed;
        const deployedAgentInfo: AgentCardInfo = {
            name: normalizedAgentName,
            description: deployedSnapshot.description ?? "N/A",
            url: deployedSnapshot.agentUrl,
            version: "N/A",
            protocolVersion: "",
            defaultInputModes: Array.isArray(deployedSnapshot.inputModes) ? deployedSnapshot.inputModes : ["text"],
            defaultOutputModes: Array.isArray(deployedSnapshot.outputModes) ? deployedSnapshot.outputModes : ["text"],
            skills: (Array.isArray(deployedSnapshot.skills) ? deployedSnapshot.skills : []).map(
                skill =>
                    ({
                        id: skill.name,
                        name: skill.name,
                        description: skill.description,
                        tags: [],
                    }) as AgentSkill
            ),
            connectors: [],
            connectorObjects: [],
            capabilities: {},
            displayName: deployedSnapshot.name ?? remoteAgent.name,
            peerAgents: [],
            tools: [],
            status: displayStatus,
            type: "external",
            createdBy: deployedSnapshot.deployedBy,
            agentCardLocation: deployedSnapshot.agentCardLocation as "well_known" | "custom" | "none",
            runtimeProblems,
            remoteProtocol: deployedRemoteProtocol,
        };

        if (deployedAgentInfo.skills && deployedAgentInfo.skills.length > 0) {
            deployedAgentInfo.skills.sort((a, b) => a.name.localeCompare(b.name));
        }

        agentInfo.deployed = deployedAgentInfo;
    }

    return agentInfo;
}
