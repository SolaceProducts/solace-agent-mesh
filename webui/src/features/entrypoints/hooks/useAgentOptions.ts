// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { useAgentCards } from "@/api/agent-cards";
import { useAgents } from "@/api/agents";
import { useRemoteAgents } from "@/api/remote-agents";

import type { AgentStatus, DeploymentStatus } from "@/types/models";

const DEPLOYMENT_STATUSES: ReadonlySet<DeploymentStatus | ""> = new Set<DeploymentStatus | "">(["", "not_deployed", "deployed", "deploy_failed"]);

function narrowDeploymentStatus(status: AgentStatus | undefined): DeploymentStatus | "" | undefined {
    if (status === undefined) return undefined;
    return DEPLOYMENT_STATUSES.has(status as DeploymentStatus | "") ? (status as DeploymentStatus | "") : undefined;
}

export interface AgentOption {
    name: string;
    displayName: string;
    deploymentStatus?: DeploymentStatus | "";
    agentId?: string;
}

export function findAgentOption(options: AgentOption[], key: string | undefined | null): AgentOption | undefined {
    if (!key) return undefined;
    return options.find(a => a.name === key) ?? options.find(a => a.displayName === key);
}

export function isAgentNotDeployed(option: AgentOption | undefined): boolean {
    if (!option) return false;
    if (option.deploymentStatus === undefined) return false;
    return option.deploymentStatus !== "deployed";
}

const EMPTY: never[] = [];

export const useAgentOptions = (): AgentOption[] => {
    const { data: dbAgents = EMPTY } = useAgents();
    const { data: remoteAgents = EMPTY } = useRemoteAgents();
    const { data: discoveredCards = EMPTY } = useAgentCards();

    return useMemo(() => {
        const byName = new Map<string, AgentOption>();

        for (const agent of dbAgents) {
            if (agent.isEphemeral) continue;
            const brokerName = agent.instanceName ?? agent.name;
            byName.set(brokerName, {
                name: brokerName,
                displayName: agent.name,
                deploymentStatus: narrowDeploymentStatus(agent.deploymentStatus),
                agentId: agent.id,
            });
        }

        for (const remote of remoteAgents) {
            const brokerName = remote.instanceName ?? remote.name;
            if (byName.has(brokerName)) continue;
            byName.set(brokerName, {
                name: brokerName,
                displayName: remote.name,
                deploymentStatus: narrowDeploymentStatus(remote.deploymentStatus),
                agentId: remote.id,
            });
        }

        for (const card of discoveredCards) {
            if (card.isWorkflow || card.isEphemeral) continue;
            if (byName.has(card.name)) continue;
            byName.set(card.name, { name: card.name, displayName: card.displayName || card.name });
        }

        return Array.from(byName.values()).sort((a, b) => a.displayName.localeCompare(b.displayName));
    }, [dbAgents, remoteAgents, discoveredCards]);
};
