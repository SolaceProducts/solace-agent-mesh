// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useMemo, useRef, useState } from "react";
import { ArrowDown, ArrowUp, ChevronsUpDown, X } from "lucide-react";
import { useNavigate, useSearchParams } from "react-router-dom";

import { useBuiltinToolsets, useCustomToolsets } from "@/api/toolsets";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { deferPanelClose } from "@/hooks/useRadixPointerEventsFix";

import { PageSidePanelWrapper } from "@/common/layout";

import { Button } from "@/ui/button";
import { SearchInput } from "@/ui/search-input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";

import { EmptyState, FileDropZone, MessageBanner, MorePopoverButton, OnboardingBanner, PaginationControls } from "@/common";
import type { Toolset } from "@/types";
import { DOC_LINKS } from "@/utils";

import { ToolsetDiscoveryIndicator } from "./ToolsetDiscoveryIndicator";
import { ToolsetMorePopover } from "./ToolsetMorePopover";
import { ToolsetSidePanel } from "./ToolsetSidePanel";

type ToolsetsSortKey = "name" | "agentCount" | "createdBy";

const TOOLS_STORAGE_KEY = "sam-custom-tools-onboarding-dismissed";
const TOOLS_HEADER = "Custom tools go beyond the built-in catalog — built by members of an organization to address specific business requirements. ";
const TOOLS_DESCRIPTION =
    "Use a preferred programming language and framework, package the tool, and upload it directly to Agent Mesh. Custom tools can integrate with internal systems, implement specialized workflows, or add capabilities tailored to the organization's needs.";
const TOOLS_LEARN_MORE_TEXT = "Learn about Toolsets";
const TOOLS_URL = DOC_LINKS.buildingToolsets;

const TYPE_OPTIONS: { value: string; label: string }[] = [
    { value: "all", label: "All Types" },
    { value: "custom", label: "Custom" },
    { value: "builtin", label: "Builtin" },
];

const STATUS_OPTIONS: { value: string; label: string }[] = [
    { value: "all", label: "All Statuses" },
    { value: "pending", label: "Pending" },
    { value: "ready", label: "Ready" },
    { value: "failed", label: "Failed" },
];

export const ToolsetsView: React.FC = () => {
    const { handleViewCustomToolset, handleCreateCustomToolset, newlyCreatedToolsetId, setNewlyCreatedToolsetId } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const canCreateToolsets = userHasCapability(CAPABILITIES.createToolsets);
    const { data: builtinToolsets = [], isLoading: builtinLoading, error: builtinError } = useBuiltinToolsets();
    const { data: customToolsets = [], isLoading: customToolsetsLoading, error: customToolsetsError } = useCustomToolsets();

    const navigate = useNavigate();
    const [searchParams, setSearchParams] = useSearchParams();

    const typeParam = searchParams.get("type") ?? "all";
    const statusParam = searchParams.get("discoveryStatus") ?? "all";

    const [searchTerm, setSearchTerm] = useState("");
    const [currentPage, setCurrentPage] = useState(1);
    const [sortBy, setSortBy] = useState<ToolsetsSortKey>("name");
    const [sortDirection, setSortDirection] = useState<"asc" | "desc">("asc");
    const [screenHeight, setScreenHeight] = useState<number>(globalThis.window === undefined ? 768 : globalThis.window.innerHeight);
    const [selectedToolset, setSelectedToolset] = useState<Toolset | null>(null);
    const selectedToolsetIdRef = useRef<string | null>(null);
    const [isSidePanelOpen, setIsSidePanelOpen] = useState<boolean>(false);
    const [openAddToAgentOnMount, setOpenAddToAgentOnMount] = useState(false);

    const itemsPerPage = screenHeight >= 900 ? 20 : 10;

    const isLoading = builtinLoading || customToolsetsLoading;
    const error = builtinError ?? customToolsetsError;

    const allToolsets = useMemo(() => [...(builtinToolsets ?? []), ...(customToolsets ?? [])], [builtinToolsets, customToolsets]);

    useEffect(() => {
        const handleResize = () => {
            if (globalThis.window === undefined) return;
            setScreenHeight(globalThis.window.innerHeight);
        };
        window.addEventListener("resize", handleResize);
        return () => window.removeEventListener("resize", handleResize);
    }, []);

    useEffect(() => {
        setCurrentPage(1);
    }, [searchTerm, typeParam, statusParam]);

    useEffect(() => {
        const id = selectedToolsetIdRef.current;
        if (!id) return;
        const fresh = allToolsets.find(t => t.id === id) ?? null;
        if (!fresh) {
            deferPanelClose(() => setIsSidePanelOpen(false));
        }
        setSelectedToolset(fresh);
    }, [allToolsets]);

    useEffect(() => {
        if (!newlyCreatedToolsetId || !customToolsets) return;
        const toolset = customToolsets.find(t => t.id === newlyCreatedToolsetId);
        if (!toolset) return;
        setNewlyCreatedToolsetId(null);
        selectedToolsetIdRef.current = toolset.id;
        setSelectedToolset(toolset);
        setIsSidePanelOpen(true);
        setOpenAddToAgentOnMount(true);
    }, [customToolsets, newlyCreatedToolsetId, setNewlyCreatedToolsetId]);

    const handleColumnSort = (column: ToolsetsSortKey) => {
        if (sortBy === column) {
            setSortDirection(sortDirection === "asc" ? "desc" : "asc");
        } else {
            setSortBy(column);
            setSortDirection("asc");
        }
        setCurrentPage(1);
    };

    const setTypeFilter = (value: string) => {
        const next = new URLSearchParams(searchParams);
        if (value === "all") {
            next.delete("type");
        } else {
            next.set("type", value);
        }
        setSearchParams(next, { replace: true });
    };

    const setStatusFilter = (value: string) => {
        const next = new URLSearchParams(searchParams);
        if (value === "all") {
            next.delete("discoveryStatus");
        } else {
            next.set("discoveryStatus", value);
        }
        setSearchParams(next, { replace: true });
    };

    const hasActiveFilters = typeParam !== "all" || statusParam !== "all";

    const clearFilters = () => {
        const next = new URLSearchParams(searchParams);
        next.delete("type");
        next.delete("discoveryStatus");
        setSearchParams(next, { replace: true });
    };

    const filteredToolsets = useMemo(() => {
        let result = allToolsets;

        if (typeParam !== "all") {
            result = result.filter(t => t.type === typeParam);
        }

        if (statusParam !== "all") {
            result = result.filter(t => t.discoveryStatus === statusParam);
        }

        if (searchTerm.trim()) {
            const lower = searchTerm.toLowerCase();
            result = result.filter(t => t.name.toLowerCase().includes(lower));
        }

        result = result.slice().sort((a, b) => {
            let aVal: string | number = "";
            let bVal: string | number = "";

            if (sortBy === "name") {
                aVal = a.name;
                bVal = b.name;
            } else if (sortBy === "agentCount") {
                aVal = a.agents?.length ?? 0;
                bVal = b.agents?.length ?? 0;
            } else if (sortBy === "createdBy") {
                aVal = a.createdBy ?? "";
                bVal = b.createdBy ?? "";
            }

            if (typeof aVal === "string" && typeof bVal === "string") {
                return sortDirection === "asc" ? aVal.localeCompare(bVal) : bVal.localeCompare(aVal);
            }
            return sortDirection === "asc" ? (aVal as number) - (bVal as number) : (bVal as number) - (aVal as number);
        });

        return result;
    }, [allToolsets, typeParam, statusParam, searchTerm, sortBy, sortDirection]);

    const totalPages = Math.ceil(filteredToolsets.length / itemsPerPage);
    const effectiveCurrentPage = Math.min(currentPage, Math.max(totalPages, 1));
    const startIndex = (effectiveCurrentPage - 1) * itemsPerPage;
    const endIndex = startIndex + itemsPerPage;
    const currentToolsets = filteredToolsets.slice(startIndex, endIndex);

    const handlePageChange = (page: number) => {
        if (page >= 1 && page <= totalPages) {
            setCurrentPage(page);
        }
    };

    const getSortIcon = (column: ToolsetsSortKey) => {
        if (sortBy !== column) return <ChevronsUpDown className="ml-2 h-4 w-4 opacity-50" />;
        return sortDirection === "asc" ? <ArrowUp className="ml-2 h-4 w-4" /> : <ArrowDown className="ml-2 h-4 w-4" />;
    };

    const handleSelectToolset = (toolset: Toolset) => {
        selectedToolsetIdRef.current = toolset.id;
        setSelectedToolset(toolset);
        setIsSidePanelOpen(true);
    };

    const handleCloseSidePanel = () => {
        selectedToolsetIdRef.current = null;
        setSelectedToolset(null);
        setIsSidePanelOpen(false);
        setOpenAddToAgentOnMount(false);
    };

    if (isLoading) {
        return <EmptyState variant="loading" title="Loading Toolsets..." />;
    }

    if (error) {
        return (
            <div className="flex h-96 items-center justify-center">
                <MessageBanner variant="error" message={`Error loading toolsets. ${error.message}`} />
            </div>
        );
    }

    const showToolbar = allToolsets.length > 0 || searchTerm || hasActiveFilters;

    return (
        <FileDropZone overlayLabel="Drop toolset ZIP to upload" disabled={!canCreateToolsets} onZipDropped={zip => navigate("/toolsets/new/edit", { state: { droppedFile: zip } })}>
            <div className="flex min-w-0 flex-1 flex-col">
                <OnboardingBanner storageKey={TOOLS_STORAGE_KEY} header={TOOLS_HEADER} description={TOOLS_DESCRIPTION} learnMoreText={TOOLS_LEARN_MORE_TEXT} learnMoreUrl={TOOLS_URL} className="mx-6 mt-6" />

                {showToolbar && (
                    <div className="mx-6 mt-6 flex flex-wrap items-center gap-2">
                        <SearchInput value={searchTerm} onChange={setSearchTerm} placeholder="Filter by name" testid="toolsetsSearchInput" />
                        <Select value={typeParam} onValueChange={setTypeFilter}>
                            <SelectTrigger className="h-9 w-36" data-testid="toolsetsTypeFilter">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {TYPE_OPTIONS.map(opt => (
                                    <SelectItem key={opt.value} value={opt.value}>
                                        {opt.label}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        <Select value={typeParam === "builtin" ? "all" : statusParam} onValueChange={setStatusFilter} disabled={typeParam === "builtin"}>
                            <SelectTrigger className="h-9 w-40" data-testid="toolsetsStatusFilter">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {STATUS_OPTIONS.map(opt => (
                                    <SelectItem key={opt.value} value={opt.value}>
                                        {opt.label}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        {hasActiveFilters && (
                            <Button variant="ghost" size="sm" className="h-9 gap-1 px-2 text-sm" onClick={clearFilters} data-testid="toolsetsClearFilters">
                                <X className="h-3.5 w-3.5" />
                                Clear filters
                            </Button>
                        )}
                    </div>
                )}

                <div className="flex min-h-0 flex-1 flex-col overflow-hidden px-6 pb-6">
                    <div className="flex min-h-0 flex-1 flex-col pt-6">
                        {currentToolsets.length > 0 ? (
                            <div className="min-h-0 flex-1 overflow-y-auto rounded-xs border" style={{ scrollbarGutter: "stable" }}>
                                <Table containerClassName="overflow-x-visible">
                                    <TableHeader sticky>
                                        <TableRow>
                                            <TableHead className="cursor-pointer font-semibold hover:bg-(--secondary-w10)/30 sm:w-1/4" onClick={() => handleColumnSort("name")}>
                                                <div className="flex items-center">
                                                    <div className="pl-5">Name</div>
                                                    {getSortIcon("name")}
                                                </div>
                                            </TableHead>
                                            <TableHead className="hidden w-24 font-semibold sm:table-cell">Type</TableHead>
                                            <TableHead className="hidden cursor-pointer font-semibold hover:bg-(--secondary-w10)/30 sm:table-cell sm:w-1/4" onClick={() => handleColumnSort("agentCount")}>
                                                <div className="flex items-center">
                                                    Agent Usage
                                                    {getSortIcon("agentCount")}
                                                </div>
                                            </TableHead>
                                            <TableHead className="hidden cursor-pointer font-semibold hover:bg-(--secondary-w10)/30 sm:table-cell sm:w-1/4" onClick={() => handleColumnSort("createdBy")}>
                                                <div className="flex items-center">
                                                    Created By
                                                    {getSortIcon("createdBy")}
                                                </div>
                                            </TableHead>
                                            <TableHead className="w-12 text-right" />
                                        </TableRow>
                                    </TableHeader>
                                    <TableBody borderLastRow>
                                        {currentToolsets.map(toolset => (
                                            <TableRow
                                                key={toolset.id}
                                                data-testid={`toolsetRow-${toolset.id}`}
                                                className="cursor-pointer hover:bg-(--secondary-w10)/50"
                                                onClick={() => handleSelectToolset(toolset)}
                                                data-state={selectedToolset?.id === toolset.id ? "selected" : undefined}
                                            >
                                                <TableCell className="sm:w-1/4">
                                                    <div className="flex min-w-0 items-center gap-3 pl-5">
                                                        <Button
                                                            title={toolset.name}
                                                            variant="link"
                                                            className="min-w-0 p-0"
                                                            onClick={e => {
                                                                e.stopPropagation();
                                                                if (toolset.type === "custom") {
                                                                    handleViewCustomToolset(toolset);
                                                                } else {
                                                                    handleSelectToolset(toolset);
                                                                }
                                                            }}
                                                        >
                                                            <span className="truncate">{toolset.name}</span>
                                                        </Button>
                                                        <ToolsetDiscoveryIndicator toolset={toolset} />
                                                    </div>
                                                </TableCell>
                                                <TableCell data-testid={`toolsetTypeCell-${toolset.id}`} className="hidden w-24 sm:table-cell">
                                                    {toolset.type === "builtin" ? "Built-in" : "Custom"}
                                                </TableCell>
                                                <TableCell data-testid={`agentCountCell-${toolset.id}`} className="hidden sm:table-cell sm:w-1/4">
                                                    {toolset.agents?.length ?? 0}
                                                </TableCell>
                                                <TableCell className="hidden sm:table-cell sm:w-1/4">{toolset.createdBy || "—"}</TableCell>
                                                <TableCell className="w-12 text-right">
                                                    {toolset.type !== "builtin" && (
                                                        <ToolsetMorePopover key="more-popover" toolset={toolset}>
                                                            {MorePopoverButton}
                                                        </ToolsetMorePopover>
                                                    )}
                                                </TableCell>
                                            </TableRow>
                                        ))}
                                    </TableBody>
                                </Table>
                            </div>
                        ) : (
                            <div className="flex min-h-[300px] flex-1 items-center justify-center">
                                {searchTerm || hasActiveFilters ? (
                                    <EmptyState
                                        variant="noImage"
                                        title="No toolsets match your filters."
                                        subtitle="Try adjusting your search terms or filters."
                                        buttons={[
                                            {
                                                text: "Clear filters",
                                                variant: "default",
                                                onClick: () => {
                                                    setSearchTerm("");
                                                    clearFilters();
                                                },
                                            },
                                        ]}
                                    />
                                ) : (
                                    <div className="flex flex-col items-center">
                                        <EmptyState
                                            className="max-w-2xl text-center"
                                            variant="noImage"
                                            title="Extend your agents with toolsets"
                                            subtitle="You haven't created any toolsets yet. Toolsets let you upload custom tools that run in a secure sandbox, giving your agents specialized capabilities."
                                        />
                                        {canCreateToolsets && (
                                            <Button className="w-min" onClick={handleCreateCustomToolset}>
                                                Create Toolset
                                            </Button>
                                        )}
                                    </div>
                                )}
                            </div>
                        )}
                    </div>
                </div>
                <PaginationControls totalPages={totalPages} currentPage={effectiveCurrentPage} onPageChange={handlePageChange} />
            </div>

            {selectedToolset && (
                <PageSidePanelWrapper
                    isSidePanelOpen={isSidePanelOpen}
                    onClose={handleCloseSidePanel}
                    sidepanel={<ToolsetSidePanel key={selectedToolset.id} toolset={selectedToolset} onClose={handleCloseSidePanel} initialOpenAddToAgent={openAddToAgentOnMount} onAddToAgentOpened={() => setOpenAddToAgentOnMount(false)} />}
                />
            )}
        </FileDropZone>
    );
};
