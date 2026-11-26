// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback } from "react";

import type { AgentCardInfo } from "@/types";

export interface DuplicateNameCheckResult {
    isDuplicate: boolean;
    conflictingAgent?: AgentCardInfo;
}

export const useDuplicateNameChecker = (agents: AgentCardInfo[], excludeDisplayName?: string): ((nameToCheck: string) => DuplicateNameCheckResult) => {
    const excludedNameLower = excludeDisplayName?.trim().toLowerCase();

    const checkForDuplicate = useCallback(
        (nameToCheck: string): DuplicateNameCheckResult => {
            const trimmedName = nameToCheck.trim();
            if (!trimmedName) {
                return { isDuplicate: false };
            }

            const nameLower = trimmedName.toLowerCase();

            if (excludedNameLower && nameLower === excludedNameLower) {
                return { isDuplicate: false };
            }

            for (const agent of agents) {
                if (agent.displayName && agent.displayName.toLowerCase() === nameLower) {
                    return { isDuplicate: true, conflictingAgent: agent };
                }
            }

            return { isDuplicate: false };
        },
        [agents, excludedNameLower]
    );

    return checkForDuplicate;
};
