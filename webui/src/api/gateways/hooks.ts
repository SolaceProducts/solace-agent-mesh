// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { skipToken, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import type { CreateGatewayRequest, GatewayConnectionTestRequest } from "@/types";

import { gatewayKeys } from "./keys";
import type { GatewayDeploymentRequest } from "./service";
import { gatewaysApi } from "./versioned";

export function useGateways(options?: { enabled?: boolean }) {
    return useQuery({
        queryKey: gatewayKeys.list(),
        queryFn: gatewaysApi.getAll,
        enabled: options?.enabled ?? true,
    });
}

export function useGatewayNetworkingDetails(gatewayId: string | undefined) {
    return useQuery({
        queryKey: gatewayId ? gatewayKeys.networkingDetails(gatewayId) : ["gateways", "networkingDetails", "skip"],
        queryFn: gatewayId ? () => gatewaysApi.getNetworkingDetails(gatewayId) : skipToken,
    });
}

export function useTestGatewayConnection() {
    return useMutation({
        mutationFn: (request: GatewayConnectionTestRequest) => gatewaysApi.testConnection(request),
    });
}

export function useCreateGatewayMutation() {
    const queryClient = useQueryClient();
    return useMutation<{ id?: string } | null, Error, CreateGatewayRequest>({
        mutationFn: gatewaysApi.create,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: gatewayKeys.lists() });
        },
    });
}

export function useDeleteGatewayMutation() {
    const queryClient = useQueryClient();
    return useMutation<void, Error, string>({
        mutationFn: gatewaysApi.delete,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: gatewayKeys.lists() });
        },
    });
}

export function useGatewayDeploymentMutation() {
    const queryClient = useQueryClient();
    return useMutation<{ id?: string } | null, Error, GatewayDeploymentRequest>({
        mutationFn: gatewaysApi.deploy,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: gatewayKeys.lists() });
        },
    });
}
