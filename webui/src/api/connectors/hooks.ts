// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useContext } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { CAPABILITIES, UserContext } from "@/providers/contexts/UserContext";

import { connectorKeys } from "./keys";
import type { CreateConnectorRequest } from "./service";
import { connectorsApi } from "./versioned";

function useReadConnectorsEnabled(): boolean {
    const userCtx = useContext(UserContext);
    return userCtx ? userCtx.userHasCapability(CAPABILITIES.readConnectors) : false;
}

export function useCanCompareConnectorSecrets(): boolean {
    const userCtx = useContext(UserContext);
    const canRead = useReadConnectorsEnabled();
    return canRead && !!userCtx?.userHasCapability(CAPABILITIES.updateConnectors);
}

export function useConnectors() {
    const enabled = useReadConnectorsEnabled();
    return useQuery({
        queryKey: connectorKeys.list(),
        queryFn: () => connectorsApi.getAll(),
        retry: 0,
        enabled,
    });
}

export function useConnectorDetail(id: string | undefined) {
    const enabled = useReadConnectorsEnabled() && !!id;
    return useQuery({
        queryKey: connectorKeys.detail(id ?? ""),
        queryFn: () => connectorsApi.get(id as string),
        retry: 0,
        refetchOnMount: "always",
        enabled,
    });
}

export function useCreateConnectorMutation() {
    const queryClient = useQueryClient();
    return useMutation<{ id?: string } | null, Error, CreateConnectorRequest>({
        mutationFn: payload => connectorsApi.create(payload),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: connectorKeys.lists() });
        },
    });
}
