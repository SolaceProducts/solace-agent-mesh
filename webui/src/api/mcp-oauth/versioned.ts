// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { MCPDiscoveryResponse, MCPOAuthDiscoverRequest, MCPOAuthInitiateRequest, MCPOAuthInitiateResponse } from "@/types";

import { discoverMCPToolsV2, discoverMCPToolsWithAuthV2, getMCPOAuthStatusV2, initiateMCPOAuthV2, type MCPAuthConfig } from "./v2";

export interface McpOAuthApi {
    initiate: (data: MCPOAuthInitiateRequest) => Promise<MCPOAuthInitiateResponse>;
    getStatus: (sessionId: string) => Promise<{ status: "pending" | "completed" | "expired" }>;
    discover: (data: MCPOAuthDiscoverRequest) => Promise<MCPDiscoveryResponse>;
    discoverWithAuth: (serverUrl: string, connectionType: string, auth?: MCPAuthConfig, customHeaders?: Record<string, string> | null) => Promise<MCPDiscoveryResponse>;
}

export const mcpOAuthApi: McpOAuthApi = { initiate: initiateMCPOAuthV2, getStatus: getMCPOAuthStatusV2, discover: discoverMCPToolsV2, discoverWithAuth: discoverMCPToolsWithAuthV2 };
