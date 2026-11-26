// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { RefreshCcw } from "lucide-react";
import type { ReactNode } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";

import { invalidateAgentRelations } from "@/api/agents/invalidate";
import { remoteAgentKeys } from "@/api/remote-agents/keys";
import { useBuiltinToolsets } from "@/api/toolsets";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { useAgentSelection } from "@/hooks/useAgentSelection";
import { deferPanelClose } from "@/hooks/useRadixPointerEventsFix";
import { useToggle } from "@/hooks/useToggle";

import { PageSidePanelWrapper } from "@/common/layout";

import { Button } from "@/ui/button";
import { SearchInput } from "@/ui/search-input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui/table";

import { AgentNameDuplicateIcon } from "@/features/agents/AgentNameDuplicateIcon";
import { useDuplicateDisplayName } from "@/features/agents/hooks";

import { Header, MessageBanner, MorePopoverButton, PageLayout, PaginationControls } from "@/common";
import type { AgentCardInfo, AgentState } from "@/types";

import { AgentActionsPopover } from "./AgentActionsPopover";
import { AgentCardSidePanel } from "./AgentCardSidePanel";
import { AgentCreateDialog } from "./AgentCreateDialog";
import { AgentCreateFromPromptDialog } from "./AgentCreateFromPromptDialog";
import { AgentListPlaceholder } from "./AgentListPlaceholder";
import { DeploymentStatus, getAgentToDisplay } from "./common";
import { DeploymentToggle } from "./DeploymentToggle";
import { useAllAgents } from "./hooks/useAllAgents";
import { NewAgentDropdown } from "./NewAgentDropdown";
import { getAgentTypeLabel } from "./utils/agentType";
import { type DeploymentFilter, filterAgentsByDeployment, isDeployed, sortAgentsByDisplayName } from "./utils/filters";

const stateForAgent = (agent: AgentCardInfo): AgentState => (isDeployed(agent) ? "active" : "inactive");

export const AgentManagementPage = () => {
    const navigate = useNavigate();
    const [searchParams, setSearchParams] = useSearchParams();
    const queryClient = useQueryClient();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { chatWithAgent } = useAgentSelection();
    const { hasDuplicateDisplayName } = useDuplicateDisplayName();
    const { platformConfigured, handleCreateAgent } = useChatContext();

    const { agentCards, isLoading: agentsLoading, isError: fetchError, errorMessage: fetchErrorMessage } = useAllAgents();
    const { data: toolsets = [] } = useBuiltinToolsets();

    const initialTabParam = searchParams.get("tab");
    const initialFilter: DeploymentFilter = initialTabParam === "undeployed" ? "undeployed" : "deployed";
    const [filter, setFilter] = useState<DeploymentFilter>(initialFilter);

    const tabParam = searchParams.get("tab");
    useEffect(() => {
        const next: DeploymentFilter = tabParam === "undeployed" ? "undeployed" : "deployed";
        if (next !== filter) setFilter(next);
    }, [tabParam, filter]);
    const [searchTerm, setSearchTerm] = useState<string>("");
    const [currentPage, setCurrentPage] = useState<number>(1);
    const [screenHeight, setScreenHeight] = useState<number>(typeof window !== "undefined" ? window.innerHeight : 768);
    const [selectedAgent, setSelectedAgent] = useState<AgentCardInfo | null>(null);
    const [isSidePanelOpen, , setIsSidePanelOpen] = useToggle(false);
    const [isCreateAgentDialogOpen, setIsCreateAgentDialogOpen] = useState<boolean>(false);
    const [useManualCreation, setUseManualCreation] = useState<boolean>(false);

    const userCanCreateAgents = userHasCapability(CAPABILITIES.createAgents);

    const refreshAgentLists = useCallback(() => {
        invalidateAgentRelations(queryClient);
        queryClient.invalidateQueries({ queryKey: remoteAgentKeys.lists() });
    }, [queryClient]);

    const headerButtons = useMemo(() => {
        const buttons: ReactNode[] = [];
        if (userCanCreateAgents) {
            buttons.push(<NewAgentDropdown key="new-agent-dropdown" platformConfigured={!!platformConfigured} onCreateNew={() => setIsCreateAgentDialogOpen(true)} onConnectExternal={() => navigate("/agents/external/new")} />);
        }
        buttons.push(
            <Button key="refresh" variant="ghost" title="Refresh Agents" onClick={refreshAgentLists} className="px-3 sm:px-4">
                <RefreshCcw />
                <span className="hidden sm:inline">Refresh</span>
            </Button>
        );
        return buttons;
    }, [userCanCreateAgents, platformConfigured, navigate, refreshAgentLists]);

    const countsReady = !agentsLoading && !fetchError;
    const deployedCount = useMemo(() => (countsReady ? agentCards.filter(isDeployed).length : undefined), [agentCards, countsReady]);
    const undeployedCount = countsReady ? agentCards.length - (deployedCount ?? 0) : undefined;

    const filteredByDeployment = useMemo(() => filterAgentsByDeployment(agentCards, filter), [agentCards, filter]);

    const filteredAgents = useMemo(() => {
        if (!searchTerm.trim()) return sortAgentsByDisplayName(filteredByDeployment);
        const lower = searchTerm.toLowerCase();
        return sortAgentsByDisplayName(filteredByDeployment.filter(card => card.displayName?.toLowerCase().includes(lower)));
    }, [filteredByDeployment, searchTerm]);

    const itemsPerPage = screenHeight >= 900 ? 20 : 10;

    useEffect(() => {
        const onResize = () => {
            if (typeof window !== "undefined") {
                setScreenHeight(window.innerHeight);
            }
        };
        window.addEventListener("resize", onResize);
        return () => window.removeEventListener("resize", onResize);
    }, []);

    useEffect(() => {
        setCurrentPage(1);
    }, [filter, searchTerm]);

    const targetAgentId = searchParams.get("id");
    const explicitTab = searchParams.get("tab");
    const openedIdRef = useRef<string | null>(null);
    useEffect(() => {
        if (!targetAgentId || agentCards.length === 0) return;
        if (openedIdRef.current === targetAgentId) return;

        const normalized = targetAgentId.replace(/-/g, "_");
        const target = agentCards.find(card => card.name === `agent_${normalized}` || card.name === `remote_agent_${normalized}`);
        if (!target) return;

        openedIdRef.current = targetAgentId;
        setSelectedAgent(target);
        setIsSidePanelOpen(true);
        if (!explicitTab) {
            setFilter(isDeployed(target) ? "deployed" : "undeployed");
        }
    }, [targetAgentId, agentCards, setIsSidePanelOpen, explicitTab]);

    const totalPages = Math.max(1, Math.ceil(filteredAgents.length / itemsPerPage));
    const effectiveCurrentPage = Math.min(currentPage, totalPages);
    const startIndex = (effectiveCurrentPage - 1) * itemsPerPage;
    const currentAgents = filteredAgents.slice(startIndex, startIndex + itemsPerPage);

    const handlePageChange = useCallback(
        (page: number) => {
            if (page >= 1 && page <= totalPages) {
                setCurrentPage(page);
            }
        },
        [totalPages]
    );

    const handleSelectAgent = useCallback((agent: AgentCardInfo) => {
        setSelectedAgent(agent);
        setIsSidePanelOpen(true);
    }, []);

    const handleCloseSidePanel = useCallback(() => setIsSidePanelOpen(false), []);
    const handleCloseComplete = useCallback(() => setSelectedAgent(null), []);

    const handleAgentDeleted = useCallback(() => {
        refreshAgentLists();
        deferPanelClose(() => {
            setSelectedAgent(null);
            setIsSidePanelOpen(false);
        });
    }, [refreshAgentLists, setIsSidePanelOpen]);

    const handleChatWithAgent = useCallback(
        (agent: AgentCardInfo) => {
            chatWithAgent(agent?.name ?? "");
        },
        [chatWithAgent]
    );

    const handleFilterChange = useCallback(
        (next: DeploymentFilter) => {
            setFilter(next);
            setSearchTerm("");
            setSelectedAgent(null);
            setIsSidePanelOpen(false);
            const next_params = new URLSearchParams(searchParams);
            next_params.set("tab", next);
            setSearchParams(next_params, { replace: true });
            refreshAgentLists();
        },
        [refreshAgentLists, searchParams, setSearchParams]
    );

    return (
        <PageLayout>
            <div className="flex h-full flex-col">
                <Header title="Agents" buttons={headerButtons} />

                <div className="flex min-h-0 w-full flex-1">
                    <div className="flex min-w-0 flex-1 flex-col">
                        <div className="flex min-w-0 flex-col gap-3 px-4 pt-4 pb-3 sm:flex-row sm:flex-wrap sm:items-center sm:justify-between sm:gap-3 sm:px-6 sm:pt-6">
                            <div className="flex justify-center sm:justify-start">
                                <DeploymentToggle selected={filter} deployedCount={deployedCount} undeployedCount={undeployedCount} disabled={agentsLoading} onChange={handleFilterChange} />
                            </div>
                            <SearchInput value={searchTerm} onChange={setSearchTerm} placeholder="Filter by name" testid="agentManagementSearchInput" />
                        </div>

                        {fetchError && (
                            <div className="px-6 pb-3">
                                <MessageBanner variant="error" message={fetchErrorMessage ? `Could not load agents: ${fetchErrorMessage}` : "Could not load agents."} />
                            </div>
                        )}

                        <div className="flex min-h-0 flex-1 flex-col overflow-hidden px-6 pb-6">
                            <div className="flex min-h-0 flex-1 flex-col">
                                {currentAgents.length > 0 ? (
                                    <div className="min-h-0 flex-1 overflow-y-auto rounded-xs border">
                                        <Table containerClassName="overflow-x-visible">
                                            <TableHeader sticky>
                                                <TableRow>
                                                    <TableHead className="pl-4 font-semibold">Name</TableHead>
                                                    <TableHead className="pl-4 font-semibold sm:w-1/5">Status</TableHead>
                                                    <TableHead className="hidden font-semibold sm:table-cell sm:w-1/5">Type</TableHead>
                                                    <TableHead className="hidden font-semibold sm:table-cell sm:w-1/5">Created By</TableHead>
                                                    <TableHead className="w-12 font-semibold"></TableHead>
                                                </TableRow>
                                            </TableHeader>
                                            <TableBody borderLastRow>
                                                {currentAgents.map(agent => {
                                                    const rowState = stateForAgent(agent);
                                                    const display = getAgentToDisplay(agent, rowState);
                                                    const hasDuplicate = hasDuplicateDisplayName(agent.name);
                                                    return (
                                                        <TableRow
                                                            key={display.name}
                                                            className="h-14 cursor-pointer hover:bg-(--secondary-w10)/50"
                                                            onClick={() => handleSelectAgent(agent)}
                                                            data-state={selectedAgent?.name === display.name ? "selected" : undefined}
                                                        >
                                                            <TableCell className="max-w-0 pl-4 font-semibold">
                                                                <div className="flex min-w-0 items-center gap-2">
                                                                    <Button
                                                                        variant="link"
                                                                        className="min-w-0 truncate p-0"
                                                                        onClick={e => {
                                                                            e.stopPropagation();
                                                                            handleSelectAgent(agent);
                                                                        }}
                                                                    >
                                                                        {display.displayName}
                                                                    </Button>
                                                                    <AgentNameDuplicateIcon show={hasDuplicate} />
                                                                </div>
                                                            </TableCell>
                                                            <TableCell className="pl-4">
                                                                <DeploymentStatus agent={display} selectedState={rowState} showSyncStatus={userHasCapability(CAPABILITIES.readAgents)} />
                                                            </TableCell>
                                                            <TableCell className="hidden sm:table-cell">{getAgentTypeLabel(display.type)}</TableCell>
                                                            <TableCell className="hidden sm:table-cell">{display.createdBy ?? "N/A"}</TableCell>
                                                            <TableCell className="w-12" onClick={e => e.stopPropagation()}>
                                                                <AgentActionsPopover
                                                                    agent={display}
                                                                    userAgent={undefined}
                                                                    selectedState={rowState}
                                                                    chatWithAgent={handleChatWithAgent}
                                                                    onAgentDeleted={handleAgentDeleted}
                                                                    onDeploymentChanged={refreshAgentLists}
                                                                    showViewAccess
                                                                >
                                                                    {MorePopoverButton}
                                                                </AgentActionsPopover>
                                                            </TableCell>
                                                        </TableRow>
                                                    );
                                                })}
                                            </TableBody>
                                        </Table>
                                    </div>
                                ) : (
                                    <AgentListPlaceholder isLoading={agentsLoading} searchTerm={searchTerm} onClearFilter={() => setSearchTerm("")} />
                                )}
                            </div>
                        </div>

                        <PaginationControls totalPages={totalPages} currentPage={effectiveCurrentPage} onPageChange={handlePageChange} />
                    </div>

                    <PageSidePanelWrapper
                        isSidePanelOpen={isSidePanelOpen}
                        onClose={handleCloseSidePanel}
                        sidepanel={
                            selectedAgent ? <AgentCardSidePanel agent={selectedAgent} selectedState={stateForAgent(selectedAgent)} onClose={handleCloseSidePanel} onDeploymentChanged={refreshAgentLists} onAgentDeleted={handleAgentDeleted} /> : null
                        }
                        onCloseComplete={handleCloseComplete}
                    />
                </div>
            </div>
            {useManualCreation ? (
                <AgentCreateDialog
                    isOpen={isCreateAgentDialogOpen}
                    onClose={() => {
                        setIsCreateAgentDialogOpen(false);
                        setUseManualCreation(false);
                    }}
                    onCreate={(name, description) => {
                        const defaultToolsets = toolsets?.filter(t => t.default) ?? [];
                        handleCreateAgent({
                            name,
                            description,
                            inputModes: ["text"],
                            outputModes: ["text"],
                            toolsets: defaultToolsets.map(t => t.id),
                            skills: defaultToolsets.map(t => ({ name: t.name, description: t.description ?? "" })),
                        });
                    }}
                />
            ) : (
                <AgentCreateFromPromptDialog
                    isOpen={isCreateAgentDialogOpen}
                    onClose={() => setIsCreateAgentDialogOpen(false)}
                    onCreate={(agent, originalPrompt) => handleCreateAgent(agent, originalPrompt)}
                    onSkip={() => setUseManualCreation(true)}
                />
            )}
        </PageLayout>
    );
};
