// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { mcpOAuthApi } from "@/api/mcp-oauth/versioned";

import { type Connector, ConnectorType } from "@/types";

import { normalizeToolSelection } from "../hooks/useConnectorWizard";

import { buildAuthConfigFromValues, parseKeyValuePairs } from "./authConfigBuilder";

export interface ValidationResult {
    success: boolean;
    error?: string;
    updatedConnector?: Connector;
    nextStep?: "config" | "tools" | "save";
}

export async function validateConnector(connector: Connector, isValidationStep: boolean | undefined): Promise<ValidationResult> {
    switch (connector.type) {
        case ConnectorType.Mcp:
            return await validateMCPConnector(connector, isValidationStep);

        case ConnectorType.Api:
            return await validateAPIConnector(connector, isValidationStep);

        default:
            return { success: true };
    }
}

async function validateMCPConnector(connector: Connector, isValidationStep: boolean | undefined): Promise<ValidationResult> {
    if (!isValidationStep) {
        return { success: true, nextStep: "save" };
    }

    try {
        const authConfig = buildAuthConfigFromValues(connector.values);

        const customHeaders = parseKeyValuePairs(connector.values.custom_headers);
        const responseData = await mcpOAuthApi.discoverWithAuth(connector.values.server_url as string, connector.values.connection_type as string, authConfig, customHeaders);

        const discoveredTools = responseData.tools || [];
        const updatedConnector = {
            ...connector,
            mcpTools: discoveredTools,
            values: {
                ...connector.values,
                manifest: JSON.stringify(discoveredTools),
            },
        };

        return {
            success: true,
            updatedConnector,
            nextStep: "tools",
        };
    } catch (error: any) {
        return { success: false, error: translateMCPErrorMessage(error?.message) };
    }
}

function isPlatformDiscoveryMessage(message: string): boolean {
    return message.includes("MCP server");
}

export function translateMCPErrorMessage(message: string | undefined): string {
    if (!message) {
        return "Unable to connect to MCP server.";
    }

    if (isPlatformDiscoveryMessage(message)) {
        return message;
    }
    if (message.includes("TaskGroup") || message.includes("401") || message.includes("403") || message.includes("Unauthorized")) {
        return "Failed to authenticate with the MCP server.";
    }
    if (message.includes("protocol") || message.includes("Invalid URL")) {
        return "Invalid server URL. Please ensure the URL starts with https:// and is correctly formatted.";
    }
    if (message.includes("timeout") || message.includes("ECONNREFUSED") || message.includes("ETIMEDOUT")) {
        return "Unable to reach the MCP server. Please check if the server is running and accessible.";
    }
    if (!message.includes("Failed to") && !message.includes("Unable to")) {
        return "Failed to connect to MCP server. Please verify the server URL and authentication settings.";
    }
    return message;
}

async function validateAPIConnector(connector: Connector, _isValidationStep: boolean | undefined): Promise<ValidationResult> {
    if (connector.subtype !== "openapi") {
        return { success: true };
    }

    const openAPISpecFile = connector.resources as FileList | null | undefined;
    if (!openAPISpecFile || openAPISpecFile.length === 0) {
        return { success: false, error: "An OpenAPI specification file is required" };
    }

    const allowList = connector.values.allow_list;
    const allowListArray = normalizeToolSelection(allowList);

    if (allowListArray.length === 0) {
        return { success: false, error: "At least one tool must be selected" };
    }

    return { success: true };
}
