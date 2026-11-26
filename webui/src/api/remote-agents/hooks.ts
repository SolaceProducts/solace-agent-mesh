// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useContext } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { CAPABILITIES, UserContext } from "@/providers/contexts/UserContext";

import { remoteAgentKeys } from "./keys";
import { transformRemoteAgent } from "./transform";
import type { CreateRemoteAgentRequest, RemoteAgentResponse } from "./types";
import { remoteAgentsApi } from "./versioned";

function useReadAgentsEnabled(): boolean {
    const userCtx = useContext(UserContext);
    return userCtx ? userCtx.userHasCapability(CAPABILITIES.readAgents) : false;
}

export function useCreateRemoteAgentMutation() {
    const queryClient = useQueryClient();
    return useMutation<RemoteAgentResponse, Error, CreateRemoteAgentRequest>({
        mutationFn: remoteAgentsApi.create,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: remoteAgentKeys.lists() });
        },
    });
}

export function useRemoteAgents() {
    const enabled = useReadAgentsEnabled();
    return useQuery({
        queryKey: remoteAgentKeys.list(),
        queryFn: remoteAgentsApi.getAll,
        retry: 0,
        enabled,
    });
}

export function useRemoteAgentCards() {
    const enabled = useReadAgentsEnabled();
    return useQuery({
        queryKey: remoteAgentKeys.list(),
        queryFn: remoteAgentsApi.getAll,
        retry: 0,
        enabled,
        select: data => data.map(transformRemoteAgent),
    });
}
