// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { SearchInput } from "@/ui/search-input";

import { useToolSelection } from "../hooks/useToolSelection";

import { SelectAllCheckbox } from "./SelectAllCheckbox";
import { ToolCard } from "./ToolCard";

export interface ToolsSelectionProps {
    fieldPath: string;
}

function ToolsSelection({ fieldPath }: ToolsSelectionProps) {
    const { tools, filteredTools, filter, setFilter, selectedToolIds, toggleTool, selectAll, deselectAll } = useToolSelection({ fieldPath });

    const isMCP = tools && tools.length > 0 && tools.every(tool => tool.method === "MCP");
    const filterPlaceholder = isMCP ? "Filter by tool name" : "Filter by Name, Method, or Endpoint";

    const selectedFilteredCount = useMemo(() => filteredTools.filter(tool => selectedToolIds.has(tool.operationId)).length, [filteredTools, selectedToolIds]);

    return (
        <div className="mt-10">
            <div className="flex items-center justify-between font-semibold">
                <span>Tools</span>
                {tools && tools.length > 0 && (
                    <span className="text-sm font-normal text-(--secondary-text-wMain)">
                        {selectedToolIds.size} out of {tools.length} enabled
                    </span>
                )}
            </div>
            {tools && tools.length > 0 ? (
                <div className="mt-4 flex flex-col gap-4">
                    <SearchInput className="w-full" placeholder={filterPlaceholder} value={filter} onChange={setFilter} />
                    <SelectAllCheckbox totalCount={filteredTools.length} selectedCount={selectedFilteredCount} onSelectAll={selectAll} onDeselectAll={deselectAll} />
                    <div className="flex flex-col gap-[11px]">
                        {filteredTools.map(tool => (
                            <ToolCard key={tool.operationId} tool={tool} selectable={true} isSelected={selectedToolIds.has(tool.operationId)} onToggle={() => toggleTool(tool.operationId)} />
                        ))}
                    </div>
                </div>
            ) : (
                <div>No tools available for the uploaded specification.</div>
            )}
        </div>
    );
}

export { ToolsSelection };
