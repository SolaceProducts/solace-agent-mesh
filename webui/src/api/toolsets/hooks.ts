// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useContext } from "react";
import { useQuery } from "@tanstack/react-query";

import { CAPABILITIES, UserContext } from "@/providers/contexts/UserContext";

import type { Toolset } from "@/types";

import { toolsetKeys } from "./keys";
import { toolsetsApi } from "./versioned";

interface UseToolsetsOptions {
    enabled?: boolean;
}

const TOOLSET_PROCESSING_STATUSES = new Set<Toolset["discoveryStatus"]>(["created", "pending"]);
const PROCESSING_REFETCH_MS = 5_000;

export function isToolsetProcessing(status?: Toolset["discoveryStatus"]): boolean {
    return !!status && TOOLSET_PROCESSING_STATUSES.has(status);
}

export function useBuiltinToolsets(options?: UseToolsetsOptions) {
    const userCtx = useContext(UserContext);
    const capabilityEnabled = userCtx ? userCtx.userHasCapability(CAPABILITIES.readToolsets) : false;
    return useQuery({
        queryKey: toolsetKeys.builtin(),
        queryFn: toolsetsApi.getBuiltin,
        enabled: (options?.enabled ?? true) && capabilityEnabled,
        retry: 0,
    });
}

export function useCustomToolsets(options?: UseToolsetsOptions) {
    const userCtx = useContext(UserContext);
    const capabilityEnabled = userCtx ? userCtx.userHasCapability(CAPABILITIES.readToolsets) : false;
    return useQuery({
        queryKey: toolsetKeys.custom(),
        queryFn: toolsetsApi.getCustom,
        enabled: (options?.enabled ?? true) && capabilityEnabled,
        refetchOnMount: "always",
        refetchInterval: query => (query.state.data?.some(toolset => isToolsetProcessing(toolset.discoveryStatus)) ? PROCESSING_REFETCH_MS : false),
        retry: 0,
    });
}

export function useToolsetBuildTarget() {
    return useQuery({
        queryKey: toolsetKeys.buildTarget(),
        queryFn: toolsetsApi.getBuildTarget,
        staleTime: 5 * 60 * 1000,
        retry: 0,
    });
}
