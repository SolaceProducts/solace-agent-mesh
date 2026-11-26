// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";

import { deferPanelClose } from "@/hooks/useRadixPointerEventsFix";

import { SearchInput } from "@/ui/search-input";

import { useDuplicateDisplayName } from "@/features/agents/hooks";
import { WorkflowDetailPanel } from "@/features/workflows/WorkflowDetailPanel";

import { LayoutSelector, PageSidePanelWrapper, PaginationControls } from "@/common";
import type { AgentCardInfo, LayoutType } from "@/types";

import { AgentCardSidePanel } from "./AgentCardSidePanel";
import { AgentDisplayCard } from "./AgentDisplayCard";
import { AgentListPlaceholder } from "./AgentListPlaceholder";
import { useAllAgents } from "./hooks/useAllAgents";
import { type AgentKind, filterAgentsByKind, isDeployed, kindOf } from "./utils/filters";

interface AgentMeshCardsProps {
    selectedLayout: string;
    availableLayouts: readonly LayoutType[];
    onLayoutChange: (layout: string) => void;
    duplicateBanner?: React.ReactNode;
    onDeployed?: (agentName: string) => void;
    agentKind: AgentKind;
    onDeepLinkKindResolved?: (kind: AgentKind) => void;
}

export const AgentMeshCards = ({ selectedLayout, availableLayouts, onLayoutChange, duplicateBanner, onDeployed, agentKind, onDeepLinkKindResolved }: Readonly<AgentMeshCardsProps>) => {
    const [searchTerm, setSearchTerm] = useState<string>("");
    const [currentPage, setCurrentPage] = useState<number>(1);
    const [screenWidth, setScreenWidth] = useState<number>(typeof window !== "undefined" ? window.innerWidth : 1024);
    const [selectedAgent, setSelectedAgent] = useState<AgentCardInfo | null>(null);
    const [isSidePanelOpen, setIsSidePanelOpen] = useState<boolean>(false);

    const { agentCards, isLoading: isLoadingAgents } = useAllAgents({ includeWorkflows: true });

    const agents = useMemo(() => agentCards.filter(isDeployed), [agentCards]);

    const { hasDuplicateDisplayName } = useDuplicateDisplayName();

    const itemsPerPage = screenWidth >= 2560 ? 20 : 16;

    useEffect(() => {
        const handleResize = () => {
            if (typeof window !== "undefined") {
                setScreenWidth(window.innerWidth);
            }
        };

        window.addEventListener("resize", handleResize);
        return () => window.removeEventListener("resize", handleResize);
    }, []);

    const filteredAgents = useMemo(() => {
        const searchLower = searchTerm.trim().toLowerCase();
        const byKind = filterAgentsByKind(agents, agentKind);
        return searchLower ? byKind.filter(agent => agent.displayName?.toLowerCase().includes(searchLower)) : byKind;
    }, [agents, searchTerm, agentKind]);

    const totalPages = Math.ceil(filteredAgents.length / itemsPerPage);
    const effectiveCurrentPage = Math.min(currentPage, Math.max(totalPages, 1));
    const currentAgents = useMemo(() => {
        const startIndex = (effectiveCurrentPage - 1) * itemsPerPage;
        const endIndex = startIndex + itemsPerPage;

        return filteredAgents.slice(startIndex, endIndex);
    }, [filteredAgents, effectiveCurrentPage, itemsPerPage]);

    useEffect(() => {
        setCurrentPage(1);
    }, [searchTerm, agentKind]);

    const [searchParams, setSearchParams] = useSearchParams();
    const selectedFromUrl = searchParams.get("selected");
    const consumedSelectedRef = useRef(false);
    useEffect(() => {
        if (consumedSelectedRef.current || !selectedFromUrl) return;
        if (agents.length === 0) return;
        const match = agents.find(a => a.name === selectedFromUrl) ?? agents.find(a => a.displayName === selectedFromUrl);
        if (!match) return;
        consumedSelectedRef.current = true;
        setSelectedAgent(match);
        setIsSidePanelOpen(true);
        onDeepLinkKindResolved?.(kindOf(match));
    }, [agents, selectedFromUrl, onDeepLinkKindResolved]);

    const prevKindRef = useRef(agentKind);
    useEffect(() => {
        if (prevKindRef.current === agentKind) return;
        prevKindRef.current = agentKind;
        setSearchTerm("");
        if (selectedAgent && kindOf(selectedAgent) === agentKind) return;
        setIsSidePanelOpen(false);
        setSelectedAgent(null);
    }, [agentKind, selectedAgent]);

    const prevPanelOpenRef = useRef(isSidePanelOpen);
    useEffect(() => {
        const wasOpen = prevPanelOpenRef.current;
        prevPanelOpenRef.current = isSidePanelOpen;
        if (!wasOpen || isSidePanelOpen) return;
        if (!searchParams.has("selected")) return;
        const next = new URLSearchParams(searchParams);
        next.delete("selected");
        setSearchParams(next, { replace: true });
    }, [isSidePanelOpen, searchParams, setSearchParams]);

    const handlePageChange = useCallback(
        (page: number) => {
            if (page >= 1 && page <= totalPages) {
                setCurrentPage(page);
            }
        },
        [totalPages]
    );

    const handleSelectAgent = useCallback(
        (agent: AgentCardInfo | null) => {
            if (selectedAgent?.name === agent?.name) {
                setSelectedAgent(null);
                setIsSidePanelOpen(false);
                return;
            }
            setSelectedAgent(agent);
            setIsSidePanelOpen(true);
        },
        [selectedAgent]
    );

    const handleCloseSidePanel = useCallback(() => {
        setIsSidePanelOpen(false);
    }, []);

    const handleCloseComplete = useCallback(() => {
        setSelectedAgent(null);
    }, []);

    const handleAgentDeleted = useCallback(() => {
        deferPanelClose(() => {
            setSelectedAgent(null);
            setIsSidePanelOpen(false);
        });
    }, []);

    let sidePanelContent: React.ReactNode = null;
    if (selectedAgent?.isWorkflow) {
        sidePanelContent = <WorkflowDetailPanel workflow={selectedAgent} onClose={handleCloseSidePanel} showOpenButton={false} />;
    } else if (selectedAgent) {
        sidePanelContent = <AgentCardSidePanel agent={selectedAgent} selectedState="active" viewOnly onClose={handleCloseSidePanel} onDeployed={onDeployed} />;
    }

    return (
        <div className="flex h-full w-full">
            <div className="flex min-w-0 flex-1 flex-col">
                {duplicateBanner}

                <div className="flex flex-wrap items-center justify-between gap-3 p-4 sm:p-6">
                    <div className="flex min-w-0 flex-nowrap items-center gap-3 sm:gap-4">
                        <SearchInput value={searchTerm} onChange={setSearchTerm} placeholder="Filter by name" testid="agentSearchInput" />
                    </div>
                    <div className="flex items-center gap-4">
                        <LayoutSelector currentLayout={selectedLayout} onLayoutChange={onLayoutChange} availableLayouts={availableLayouts} />
                    </div>
                </div>

                {currentAgents.length > 0 ? (
                    <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-4 sm:px-6 sm:pb-6">
                        <div className="h-full min-h-full">
                            <div className="grid grid-cols-[repeat(auto-fill,minmax(min(320px,100%),1fr))] gap-6 py-3">
                                {currentAgents.map(agent => {
                                    const onSelectAgent = () => handleSelectAgent(agent);
                                    return (
                                        <AgentDisplayCard
                                            key={agent.name}
                                            agent={agent}
                                            onSelect={onSelectAgent}
                                            selected={selectedAgent?.name === agent.name}
                                            onAgentDeleted={handleAgentDeleted}
                                            hasDuplicate={hasDuplicateDisplayName(agent.name)}
                                            onDeployed={onDeployed}
                                        />
                                    );
                                })}
                            </div>
                        </div>
                    </div>
                ) : (
                    <div className="flex min-h-0 flex-1 flex-col">
                        <AgentListPlaceholder isLoading={isLoadingAgents} searchTerm={searchTerm} noun={agentKind === "workflow" ? "Workflows" : "Agents"} onClearFilter={() => setSearchTerm("")} />
                    </div>
                )}
                <PaginationControls totalPages={totalPages} currentPage={effectiveCurrentPage} onPageChange={handlePageChange} />
            </div>
            <PageSidePanelWrapper isSidePanelOpen={isSidePanelOpen} onClose={handleCloseSidePanel} sidepanel={sidePanelContent} onCloseComplete={handleCloseComplete} />
        </div>
    );
};
