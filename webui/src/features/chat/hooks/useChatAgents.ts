// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";

import { agentCardKeys, useAgentCards } from "@/api/agent-cards";
import { remoteAgentKeys, useRemoteAgentCards } from "@/api/remote-agents";

export interface ChatAgentsQuery {
    isLoading: boolean;
    isRefetching: boolean;
    isError: boolean;
    refetch: () => void;
}

export function useChatAgents(): ChatAgentsQuery {
    const queryClient = useQueryClient();
    const { isLoading: yamlAgentsLoading, isFetching: yamlAgentsFetching, isError: yamlAgentsError, error: yamlAgentsErrorObj } = useAgentCards();
    const { isLoading: remoteAgentsLoading, isFetching: remoteAgentsFetching, isError: remoteAgentsError, error: remoteAgentsErrorObj } = useRemoteAgentCards();

    useEffect(() => {
        if (yamlAgentsErrorObj) {
            console.error("Failed to load agent cards:", yamlAgentsErrorObj);
        }
    }, [yamlAgentsErrorObj]);
    useEffect(() => {
        if (remoteAgentsErrorObj) {
            console.error("Failed to load remote agents:", remoteAgentsErrorObj);
        }
    }, [remoteAgentsErrorObj]);

    const refetch = useCallback(() => {
        queryClient.invalidateQueries({ queryKey: agentCardKeys.lists() });
        queryClient.invalidateQueries({ queryKey: remoteAgentKeys.lists() });
    }, [queryClient]);

    return {
        isLoading: yamlAgentsLoading || remoteAgentsLoading,
        isRefetching: yamlAgentsFetching || remoteAgentsFetching,
        isError: yamlAgentsError || remoteAgentsError,
        refetch,
    };
}
