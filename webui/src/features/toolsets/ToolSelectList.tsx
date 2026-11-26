// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import { CustomCheckbox } from "@/common/CustomCheckbox";

import { SearchInput } from "@/ui/search-input";

import { AgentToolsetConfigDialog } from "@/features/agents/AgentToolsetConfigDialog";
import { SelectAllCheckbox } from "@/features/connectors/components/SelectAllCheckbox";

import type { ConfigSchemaField, Toolset } from "@/types";

export interface ToolSelectItem {
    name: string;
    description?: string;
    configSchema?: ConfigSchemaField[] | null;
}

interface ToolSelectListProps {
    tools: ToolSelectItem[];
    checkedTools: Set<string>;
    onToggle: (toolName: string) => void;
    onSelectAll: () => void;
    onDeselectAll: () => void;
    discoveryStatus?: Toolset["discoveryStatus"];
    testId?: string;
    configValues?: Record<string, Record<string, unknown>>;
    onConfigSave?: (toolName: string, values: Record<string, unknown>) => void;
    selectAllId?: string;
}

export function ToolSelectList({ tools, checkedTools, onToggle, onSelectAll, onDeselectAll, discoveryStatus, testId, configValues, onConfigSave, selectAllId }: Readonly<ToolSelectListProps>) {
    const [search, setSearch] = useState("");

    if (discoveryStatus === "pending") {
        return <div className="text-sm">This toolset is pending discovery. Refresh the page once discovery has completed to select individual tools.</div>;
    }

    if (discoveryStatus === "failed") {
        return <div className="text-sm text-(--error-wMain)">Discovery failed. Check the toolset details for errors.</div>;
    }

    if (tools.length === 0) {
        return <div className="text-sm">No tools discovered.</div>;
    }

    const filtered = search ? tools.filter(t => t.name.toLowerCase().includes(search.toLowerCase())) : tools;

    return (
        <div data-testid={testId}>
            <div className="mb-3 text-sm font-medium">Choose the tools you want this agent to have access to when completing tasks.</div>

            <div className="mb-3 flex items-center gap-2">
                <SearchInput value={search} onChange={setSearch} placeholder="Filter by name" />
                <SelectAllCheckbox totalCount={tools.length} selectedCount={checkedTools.size} onSelectAll={onSelectAll} onDeselectAll={onDeselectAll} id={selectAllId} />
            </div>

            <div className="space-y-2">
                {filtered.map(tool => {
                    const hasConfig = tool.configSchema && tool.configSchema.length > 0;
                    const isChecked = checkedTools.has(tool.name);
                    return (
                        <div key={tool.name} data-testid={`tool-row-${tool.name}`} className="flex items-center gap-3">
                            <CustomCheckbox checked={isChecked} onChange={() => onToggle(tool.name)} ariaLabel={tool.name} />
                            <div className="flex-1">
                                <div className="text-sm font-medium">{tool.name}</div>
                                {tool.description && <div className="text-sm">{tool.description}</div>}
                            </div>
                            {hasConfig && isChecked && onConfigSave && <AgentToolsetConfigDialog toolName={tool.name} configSchema={tool.configSchema!} configValues={configValues?.[tool.name] || {}} onSave={onConfigSave} />}
                        </div>
                    );
                })}
                {filtered.length === 0 && <div className="py-4 text-center text-sm">No tools found</div>}
            </div>
        </div>
    );
}
