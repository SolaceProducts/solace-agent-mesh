// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { isOperationalStatus } from "@/common/status";

import type { AgentCardInfo, AgentStatus, RuntimeStatus } from "@/types";

export type DeploymentFilter = "deployed" | "undeployed";

export function isRemoteNotRunning(agent: AgentCardInfo): boolean {
    return agent.type === "external" && agent.status === "stopped";
}

export function isDeployed(agent: AgentCardInfo): boolean {
    return agent.status === "deployed" || agent.status === "degraded" || agent.status === "disconnected" || isRemoteNotRunning(agent);
}

export function filterAgentsByDeployment(agents: AgentCardInfo[], filter: DeploymentFilter): AgentCardInfo[] {
    if (filter === "deployed") return agents.filter(isDeployed);
    return agents.filter(agent => !isDeployed(agent));
}

export type AgentKind = "agent" | "workflow";

export const kindOf = (agent: AgentCardInfo): AgentKind => (agent.isWorkflow ? "workflow" : "agent");

export type AgentMeshTab = "agents" | "workflows";

export const tabToKind = (tab: string | null): AgentKind => (tab === "workflows" ? "workflow" : "agent");

export const kindToTab = (kind: AgentKind): AgentMeshTab => (kind === "workflow" ? "workflows" : "agents");

export function filterAgentsByKind(agents: AgentCardInfo[], kind: AgentKind): AgentCardInfo[] {
    return agents.filter(agent => kindOf(agent) === kind);
}

export function sortAgentsByDisplayName(agents: AgentCardInfo[]): AgentCardInfo[] {
    return [...agents].sort((a, b) => (a.displayName ?? "").localeCompare(b.displayName ?? "", undefined, { sensitivity: "base" }));
}

function tabForStoppedDeployedAgent(agentType?: AgentCardInfo["type"]): DeploymentFilter {
    return agentType === "external" ? "deployed" : "undeployed";
}

export function agentManagementTabFor(deploymentStatus: AgentStatus | undefined, runtimeStatus?: RuntimeStatus, agentType?: AgentCardInfo["type"]): DeploymentFilter {
    const isOperational = isOperationalStatus(runtimeStatus?.status);
    if (deploymentStatus === "deployed" && !isOperational) return tabForStoppedDeployedAgent(agentType);
    if (deploymentStatus === "deployed" || deploymentStatus === "disconnected") return "deployed";
    return "undeployed";
}

export function agentManagementUrl(agentId: string | undefined, tab: DeploymentFilter): string {
    const params = new URLSearchParams();
    if (agentId) params.set("id", agentId);
    params.set("tab", tab);
    return `/agents?${params.toString()}`;
}
