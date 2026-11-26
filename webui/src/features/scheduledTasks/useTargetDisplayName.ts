// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo } from "react";

import { useAgentCards } from "@/api/agent-cards";

import { findAgentOption, useAgentOptions } from "@/features/entrypoints/hooks/useAgentOptions";

export function useTargetDisplayName(targetAgentName: string | undefined | null): string {
    const resolveTargetDisplayName = useTargetDisplayNameResolver();
    return useMemo(() => resolveTargetDisplayName(targetAgentName), [resolveTargetDisplayName, targetAgentName]);
}

export function useTargetDisplayNameResolver(): (targetAgentName: string | undefined | null) => string {
    const agentOptions = useAgentOptions();
    const { data: agentCards = [] } = useAgentCards();

    return useCallback(
        (targetAgentName: string | undefined | null) => {
            if (!targetAgentName) return "";
            const agentDisplay = findAgentOption(agentOptions, targetAgentName)?.displayName;
            if (agentDisplay) return agentDisplay;
            const workflowCard = agentCards.find(c => c.isWorkflow && c.name === targetAgentName);
            return workflowCard?.displayName || workflowCard?.name || targetAgentName;
        },
        [agentOptions, agentCards]
    );
}
