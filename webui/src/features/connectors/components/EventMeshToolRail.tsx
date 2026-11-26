// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { AlertTriangle, ArrowDown, ArrowUp, Copy, MoreHorizontal, PanelLeftOpen, Trash2 } from "lucide-react";

import { SearchInput } from "@/ui/search-input";

import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/ui";

import type { EventMeshOperation } from "../eventMeshUtils";

import { CollapsePanelIcon } from "./CollapsePanelIcon";

interface EventMeshToolRailProps {
    operations: EventMeshOperation[];
    activeIndex: number;
    search: string;
    onSearchChange: (value: string) => void;
    collapsed: boolean;
    onCollapsedChange: (collapsed: boolean) => void;
    onSelect: (index: number) => void;
    onAdd: () => void;
    onDuplicate: (index: number) => void;
    onMove: (index: number, direction: -1 | 1) => void;
    onRequestDelete: (index: number) => void;
    invalidUids: Set<string>;
}

export function EventMeshToolRail({ operations, activeIndex, search, onSearchChange, collapsed, onCollapsedChange, onSelect, onAdd, onDuplicate, onMove, onRequestDelete, invalidUids }: Readonly<EventMeshToolRailProps>) {
    const [openMenuUid, setOpenMenuUid] = useState<string | null>(null);
    if (collapsed) {
        return (
            <div className="flex h-full w-12 shrink-0 flex-col items-center border-r bg-(--background-w10) py-3">
                <Button type="button" size="sm" variant="ghost" onClick={() => onCollapsedChange(false)} aria-label="Expand tools panel">
                    <PanelLeftOpen className="size-4" />
                </Button>
            </div>
        );
    }

    const query = search.trim().toLowerCase();
    const rows = operations.map((operation, index) => ({ operation, index })).filter(({ operation }) => !query || operation.tool_name.toLowerCase().includes(query) || operation.topic.toLowerCase().includes(query));

    return (
        <aside className="flex h-full w-[300px] shrink-0 flex-col border-r bg-(--background-w10)">
            <div className="flex h-10 items-center justify-between border-b">
                <Button type="button" size="sm" variant="ghost" onClick={onAdd} className="ml-2 text-sm leading-5 text-(--primary-wMain)">
                    Create New Tool
                </Button>
                <Button type="button" size="sm" variant="ghost" className="h-full w-10 rounded-none text-(--secondary-text-wMain)" onClick={() => onCollapsedChange(true)} aria-label="Collapse tools panel">
                    <CollapsePanelIcon className="size-[18px]" />
                </Button>
            </div>
            <div className="border-b px-2 py-2">
                <SearchInput value={search} onChange={onSearchChange} placeholder="Search" className="max-w-none" />
            </div>
            <ul className="min-h-0 flex-1 divide-y overflow-y-auto overscroll-contain">
                {rows.length === 0 && <li className="px-3 py-4 text-center text-xs leading-4 text-(--secondary-text-wMain)">No tools match your search.</li>}
                {rows.map(({ operation, index }) => {
                    const isActive = index === activeIndex;
                    const displayName = operation.tool_name || `Untitled tool ${index + 1}`;
                    const hasIssues = operation._uid ? invalidUids.has(operation._uid) : false;
                    const menuKey = operation._uid ?? `row-${index}`;
                    return (
                        <li key={operation._uid ?? index} className={`group relative ${isActive ? "bg-(--secondary-w10)" : "bg-(--background-w10)"}`}>
                            <button type="button" onClick={() => onSelect(index)} className="flex min-h-[72px] w-full items-start gap-2 px-5 py-3 pr-10 text-left transition-colors hover:bg-(--primary-w10)" aria-current={isActive}>
                                <span className="min-w-0 flex-1">
                                    <span className="flex items-center gap-1.5">
                                        <span className="truncate text-sm leading-normal font-medium text-(--primary-wMain)">{displayName}</span>
                                        {hasIssues && <AlertTriangle className="size-3.5 shrink-0 text-(--warning-wMain)" aria-label="This tool has validation errors" />}
                                    </span>
                                    <span className="mt-1 block text-sm leading-normal font-normal break-all text-(--secondary-text-wMain)">{operation.topic || "Insert topic address"}</span>
                                </span>
                            </button>
                            <div className="absolute top-3 right-2">
                                <DropdownMenu open={openMenuUid === menuKey} onOpenChange={open => setOpenMenuUid(open ? menuKey : null)}>
                                    <DropdownMenuTrigger asChild>
                                        <Button type="button" size="sm" variant="ghost" className="size-7 p-0 text-(--secondary-text-wMain)" aria-label={`Actions for ${displayName}`}>
                                            <MoreHorizontal className="size-4" strokeWidth={2.5} />
                                        </Button>
                                    </DropdownMenuTrigger>
                                    <DropdownMenuContent align="end">
                                        <DropdownMenuItem onClick={() => onDuplicate(index)}>
                                            <Copy className="mr-2 size-4" />
                                            Duplicate
                                        </DropdownMenuItem>
                                        <DropdownMenuItem disabled={index === 0} onClick={() => onMove(index, -1)}>
                                            <ArrowUp className="mr-2 size-4" />
                                            Move up
                                        </DropdownMenuItem>
                                        <DropdownMenuItem disabled={index === operations.length - 1} onClick={() => onMove(index, 1)}>
                                            <ArrowDown className="mr-2 size-4" />
                                            Move down
                                        </DropdownMenuItem>
                                        <DropdownMenuItem disabled={operations.length === 1} onClick={() => onRequestDelete(index)}>
                                            <Trash2 className="mr-2 size-4" />
                                            Delete
                                        </DropdownMenuItem>
                                    </DropdownMenuContent>
                                </DropdownMenu>
                            </div>
                        </li>
                    );
                })}
            </ul>
        </aside>
    );
}
