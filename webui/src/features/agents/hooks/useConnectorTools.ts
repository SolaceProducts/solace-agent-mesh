// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useState } from "react";
import jsyaml from "js-yaml";

import { connectorsApi } from "@/api/connectors/versioned";

import { extractTools } from "@/features/connectors/utils/openAPIUtils";
import { getResource } from "@/features/connectors/utils/resourcesCache";

import type { Connector, Tool } from "@/types";

interface UseConnectorToolsParams {
    connector: Connector;
    enabled: boolean;
    runtimeToolNames?: string[];
}

interface UseConnectorToolsReturn {
    tools: Tool[];
    totalCount: number;
    isLoading: boolean;
    error: string | null;
}

type ToolsResult = { tools: Tool[]; totalCount: number } | { error: string };

const splitCsv = (value: unknown): string[] => (typeof value === "string" ? value.split(",").map(s => s.trim()) : []);

function mcpToolsFromValues(values: Connector["values"]): ToolsResult | null {
    if (!values?.manifest) return null;
    const manifest = typeof values.manifest === "string" ? JSON.parse(values.manifest) : values.manifest;
    const selectedToolNames = splitCsv(values?.selected_tools).filter(Boolean);
    const enabled = selectedToolNames.length ? manifest.filter((tool: any) => selectedToolNames.includes(tool.name)) : manifest;
    const tools = enabled.map((tool: any) => ({ operationId: tool.name, name: tool.name, description: tool.description || "", method: "MCP", endpoint: "", inputSchema: tool.inputSchema })) as Tool[];
    return { tools, totalCount: manifest.length };
}

function eventMeshToolsFromValues(values: Connector["values"]): ToolsResult {
    const operations = Array.isArray(values?.operations) ? values.operations : [];
    const tools = operations.map((op: any) => ({ operationId: op.tool_name, name: op.tool_name, description: op.tool_description || "", method: "EVENT_MESH", endpoint: op.topic || "", inputSchema: op.input_schema })) as Tool[];
    return { tools, totalCount: tools.length };
}

function runtimeToolsResult(runtimeToolNames: string[] | undefined, method: string): ToolsResult | null {
    if (!runtimeToolNames?.length) return null;
    const tools = runtimeToolNames.map(name => ({ operationId: name, name, description: "", method, endpoint: "" })) as Tool[];
    return { tools, totalCount: tools.length };
}

function allowListToolsResult(values: Connector["values"]): ToolsResult | null {
    const operationIds = splitCsv(values?.allow_list);
    if (!operationIds.length) return null;
    const tools = operationIds.map(id => ({ operationId: id, name: id, description: "", method: "OPENAPI", endpoint: "" })) as Tool[];
    return { tools, totalCount: tools.length };
}

async function apiToolsFromValues(values: Connector["values"], connectorId: string, resourcePath: (id: string) => string, runtimeToolNames?: string[]): Promise<ToolsResult | null> {
    if (!connectorId) return null;
    if (!values?.specification_url) return runtimeToolsResult(runtimeToolNames, "OPENAPI") ?? allowListToolsResult(values);
    const resp = await getResource(resourcePath(connectorId));
    if (resp.status === 200) {
        const contentType = resp.contentType ?? "application/json";
        const spec = contentType.includes("json") ? JSON.parse(resp.text) : jsyaml.load(resp.text);
        const allTools = extractTools(spec);
        const allowedToolIds = splitCsv(values?.allow_list);
        return { tools: allTools.filter(tool => allowedToolIds.includes(tool.operationId)), totalCount: allTools.length };
    }
    if (resp.status === 404) {
        return runtimeToolsResult(runtimeToolNames, "OPENAPI") ?? allowListToolsResult(values) ?? { error: `Failed to fetch OpenAPI specification (status ${resp.status})` };
    }
    const runtimeResult = runtimeToolsResult(runtimeToolNames, "OPENAPI");
    if (runtimeResult) {
        console.warn(`Connector ${connectorId} spec fetch returned status ${resp.status}; falling back to runtime tool names`);
        return runtimeResult;
    }
    return { error: `Failed to fetch OpenAPI specification (status ${resp.status})` };
}

function connectorHasTools(type: string, values: Connector["values"], runtimeToolNames?: string[]): boolean {
    if (type === "mcp") return Boolean(values?.manifest) || Boolean(values?.selected_tools) || Boolean(runtimeToolNames?.length);
    if (type === "api") return Boolean(values?.allow_list) || Boolean(runtimeToolNames?.length);
    if (type === "event_mesh") return Array.isArray(values?.operations) && values.operations.length > 0;
    return false;
}

function buildToolsForType(type: string, values: Connector["values"], connectorId: string, resourcePath: (id: string) => string, runtimeToolNames?: string[]): ToolsResult | null | Promise<ToolsResult | null> {
    if (type === "mcp") return mcpToolsFromValues(values) ?? runtimeToolsResult(runtimeToolNames, "MCP");
    if (type === "event_mesh") return eventMeshToolsFromValues(values);
    if (type === "api") return apiToolsFromValues(values, connectorId, resourcePath, runtimeToolNames);
    return null;
}

export const useConnectorTools = ({ connector, enabled, runtimeToolNames }: UseConnectorToolsParams): UseConnectorToolsReturn => {
    const [tools, setTools] = useState<Tool[]>([]);
    const [totalCount, setTotalCount] = useState(0);
    const [isLoading, setIsLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);

    const { type, values, id: connectorId } = connector;

    const runtimeToolNamesKey = runtimeToolNames?.join(",") ?? "";

    const toolsConfigKey = useMemo(
        () =>
            JSON.stringify({
                manifest: values?.manifest,
                selected_tools: values?.selected_tools,
                specification_url: values?.specification_url,
                allow_list: values?.allow_list,
                operations: values?.operations,
                runtimeToolNames: runtimeToolNamesKey,
            }),
        [values?.manifest, values?.selected_tools, values?.specification_url, values?.allow_list, values?.operations, runtimeToolNamesKey]
    );

    useEffect(() => {
        let cancelled = false;

        const loadTools = async () => {
            if (!enabled || !connectorHasTools(type, values, runtimeToolNames)) {
                if (!cancelled) {
                    setTools([]);
                    setTotalCount(0);
                }
                return;
            }

            if (!cancelled) {
                setIsLoading(true);
                setError(null);
            }

            try {
                const result = await buildToolsForType(type, values, connectorId, connectorsApi.resourcePath, runtimeToolNames);

                if (cancelled || !result) return;
                if ("error" in result) {
                    setError(result.error);
                    return;
                }
                setTools(result.tools);
                setTotalCount(result.totalCount);
            } catch (err) {
                console.error("Failed to load connector tools:", err);
                if (!cancelled) {
                    setError(err instanceof Error ? err.message : "Failed to load tools");
                }
            } finally {
                if (!cancelled) {
                    setIsLoading(false);
                }
            }
        };

        loadTools();

        return () => {
            cancelled = true;
        };
    }, [enabled, type, connectorId, toolsConfigKey]);

    return useMemo(
        () => ({
            tools,
            totalCount,
            isLoading,
            error,
        }),
        [tools, totalCount, isLoading, error]
    );
};
