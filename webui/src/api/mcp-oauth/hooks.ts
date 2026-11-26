// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMutation } from "@tanstack/react-query";

import type { MCPOAuthDiscoverRequest, MCPOAuthInitiateRequest } from "@/types";

import { mcpOAuthApi } from "./versioned";

export function useInitiateMCPOAuth() {
    return useMutation({
        mutationFn: (data: MCPOAuthInitiateRequest) => mcpOAuthApi.initiate(data),
    });
}

export function useDiscoverMCPTools() {
    return useMutation({
        mutationFn: (data: MCPOAuthDiscoverRequest) => mcpOAuthApi.discover(data),
    });
}

export function useDiscoverMCPToolsWithAuth() {
    return useMutation({
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        mutationFn: (vars: { serverUrl: string; connectionType: string; auth?: any; customHeaders?: Record<string, string> | null }) => mcpOAuthApi.discoverWithAuth(vars.serverUrl, vars.connectionType, vars.auth, vars.customHeaders),
    });
}
