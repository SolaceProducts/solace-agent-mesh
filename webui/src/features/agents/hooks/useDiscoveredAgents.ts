// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { useAgentCards } from "@/api/agent-cards";
import { useUserAgentCards } from "@/api/agents";
import { useRemoteAgentCards } from "@/api/remote-agents";

import { isWorkflowAgent } from "@/utils/agentUtils";

import type { AgentCardInfo } from "@/types";

const EMPTY_REMOTE_AGENT_CARDS: AgentCardInfo[] = [];
const EMPTY_USER_AGENT_CARDS: AgentCardInfo[] = [];
const EMPTY_YAML_AGENT_CARDS: AgentCardInfo[] = [];

export interface UseDiscoveredAgentsReturn {
    discoveredAgents: AgentCardInfo[];
    isLoading: boolean;
}

export const useDiscoveredAgents = (): UseDiscoveredAgentsReturn => {
    const { data: yamlAgentCards = EMPTY_YAML_AGENT_CARDS } = useAgentCards();
    const { data: userAgentCards = EMPTY_USER_AGENT_CARDS } = useUserAgentCards();
    const { data: remoteAgentCards = EMPTY_REMOTE_AGENT_CARDS } = useRemoteAgentCards();

    const discoveredAgents = useMemo(() => {
        const userAgentMap = new Map((userAgentCards || []).map(agent => [agent.name, agent]));

        const remoteAgentMap = new Map((remoteAgentCards || []).map(agent => [agent.name, agent]));

        const yamlAgents = (yamlAgentCards || []).map(yamlAgent => {
            const userAgent = userAgentMap.get(yamlAgent.name);
            if (userAgent) return userAgent;

            const remoteAgent = remoteAgentMap.get(yamlAgent.name);
            if (remoteAgent) {
                const deployedWithLiveSkills = remoteAgent.deployed
                    ? {
                          ...remoteAgent.deployed,
                          skills: yamlAgent.skills,
                          description: yamlAgent.description,
                      }
                    : undefined;

                return {
                    ...yamlAgent,
                    type: "external" as const,
                    displayName: remoteAgent.displayName,
                    agentCardLocation: remoteAgent.agentCardLocation,
                    status: remoteAgent.status,
                    syncStatus: remoteAgent.syncStatus,
                    deployed: deployedWithLiveSkills,
                    createdBy: remoteAgent.createdBy,
                    updatedBy: remoteAgent.updatedBy,
                    createdAt: remoteAgent.createdAt,
                    updatedAt: remoteAgent.updatedAt,
                };
            }

            return yamlAgent;
        });

        const yamlAgentNames = new Set((yamlAgentCards || []).map(agent => agent.name));

        const externalAgents = remoteAgentCards || [];
        const registeredExternal = externalAgents.filter(agent => agent.agentCardLocation === "none" && agent.status === "deployed" && !yamlAgentNames.has(agent.name));

        const allAgents = [...yamlAgents, ...registeredExternal];
        return allAgents.filter(agent => !isWorkflowAgent(agent));
    }, [yamlAgentCards, userAgentCards, remoteAgentCards]);

    const isLoading = !yamlAgentCards || !userAgentCards || !remoteAgentCards;

    return { discoveredAgents, isLoading };
};
