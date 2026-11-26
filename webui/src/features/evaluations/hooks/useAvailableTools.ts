// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { useAgentCards } from "@/api/agent-cards";

export function useAvailableTools(): string[] {
    const { data: agentCards = [] } = useAgentCards();

    return useMemo(() => {
        const toolSet = new Set<string>();
        for (const card of agentCards) {
            toolSet.add(`peer_${card.name}`);
            for (const tool of card.tools ?? []) {
                if (tool.name) toolSet.add(tool.name);
            }
        }
        return Array.from(toolSet).sort();
    }, [agentCards]);
}
