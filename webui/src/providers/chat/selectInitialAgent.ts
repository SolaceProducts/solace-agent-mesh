// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { UI_MODE, type UIModeName } from "@/providers/contexts/UIModeContext";

import type { AgentCardInfo } from "@/types";
import { pickDefaultChatAgent } from "@/utils";

export function selectInitialAgent({
    agents,
    urlAgentName,
    projectDefaultAgentId,
    lastUsedAgentName,
    lastUsedCandidates,
    mode,
}: {
    agents: AgentCardInfo[];
    urlAgentName?: string | null;
    projectDefaultAgentId?: string | null;
    lastUsedAgentName?: string | null;
    lastUsedCandidates?: AgentCardInfo[];
    mode: UIModeName;
}): AgentCardInfo | undefined {
    if (agents.length === 0) return undefined;

    if (mode === UI_MODE.agentMode) {
        if (!urlAgentName) return undefined;
        return agents.find(agent => agent.name === urlAgentName);
    }

    if (urlAgentName) {
        const urlAgent = agents.find(agent => agent.name === urlAgentName);
        if (urlAgent) {
            console.log(`Using URL parameter agent: ${urlAgent.name}`);
            return urlAgent;
        }
        console.warn(`URL parameter agent "${urlAgentName}" not found in available agents, falling back to priority order`);
    }

    if (projectDefaultAgentId) {
        const projectDefault = agents.find(agent => agent.name === projectDefaultAgentId);
        if (projectDefault) {
            console.log(`Using project default agent: ${projectDefault.name}`);
            return projectDefault;
        }
        console.warn(`Project default agent "${projectDefaultAgentId}" not found in agent registry; falling back to default agent`);
    }

    if (lastUsedAgentName) {
        const lastUsed = (lastUsedCandidates ?? agents).find(agent => agent.name === lastUsedAgentName && !agent.isInternal && !agent.isWorkflow);
        if (lastUsed) {
            console.log(`Using last used agent: ${lastUsed.name}`);
            return lastUsed;
        }
    }

    return pickDefaultChatAgent(agents) ?? agents[0];
}
