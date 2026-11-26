// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { useAgentCards } from "@/api/agent-cards";
import { transformAgent, useAgents } from "@/api/agents";
import { useConnectors } from "@/api/connectors";
import { transformRemoteAgent, useRemoteAgents } from "@/api/remote-agents";
import { useBuiltinToolsets, useCustomToolsets } from "@/api/toolsets";

import type { AgentCardInfo, AgentStatus } from "@/types";

interface UseAllAgentsOptions {
    includeWorkflows?: boolean;
}

interface UseAllAgentsReturn {
    agentCards: AgentCardInfo[];
    isLoading: boolean;
    isError: boolean;
    errorMessage?: string;
}

export function useAllAgents({ includeWorkflows = false }: UseAllAgentsOptions = {}): UseAllAgentsReturn {
    const { data: agents = [], isLoading: agentsLoading, isError: agentsError, error: agentsErrorObj } = useAgents();
    const { data: remoteAgents = [], isLoading: remoteAgentsLoading, isError: remoteAgentsError, error: remoteAgentsErrorObj } = useRemoteAgents();
    const { data: builtinToolsets = [], isLoading: builtinToolsetsLoading, isError: builtinToolsetsError, error: builtinToolsetsErrorObj } = useBuiltinToolsets();
    const { data: customToolsets = [], isLoading: customToolsetsLoading, isError: customToolsetsError, error: customToolsetsErrorObj } = useCustomToolsets();
    const { data: connectors = [], isLoading: connectorsLoading, isError: connectorsError, error: connectorsErrorObj } = useConnectors();
    const { data: discoveredAgentCards = [], isLoading: agentCardsLoading, isError: agentCardsError, error: agentCardsErrorObj } = useAgentCards();

    const discoveredAgentsByName = useMemo(() => new Map(discoveredAgentCards.map(card => [card.name, card])), [discoveredAgentCards]);

    const userAgentCards = useMemo<AgentCardInfo[]>(() => agents.filter(agent => !agent.isEphemeral).map(agent => transformAgent(agent, builtinToolsets, connectors, customToolsets)), [agents, builtinToolsets, customToolsets, connectors]);

    const remoteAgentCards = useMemo<AgentCardInfo[]>(
        () =>
            remoteAgents.map(remote => {
                const card = transformRemoteAgent(remote);
                const live = discoveredAgentsByName.get(card.name);
                const isActive = !!live;
                const discoveryAbsent = !isActive && card.agentCardLocation !== "none";
                const isDowngraded = (card.status === "deployed" || card.status === "degraded") && discoveryAbsent;
                const status = isDowngraded ? "disconnected" : card.status;
                const runtimeProblems = isDowngraded ? undefined : card.runtimeProblems;
                const deployedWithLiveSkills = card.deployed && live ? { ...card.deployed, skills: live.skills, description: live.description } : card.deployed;
                return { ...card, isActive, status, runtimeProblems, deployed: deployedWithLiveSkills };
            }),
        [remoteAgents, discoveredAgentsByName]
    );

    const dbBackedAgentNames = useMemo(() => new Set<string>([...userAgentCards.map(a => a.name), ...remoteAgentCards.map(a => a.name)]), [userAgentCards, remoteAgentCards]);

    const pureDiscoveredCards = useMemo<AgentCardInfo[]>(
        () =>
            discoveredAgentCards
                .filter(card => !dbBackedAgentNames.has(card.name) && (includeWorkflows || !card.isWorkflow) && !card.isEphemeral)
                .map(card => {
                    const status: AgentStatus = card.runtimeProblems?.length ? "degraded" : "deployed";
                    return { ...card, status, type: "discovered" as const };
                }),
        [discoveredAgentCards, dbBackedAgentNames, includeWorkflows]
    );

    const agentCards = useMemo<AgentCardInfo[]>(() => [...userAgentCards, ...remoteAgentCards, ...pureDiscoveredCards], [userAgentCards, remoteAgentCards, pureDiscoveredCards]);

    const isError = [agentsError, remoteAgentsError, builtinToolsetsError, customToolsetsError, connectorsError, agentCardsError].some(Boolean);
    const errorMessage = (agentsErrorObj ?? remoteAgentsErrorObj ?? builtinToolsetsErrorObj ?? customToolsetsErrorObj ?? connectorsErrorObj ?? agentCardsErrorObj)?.message;

    const isLoading = [agentsLoading, remoteAgentsLoading, builtinToolsetsLoading, customToolsetsLoading, connectorsLoading, agentCardsLoading].some(Boolean);

    return { agentCards, isLoading, isError, errorMessage };
}
