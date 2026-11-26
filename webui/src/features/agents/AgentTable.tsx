// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";

import { useAgents } from "@/api/agents";
import { useRemoteAgents } from "@/api/remote-agents";

import { useUserContext } from "@/providers/contexts";

import { useAgentSelection } from "@/hooks/useAgentSelection";
import { deferPanelClose } from "@/hooks/useRadixPointerEventsFix";

import { PageSidePanelWrapper } from "@/common/layout";

import { SearchInput } from "@/ui/search-input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";

import { AgentNameDuplicateIcon } from "@/features/agents/AgentNameDuplicateIcon";
import { useDuplicateDisplayName } from "@/features/agents/hooks";
import { WorkflowDetailPanel } from "@/features/workflows/WorkflowDetailPanel";

import { LayoutSelector, MorePopoverButton, PaginationControls } from "@/common";
import type { AgentCardInfo, LayoutType } from "@/types";

import { AgentActionsPopover } from "./AgentActionsPopover";
import { AgentCardSidePanel } from "./AgentCardSidePanel";
import { AgentListPlaceholder } from "./AgentListPlaceholder";
import { DeploymentStatus, getAgentToDisplay } from "./common";
import { useAllAgents } from "./hooks/useAllAgents";
import { lookupAgent } from "./utils/agentLookup";
import { getAgentTypeLabel } from "./utils/agentType";
import { type AgentKind, filterAgentsByKind, isDeployed, sortAgentsByDisplayName } from "./utils/filters";

interface AgentTableLayoutProps {
    selectedLayout: string;
    availableLayouts: readonly LayoutType[];
    onLayoutChange: (layout: string) => void;
    duplicateBanner?: React.ReactNode;
    onDeployed?: (agentName: string) => void;
    agentKind: AgentKind;
}

export const AgentTable = ({ selectedLayout, availableLayouts, onLayoutChange, duplicateBanner, onDeployed, agentKind }: Readonly<AgentTableLayoutProps>) => {
    const { data: userAgents = [] } = useAgents();
    const { data: remoteAgents = [] } = useRemoteAgents();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { chatWithAgent } = useAgentSelection();

    const [searchTerm, setSearchTerm] = useState<string>("");
    const [currentPage, setCurrentPage] = useState<number>(1);
    const [screenHeight, setScreenHeight] = useState<number>(typeof window !== "undefined" ? window.innerHeight : 768);
    const [selectedAgent, setSelectedAgent] = useState<AgentCardInfo | null>(null);
    const [isSidePanelOpen, setIsSidePanelOpen] = useState<boolean>(false);

    const { agentCards, isLoading: isLoadingAgents } = useAllAgents({ includeWorkflows: true });

    const displayAgents = useMemo(() => agentCards.filter(isDeployed), [agentCards]);

    const itemsPerPage = screenHeight >= 900 ? 20 : 10;

    useEffect(() => {
        const handleResize = () => {
            if (typeof window !== "undefined") {
                setScreenHeight(window.innerHeight);
            }
        };

        window.addEventListener("resize", handleResize);
        return () => window.removeEventListener("resize", handleResize);
    }, []);

    const { hasDuplicateDisplayName } = useDuplicateDisplayName();

    const filteredAgents = useMemo(() => {
        const searchLower = searchTerm.trim().toLowerCase();
        const byKind = filterAgentsByKind(displayAgents, agentKind);
        const bySearch = searchLower ? byKind.filter(agent => agent.displayName?.toLowerCase().includes(searchLower)) : byKind;
        return sortAgentsByDisplayName(bySearch);
    }, [displayAgents, searchTerm, agentKind]);

    const totalPages = Math.ceil(filteredAgents.length / itemsPerPage);
    const effectiveCurrentPage = Math.min(currentPage, Math.max(totalPages, 1));
    const startIndex = (effectiveCurrentPage - 1) * itemsPerPage;
    const endIndex = startIndex + itemsPerPage;
    const currentAgents = filteredAgents?.slice(startIndex, endIndex) || [];

    useEffect(() => {
        setCurrentPage(1);
    }, [searchTerm, agentKind]);

    useEffect(() => {
        setSearchTerm("");
        setIsSidePanelOpen(false);
        setSelectedAgent(null);
    }, [agentKind]);

    const handlePageChange = useCallback(
        (page: number) => {
            if (page >= 1 && page <= totalPages) {
                setCurrentPage(page);
            }
        },
        [totalPages]
    );

    const handleSelectAgent = useCallback((agent: AgentCardInfo | null) => {
        setSelectedAgent(agent);
        setIsSidePanelOpen(true);
    }, []);

    const handleChatWithAgent = useCallback(
        (agent: AgentCardInfo) => {
            chatWithAgent(agent?.name || "");
        },
        [chatWithAgent]
    );

    const handleCloseSidePanel = useCallback(() => {
        setIsSidePanelOpen(false);
    }, []);

    const handleCloseComplete = useCallback(() => {
        setSelectedAgent(null);
    }, []);

    const onAgentDeleted = useCallback(() => {
        deferPanelClose(() => {
            setSelectedAgent(null);
            setIsSidePanelOpen(false);
        });
    }, []);

    const showTypeColumn = agentKind === "agent";

    const showCreatedByColumn = agentKind === "agent";

    let sidePanelContent: ReactNode = null;
    if (selectedAgent?.isWorkflow) {
        sidePanelContent = <WorkflowDetailPanel workflow={selectedAgent} onClose={handleCloseSidePanel} showOpenButton={false} />;
    } else if (selectedAgent) {
        sidePanelContent = <AgentCardSidePanel agent={selectedAgent} selectedState="active" viewOnly onClose={handleCloseSidePanel} onDeployed={onDeployed} />;
    }

    return (
        <div className="flex h-full w-full">
            <div className="flex flex-1 flex-col">
                {duplicateBanner}

                <div className="flex min-w-0 flex-wrap items-center justify-between gap-3 p-4 sm:p-6">
                    <div className="flex min-w-0 flex-nowrap items-center gap-3 sm:gap-4">
                        <SearchInput value={searchTerm} onChange={setSearchTerm} placeholder="Filter by name" testid="agentSearchInput" />
                    </div>
                    <div className="flex shrink-0 items-center gap-4">{selectedLayout && onLayoutChange && <LayoutSelector currentLayout={selectedLayout} onLayoutChange={onLayoutChange} availableLayouts={availableLayouts} />}</div>
                </div>

                <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
                    {currentAgents.length > 0 ? (
                        <div className="flex min-h-0 flex-1 flex-col px-4 pb-4 sm:pr-2 sm:pl-6">
                            <div className="min-h-0 flex-1 overflow-y-auto rounded-xs border">
                                <Table containerClassName="overflow-x-visible">
                                    <TableHeader sticky>
                                        <TableRow>
                                            <TableHead className="pl-4 font-semibold">Name</TableHead>
                                            <TableHead className="font-semibold sm:w-1/5">Status</TableHead>
                                            {showTypeColumn && <TableHead className="hidden font-semibold sm:table-cell sm:w-1/5">Type</TableHead>}
                                            {showCreatedByColumn && <TableHead className="hidden font-semibold sm:table-cell sm:w-1/5">Created By</TableHead>}
                                            <TableHead className="w-12 font-semibold"></TableHead>
                                        </TableRow>
                                    </TableHeader>
                                    <TableBody borderLastRow>
                                        {currentAgents.map(agent => {
                                            const agentToDisplay = getAgentToDisplay(agent, "active");
                                            const userAgent = lookupAgent(agent.displayName, userAgents, remoteAgents);
                                            const hasDuplicate = hasDuplicateDisplayName(agent.name);
                                            return (
                                                <TableRow
                                                    key={agentToDisplay.name}
                                                    className="h-14 cursor-pointer hover:bg-(--secondary-w10)/50"
                                                    onClick={() => handleSelectAgent(agent)}
                                                    data-state={selectedAgent?.name === agentToDisplay.name ? "selected" : undefined}
                                                >
                                                    <TableCell className="max-w-0 pl-4 font-semibold">
                                                        <div className="flex min-w-0 items-center gap-2">
                                                            <span className="min-w-0 truncate">{agentToDisplay.displayName}</span>
                                                            <AgentNameDuplicateIcon show={hasDuplicate} />
                                                        </div>
                                                    </TableCell>
                                                    <TableCell>
                                                        <div className="flex items-center gap-2">
                                                            <DeploymentStatus agent={agentToDisplay} selectedState="active" showSyncStatus={userHasCapability(CAPABILITIES.readAgents)} />
                                                        </div>
                                                    </TableCell>
                                                    {showTypeColumn && <TableCell className="hidden sm:table-cell">{getAgentTypeLabel(agentToDisplay.type)}</TableCell>}
                                                    {showCreatedByColumn && <TableCell className="hidden sm:table-cell">{agentToDisplay.createdBy ?? "N/A"}</TableCell>}
                                                    <TableCell className="w-12" onClick={e => e.stopPropagation()}>
                                                        {!agent.isWorkflow && (
                                                            <AgentActionsPopover agent={agentToDisplay} userAgent={userAgent} selectedState="active" viewOnly chatWithAgent={handleChatWithAgent} onAgentDeleted={onAgentDeleted} onDeployed={onDeployed}>
                                                                {MorePopoverButton}
                                                            </AgentActionsPopover>
                                                        )}
                                                    </TableCell>
                                                </TableRow>
                                            );
                                        })}
                                    </TableBody>
                                </Table>
                            </div>
                        </div>
                    ) : (
                        <AgentListPlaceholder isLoading={isLoadingAgents} searchTerm={searchTerm} noun={agentKind === "workflow" ? "Workflows" : "Agents"} onClearFilter={() => setSearchTerm("")} />
                    )}
                </div>
                <PaginationControls totalPages={totalPages} currentPage={effectiveCurrentPage} onPageChange={handlePageChange} />
            </div>
            <PageSidePanelWrapper isSidePanelOpen={isSidePanelOpen} onClose={handleCloseSidePanel} sidepanel={sidePanelContent} onCloseComplete={handleCloseComplete} />
        </div>
    );
};
