// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

const TOOLSET_SEPARATOR = "__";

export interface SplitToolName {
    toolset?: string;
    tool: string;
}

export function nonEmptyString(value: unknown): string | undefined {
    return typeof value === "string" && value ? value : undefined;
}

export function splitToolName(name: string): SplitToolName {
    const separatorIndex = name.indexOf(TOOLSET_SEPARATOR);
    if (separatorIndex <= 0) {
        return { tool: name };
    }

    const toolset = name.slice(0, separatorIndex);
    const tool = name.slice(separatorIndex + TOOLSET_SEPARATOR.length);
    if (!tool) {
        return { tool: name };
    }

    return { toolset, tool };
}

export function toolNodeLabel({ connectorName, toolName }: { connectorName?: string; toolName?: string }, fallback: string): string {
    const validToolName = nonEmptyString(toolName);
    const tool = validToolName ? splitToolName(validToolName).tool : undefined;
    if (connectorName && tool) {
        return `${connectorName} / ${tool}`;
    }
    return connectorName || tool || fallback;
}
