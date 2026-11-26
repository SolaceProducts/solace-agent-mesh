// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Tool } from "@/types";

export const parseMCPTools = (mcpTools: any[], selectedToolNames: string | string[]): Tool[] => {
    if (!mcpTools || !selectedToolNames) {
        return [];
    }

    const toolNamesArray: string[] = typeof selectedToolNames === "string" ? selectedToolNames.split(",").map(s => s.trim()) : Array.isArray(selectedToolNames) ? selectedToolNames.map(s => String(s).trim()) : [];

    return mcpTools
        .filter((tool: any) => toolNamesArray.includes(tool.name))
        .map((tool: any) => ({
            operationId: tool.name,
            name: tool.name,
            description: tool.description || "",
            method: "MCP",
            endpoint: "",
            inputSchema: tool.inputSchema,
        }));
};

export const parseAPITools = (tools: Tool[], allowList: string | string[]): Tool[] => {
    if (!tools || !allowList) {
        return [];
    }

    const allowListArray: string[] = typeof allowList === "string" ? allowList.split(",").map(s => s.trim()) : Array.isArray(allowList) ? allowList.map(s => String(s).trim()) : [];

    return tools.filter((tool: Tool) => allowListArray.includes(tool.operationId));
};
