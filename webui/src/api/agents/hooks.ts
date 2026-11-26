// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useContext, useMemo } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { useConnectors } from "@/api/connectors";
import { useBuiltinToolsets, useCustomToolsets } from "@/api/toolsets";

import { CAPABILITIES, UserContext } from "@/providers/contexts/UserContext";

import type { Agent, AgentCardInfo } from "@/types";

import { invalidateAgentRelations } from "./invalidate";
import { agentKeys } from "./keys";
import type { CreateAgentRequest } from "./service";
import { transformAgent } from "./transform";
import { agentsApi } from "./versioned";

interface UseAgentsOptions {
    enabled?: boolean;
    refetchOnMount?: "always";
    refetchInterval?: number | false | ((agents: Agent[] | undefined) => number | false);
}

export function useAgents(options?: UseAgentsOptions) {
    const userCtx = useContext(UserContext);
    const capabilityEnabled = userCtx ? userCtx.userHasCapability(CAPABILITIES.readAgents) : false;
    const interval = options?.refetchInterval;
    return useQuery({
        queryKey: agentKeys.list(),
        queryFn: agentsApi.getAll,
        enabled: (options?.enabled ?? true) && capabilityEnabled,
        retry: 0,
        refetchOnMount: options?.refetchOnMount,
        refetchInterval: typeof interval === "function" ? query => interval(query.state.data) : interval,
    });
}

const EMPTY_CARDS: AgentCardInfo[] = [];

export function useUserAgentCards(options?: UseAgentsOptions) {
    const enabled = options?.enabled ?? true;
    const agentsQuery = useAgents({ enabled });
    const builtinQuery = useBuiltinToolsets({ enabled });
    const customQuery = useCustomToolsets({ enabled });
    const connectorsQuery = useConnectors();

    const data = useMemo<AgentCardInfo[]>(() => {
        if (!agentsQuery.data) return EMPTY_CARDS;
        return agentsQuery.data.map(agent => transformAgent(agent, builtinQuery.data ?? [], connectorsQuery.data ?? [], customQuery.data ?? []));
    }, [agentsQuery.data, builtinQuery.data, customQuery.data, connectorsQuery.data]);

    return {
        data,
        isLoading: agentsQuery.isLoading || builtinQuery.isLoading || customQuery.isLoading || connectorsQuery.isLoading,
        error: agentsQuery.error ?? builtinQuery.error ?? customQuery.error ?? connectorsQuery.error,
        refetch: agentsQuery.refetch,
    };
}

export function useCreateAgentMutation() {
    const queryClient = useQueryClient();
    return useMutation<{ id?: string } | null, Error, CreateAgentRequest>({
        mutationFn: agentsApi.create,
        onSuccess: () => invalidateAgentRelations(queryClient),
    });
}
