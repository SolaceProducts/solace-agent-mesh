// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useRef, useState } from "react";
import { useFormContext, useWatch } from "react-hook-form";

import type { MCPTool, Tool } from "@/types";

function convertMCPToolToTool(mcpTool: MCPTool): Tool {
    return {
        operationId: mcpTool.name,
        name: mcpTool.name,
        description: mcpTool.description || "",
        method: "MCP",
        endpoint: "",
    };
}

function intersectSelections(prev: Set<string>, toolIds: Set<string>): Set<string> {
    return new Set([...prev].filter(id => toolIds.has(id)));
}

function parseAllowList(allowList: unknown, toolIds: Set<string>): Set<string> | null {
    if (!allowList || typeof allowList !== "string" || allowList.length === 0) {
        return null;
    }
    const savedSelections = allowList
        .split(",")
        .map(id => id.trim())
        .filter(id => id.length > 0);
    const validSelections = savedSelections.filter(id => toolIds.has(id));
    if (validSelections.length === 0) {
        return null;
    }
    return new Set(validSelections);
}

interface UseToolSelectionProps {
    fieldPath: string;
}

export const useToolSelection = ({ fieldPath }: UseToolSelectionProps) => {
    const { control, getValues, setValue } = useFormContext();
    const [filter, setFilter] = useState<string>("");
    const [filteredTools, setFilteredTools] = useState<Tool[]>([]);
    const [selectedToolIds, setSelectedToolIds] = useState<Set<string>>(new Set());
    const [isInitialized, setIsInitialized] = useState(false);
    const previousValueRef = useRef<string>("");

    const apiTools: Tool[] | undefined = useWatch({ control, name: "tools" });
    const mcpTools: MCPTool[] | undefined = useWatch({ control, name: "mcpTools" });

    const tools: Tool[] | undefined = useMemo(() => {
        if (apiTools) return apiTools;
        if (mcpTools) return mcpTools.map(convertMCPToolToTool);
        return undefined;
    }, [apiTools, mcpTools]);

    useEffect(() => {
        if (!tools || tools.length === 0) return;

        const toolIds = new Set(tools.map(t => t.operationId));

        if (isInitialized) {
            setSelectedToolIds(prev => intersectSelections(prev, toolIds));
        } else {
            const restored = parseAllowList(getValues(fieldPath), toolIds);
            const selections = restored ?? new Set(tools.map(t => t.operationId));
            setSelectedToolIds(selections);
            setIsInitialized(true);
        }
    }, [tools, isInitialized, getValues, fieldPath]);

    useEffect(() => {
        if (!tools) return;
        if (filter.trim() === "") {
            setFilteredTools(tools);
        } else {
            const lowerFilter = filter.toLowerCase();
            setFilteredTools(
                tools.filter(tool => tool.name.toLowerCase().includes(lowerFilter) || tool.method?.toLowerCase().includes(lowerFilter) || tool.endpoint?.toLowerCase().includes(lowerFilter) || tool.operationId.toLowerCase().includes(lowerFilter))
            );
        }
    }, [filter, tools]);

    useEffect(() => {
        if (!tools) return;
        const selectedTools = tools.filter(tool => selectedToolIds.has(tool.operationId)).map(tool => tool.operationId);
        const newValue = selectedTools.join(",");

        if (newValue !== previousValueRef.current) {
            previousValueRef.current = newValue;
            setValue(fieldPath, newValue);
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [selectedToolIds, fieldPath, tools]);

    const toggleTool = (operationId: string) => {
        const newSelectedToolIds = new Set(selectedToolIds);
        if (newSelectedToolIds.has(operationId)) {
            newSelectedToolIds.delete(operationId);
        } else {
            newSelectedToolIds.add(operationId);
        }
        setSelectedToolIds(newSelectedToolIds);
    };

    const selectAll = () => {
        setSelectedToolIds(prev => new Set([...prev, ...filteredTools.map(tool => tool.operationId)]));
    };

    const deselectAll = () => {
        setSelectedToolIds(prev => {
            const filteredIds = new Set(filteredTools.map(tool => tool.operationId));
            const newSet = new Set(prev);
            for (const id of filteredIds) {
                newSet.delete(id);
            }
            return newSet;
        });
    };

    return {
        tools,
        filteredTools,
        filter,
        setFilter,
        selectedToolIds,
        toggleTool,
        selectAll,
        deselectAll,
    };
};
