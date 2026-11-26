// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface MCPOAuthInitiateRequest {
    serverUrl: string;
    connectionType: string;
    mode: "discovery" | "manual";
    authorizationUrl?: string;
    tokenUrl?: string;
    clientId?: string;
    clientSecret?: string;
    scopes?: string;
    tokenEndpointAuthMethod?: string;
}

export interface MCPOAuthInitiateResponse {
    authorizationUrl: string;
    state: string;
    sessionId: string;
}

export interface MCPOAuthDiscoverRequest {
    sessionId: string;
    serverUrl: string;
    connectionType: string;
    customHeaders?: Record<string, string>;
}

export interface MCPToolAnnotations {
    readOnlyHint?: boolean | null;
    destructiveHint?: boolean | null;
    openWorldHint?: boolean | null;
}

export interface MCPTool {
    name: string;
    description: string;
    inputSchema?: Record<string, unknown>;
    annotations?: MCPToolAnnotations;
}

export interface MCPDiscoveryResponse {
    tools: MCPTool[];
}

export interface MCPOAuthSuccessMessage {
    type: "mcp-oauth-callback";
    sessionId: string;
    state: string;
}

export interface MCPOAuthErrorMessage {
    type: "mcp-oauth-error";
    error: string;
    state: string | null;
}

export type MCPOAuthMessage = MCPOAuthSuccessMessage | MCPOAuthErrorMessage;
