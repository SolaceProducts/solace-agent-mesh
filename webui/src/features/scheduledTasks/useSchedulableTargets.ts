// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { useAgentCards } from "@/api/agent-cards";

import type { AgentCardInfo } from "@/types";

export interface SchedulableTargets {
    targets: AgentCardInfo[];
    allTargets: AgentCardInfo[];
    isLoading: boolean;
    hasNoInvokeAccess: boolean;
    hasCardsError: boolean;
    refetch: () => void;
    isRefetching: boolean;
}

export function useSchedulableTargets(): SchedulableTargets {
    const { data, isLoading, isRefetching, isError, refetch } = useAgentCards();

    const allTargets = useMemo(() => data ?? [], [data]);
    const targets = useMemo(() => allTargets.filter(card => !card.isInternal), [allTargets]);

    return {
        targets,
        allTargets,
        isLoading,
        hasNoInvokeAccess: !isLoading && !isError && targets.length === 0,
        hasCardsError: isError,
        refetch,
        isRefetching,
    };
}
