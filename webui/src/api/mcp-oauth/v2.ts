// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { MCPDiscoveryResponse, MCPOAuthDiscoverRequest, MCPOAuthInitiateRequest, MCPOAuthInitiateResponse, MCPTool, MCPToolAnnotations } from "@/types";

import { fetchAllPages, pageQuery, type PaginatedResponse, platformV2, withoutNulls } from "../common";

const DISCOVER_PATH = "/connectors/mcp/discover";
const DISCOVER_WITH_TOKEN_PATH = "/connectors/mcp/oauth/discoverWithToken";
const MAX_PAGE_SIZE = 100;

export interface MCPAuthConfig {
    type: string;
    http?: { scheme?: string; token?: string; username?: string; password?: string };
    apikey?: { in?: string; name?: string; value?: string };
    oauth?: { accessToken?: string };
}

interface MCPToolV2 {
    name: string;
    description: string;
    inputSchema: Record<string, unknown> | null;
    outputSchema: Record<string, unknown> | null;
    annotations: MCPToolAnnotations | null;
}

const toV2ConnectionType = (connectionType: string) => (connectionType === "streamable-http" ? "streamable_http" : connectionType);

function toV2Auth(auth: MCPAuthConfig | undefined) {
    if (!auth) return undefined;
    const { apikey, ...rest } = auth;
    return { ...rest, ...(apikey && { apiKey: apikey }) };
}

const toMCPTool = (tool: MCPToolV2): MCPTool => withoutNulls(tool) as MCPTool;

const toDiscovery = (tools: MCPToolV2[]): MCPDiscoveryResponse => ({ tools: tools.map(toMCPTool) });

export async function initiateMCPOAuthV2(data: MCPOAuthInitiateRequest): Promise<MCPOAuthInitiateResponse> {
    return platformV2.put<MCPOAuthInitiateResponse>("/connectors/mcp/oauth/initiate", { ...data, connectionType: toV2ConnectionType(data.connectionType) });
}

export async function getMCPOAuthStatusV2(sessionId: string): Promise<{ status: "pending" | "completed" | "expired" }> {
    return platformV2.get(`/connectors/mcp/oauth/status?sessionId=${encodeURIComponent(sessionId)}`);
}

export async function discoverMCPToolsV2({ sessionId, serverUrl, connectionType }: MCPOAuthDiscoverRequest): Promise<MCPDiscoveryResponse> {
    const body = { sessionId, serverUrl, connectionType: toV2ConnectionType(connectionType) };
    const page: PaginatedResponse<MCPToolV2> = await platformV2.putList(`${DISCOVER_WITH_TOKEN_PATH}?${pageQuery({ pageNumber: 1, pageSize: MAX_PAGE_SIZE })}`, body);
    const total = page.meta.pagination.count;
    if (total > page.data.length) {
        throw new Error(`This MCP server publishes ${total} tools, more than the ${MAX_PAGE_SIZE} that can be listed after signing in.`);
    }
    return toDiscovery(page.data);
}

export async function discoverMCPToolsWithAuthV2(serverUrl: string, connectionType: string, auth?: MCPAuthConfig, customHeaders?: Record<string, string> | null): Promise<MCPDiscoveryResponse> {
    const body = { serverUrl, connectionType: toV2ConnectionType(connectionType), auth: toV2Auth(auth), ...(customHeaders ? { customHeaders } : {}) };
    const tools = await fetchAllPages(params => platformV2.putList<MCPToolV2>(`${DISCOVER_PATH}?${pageQuery(params)}`, body));
    return toDiscovery(tools);
}
