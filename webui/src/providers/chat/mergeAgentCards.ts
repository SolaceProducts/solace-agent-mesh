// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { AgentCardInfo, AgentWelcomeConfig } from "@/types";

const backfillWelcome = (userWelcome: AgentWelcomeConfig | undefined, spineWelcome: AgentWelcomeConfig | undefined): AgentWelcomeConfig | undefined => {
    if (!spineWelcome) return userWelcome;
    if (!userWelcome) return spineWelcome;
    const welcomeMessage = userWelcome.welcome_message || spineWelcome.welcome_message;
    const suggestions = userWelcome.suggestions?.length ? userWelcome.suggestions : spineWelcome.suggestions;
    if (welcomeMessage === userWelcome.welcome_message && suggestions === userWelcome.suggestions) return userWelcome;
    return { ...userWelcome, welcome_message: welcomeMessage, suggestions };
};

export function mergeAgentCards(yamlAgentCards: AgentCardInfo[] | null | undefined, userAgentCards: AgentCardInfo[] | null | undefined, remoteAgentCards: AgentCardInfo[] | null | undefined): AgentCardInfo[] {
    const userAgentMap = new Map((userAgentCards || []).map(agent => [agent.name, agent]));
    const remoteAgentMap = new Map((remoteAgentCards || []).map(agent => [agent.name, agent]));

    return (yamlAgentCards || []).map(yamlAgent => {
        const userAgent = userAgentMap.get(yamlAgent.name);
        if (userAgent) {
            const welcome = backfillWelcome(userAgent.welcome, yamlAgent.welcome);
            return welcome === userAgent.welcome ? userAgent : { ...userAgent, welcome };
        }

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
                agentCardLocation: remoteAgent.agentCardLocation,
                status: remoteAgent.status,
                deployed: deployedWithLiveSkills,
                createdBy: remoteAgent.createdBy,
                updatedBy: remoteAgent.updatedBy,
                createdAt: remoteAgent.createdAt,
                updatedAt: remoteAgent.updatedAt,
            };
        }

        return yamlAgent;
    });
}
