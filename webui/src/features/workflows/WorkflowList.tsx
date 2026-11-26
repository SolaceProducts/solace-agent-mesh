// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useRef, useState } from "react";
import { Sparkles } from "lucide-react";
import { useNavigate, useSearchParams } from "react-router-dom";

import { OnboardingBanner } from "@/common/banners";
import { EmptyState, HealthProblemsIndicator, OnboardingView, STATUS_COLOUR_MAP, STATUS_LABEL_MAP } from "@/common/status";

import { Pagination, PaginationContent, PaginationEllipsis, PaginationItem, PaginationLink, PaginationNext, PaginationPrevious } from "@/ui/pagination";

import { buildWorkflowNavigationUrl } from "@/features/workflowVisualization/utils/workflowNavigation";

import { WorkflowIllustration } from "@/assets";
import { MorePopoverButton } from "@/common";
import { useIsMobile } from "@/hooks";
import type { AgentCardInfo } from "@/types";
import { Button, SearchInput, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/ui";
import { DOC_LINKS } from "@/utils";

import { WorkflowActionsPopover } from "./WorkflowActionsPopover";
import { WorkflowDetailPanel } from "./WorkflowDetailPanel";
import { WORKFLOW_ID_PARAM } from "./workflowManagementUrl";

const WORKFLOW_STORAGE_KEY = "sam-workflow-onboarding-dismissed";
const WORKFLOW_URL = DOC_LINKS.buildingWorkflows;
const WORKFLOW_HEADER = "Workflows give enterprises production-ready, best-practice agent patterns that are predictable and reliable.";
const WORKFLOW_DESCRIPTION = "Turn complex multi-agent tasks into streamlined workflows. Great for building repeatable processes that need multiple agents working together in a specific order.";
const WORKFLOW_LEARN_MORE_TEXT = "Learn how to create workflows";

interface WorkflowListProps {
    workflows: AgentCardInfo[];
    className?: string;
    onEditInBuilder?: (workflow: AgentCardInfo) => void;
    onBuildWithAI?: () => void;
    canDelete?: boolean;
    canEdit?: boolean;
    canViewAccess?: boolean;
    workflowIdByName?: Record<string, string>;
}

export const WorkflowList = ({ workflows, className, onEditInBuilder, onBuildWithAI, canDelete = false, canEdit = false, canViewAccess = false, workflowIdByName = {} }: Readonly<WorkflowListProps>) => {
    const navigate = useNavigate();
    const [searchTerm, setSearchTerm] = useState<string>("");
    const [currentPage, setCurrentPage] = useState<number>(1);
    const [screenHeight, setScreenHeight] = useState<number>(typeof window !== "undefined" ? window.innerHeight : 768);
    const [selectedWorkflow, setSelectedWorkflow] = useState<AgentCardInfo | null>(null);
    const [isSidePanelOpen, setIsSidePanelOpen] = useState<boolean>(false);
    const isMobile = useIsMobile();

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

    const filteredWorkflows = useMemo(() => {
        if (!workflows || workflows.length === 0) return [];

        const result = searchTerm.trim() ? workflows.filter(workflow => (workflow.displayName || workflow.name)?.toLowerCase().includes(searchTerm.toLowerCase())) : workflows;

        return result.slice().sort((a, b) => (a.displayName || a.name).localeCompare(b.displayName || b.name));
    }, [workflows, searchTerm]);

    const totalPages = Math.ceil(filteredWorkflows.length / itemsPerPage);
    const effectiveCurrentPage = Math.min(currentPage, Math.max(totalPages, 1));
    const startIndex = (effectiveCurrentPage - 1) * itemsPerPage;
    const endIndex = startIndex + itemsPerPage;
    const currentWorkflows = filteredWorkflows.slice(startIndex, endIndex);

    useEffect(() => {
        setCurrentPage(1);
    }, [searchTerm]);

    useEffect(() => {
        if (!selectedWorkflow) return;
        if (workflows?.some(workflow => workflow.name === selectedWorkflow.name) === false) {
            setIsSidePanelOpen(false);
            setSelectedWorkflow(null);
        }
    }, [workflows, selectedWorkflow]);

    const handlePageChange = (page: number) => {
        if (page >= 1 && page <= totalPages) {
            setCurrentPage(page);
        }
    };

    const handleSelectWorkflow = (workflow: AgentCardInfo | null) => {
        if (workflow) {
            if (selectedWorkflow?.name === workflow.name && isSidePanelOpen) {
                handleCloseSidePanel();
            } else {
                setSelectedWorkflow(workflow);
                setIsSidePanelOpen(true);
            }
        }
    };

    const handleCloseSidePanel = () => {
        setIsSidePanelOpen(false);
        setSelectedWorkflow(null);
    };

    const [searchParams] = useSearchParams();
    const targetWorkflowId = searchParams.get(WORKFLOW_ID_PARAM);
    const openedIdRef = useRef<string | null>(null);
    useEffect(() => {
        if (!targetWorkflowId || openedIdRef.current === targetWorkflowId) return;
        const target = workflows.find(workflow => workflowIdByName[workflow.name] === targetWorkflowId);
        if (!target) return;
        openedIdRef.current = targetWorkflowId;
        setSelectedWorkflow(target);
        setIsSidePanelOpen(true);
    }, [targetWorkflowId, workflows, workflowIdByName]);

    const getPageNumbers = () => {
        const pages: (number | string)[] = [];
        const maxVisiblePages = 5;

        if (totalPages <= maxVisiblePages) {
            for (let i = 1; i <= totalPages; i++) {
                pages.push(i);
            }
        } else {
            if (effectiveCurrentPage <= 3) {
                for (let i = 1; i <= 4; i++) {
                    pages.push(i);
                }
                pages.push("ellipsis");
                pages.push(totalPages);
            } else if (effectiveCurrentPage >= totalPages - 2) {
                pages.push(1);
                pages.push("ellipsis");
                for (let i = totalPages - 3; i <= totalPages; i++) {
                    pages.push(i);
                }
            } else {
                pages.push(1);
                pages.push("ellipsis");
                for (let i = effectiveCurrentPage - 1; i <= effectiveCurrentPage + 1; i++) {
                    pages.push(i);
                }
                pages.push("ellipsis");
                pages.push(totalPages);
            }
        }

        return pages;
    };

    if (workflows.length === 0) {
        return (
            <OnboardingView
                title={WORKFLOW_HEADER}
                description={WORKFLOW_DESCRIPTION}
                image={<WorkflowIllustration />}
                learnMoreText={WORKFLOW_LEARN_MORE_TEXT}
                learnMoreHref={WORKFLOW_URL}
                actionButton={onBuildWithAI ? { text: "Build with AI", icon: <Sparkles className="mr-2 h-4 w-4" />, onClick: onBuildWithAI } : undefined}
            />
        );
    }

    const PaginationControls = () => {
        if (totalPages <= 1) return null;

        return (
            <div className="mt-4 flex flex-shrink-0 justify-center border-t bg-(--background-w10) pt-4 pb-2">
                <Pagination>
                    <PaginationContent>
                        <PaginationItem>
                            <PaginationPrevious onClick={() => handlePageChange(effectiveCurrentPage - 1)} className={effectiveCurrentPage === 1 ? "pointer-events-none opacity-50" : "cursor-pointer"} />
                        </PaginationItem>

                        {getPageNumbers().map((page, index) => (
                            <PaginationItem key={index}>
                                {page === "ellipsis" ? (
                                    <PaginationEllipsis />
                                ) : (
                                    <PaginationLink onClick={() => handlePageChange(page as number)} isActive={effectiveCurrentPage === page} className="cursor-pointer">
                                        {page}
                                    </PaginationLink>
                                )}
                            </PaginationItem>
                        ))}

                        <PaginationItem>
                            <PaginationNext onClick={() => handlePageChange(effectiveCurrentPage + 1)} className={effectiveCurrentPage === totalPages ? "pointer-events-none opacity-50" : "cursor-pointer"} />
                        </PaginationItem>
                    </PaginationContent>
                </Pagination>
            </div>
        );
    };

    return (
        <div className={`flex h-full w-full ${className ?? ""}`}>
            <div className="flex min-w-0 flex-1 flex-col">
                <OnboardingBanner storageKey={WORKFLOW_STORAGE_KEY} header={WORKFLOW_HEADER} description={WORKFLOW_DESCRIPTION} learnMoreText={WORKFLOW_LEARN_MORE_TEXT} learnMoreUrl={WORKFLOW_URL} className="mx-6 mt-6 mb-6" />
                <SearchInput value={searchTerm} onChange={value => setSearchTerm(value)} testid="workflowSearchInput" className="mx-6" />

                <div className="flex min-h-0 flex-1 flex-col overflow-hidden px-6 pb-6">
                    <div className="flex min-h-0 flex-1 flex-col pt-6">
                        {(() => {
                            if (currentWorkflows.length === 0) {
                                return (
                                    <div className="flex h-full min-h-[300px] items-center justify-center">
                                        <EmptyState variant="notFound" title="No workflows found" subtitle="Try adjusting your search terms" buttons={[{ text: "Clear Filter", variant: "default", onClick: () => setSearchTerm("") }]} />
                                    </div>
                                );
                            }
                            if (isMobile) {
                                return (
                                    <div className="min-h-0 flex-1 divide-y overflow-y-auto rounded-xs border">
                                        {currentWorkflows.map(workflow => (
                                            <div key={workflow.name} className="flex items-center gap-2 px-4 py-3 hover:bg-(--secondary-w10)/50">
                                                <button type="button" data-testid={`workflow-name-${workflow.name}`} onClick={() => handleSelectWorkflow(workflow)} className="flex min-w-0 flex-1 flex-col gap-1 text-left">
                                                    <span className="truncate font-semibold text-(--brand-wMain)">{workflow.displayName ?? workflow.name}</span>
                                                    <span className="flex items-center gap-2 text-sm text-(--secondary-text-wMain)">
                                                        <span className={`h-2 w-2 shrink-0 rounded-full ${STATUS_COLOUR_MAP[workflow.status ?? ""] ?? "bg-(--secondary-text-wMain)"}`} />
                                                        {STATUS_LABEL_MAP[workflow.status ?? ""] ?? "Unknown"} · {workflow.version ? `v${workflow.version}` : "N/A"}
                                                        <HealthProblemsIndicator problems={workflow.runtimeProblems} testIdPrefix="workflowHealth" />
                                                    </span>
                                                </button>
                                                <div className="shrink-0" onClick={e => e.stopPropagation()}>
                                                    <WorkflowActionsPopover
                                                        workflowId={workflowIdByName[workflow.name]}
                                                        workflowName={workflow.displayName || workflow.name}
                                                        onOpen={() => navigate(buildWorkflowNavigationUrl(workflow.name))}
                                                        canDelete={canDelete}
                                                        canEdit={canEdit}
                                                        canViewAccess={canViewAccess}
                                                        onEditInBuilder={onEditInBuilder ? () => onEditInBuilder(workflow) : undefined}
                                                    >
                                                        {MorePopoverButton}
                                                    </WorkflowActionsPopover>
                                                </div>
                                            </div>
                                        ))}
                                    </div>
                                );
                            }
                            return (
                                <div className="min-h-0 flex-1 overflow-y-auto rounded-xs border">
                                    <Table aria-label="Workflows">
                                        <TableHeader>
                                            <TableRow>
                                                <TableHead className="font-semibold">
                                                    <div className="pl-4">Name</div>
                                                </TableHead>
                                                <TableHead className="w-1/4 font-semibold">Version</TableHead>
                                                <TableHead className="w-1/4 font-semibold">Status</TableHead>
                                                <TableHead className="w-12" />
                                            </TableRow>
                                        </TableHeader>
                                        <TableBody borderLastRow>
                                            {currentWorkflows.map(workflow => (
                                                <TableRow key={workflow.name} onClick={() => handleSelectWorkflow(workflow)} data-state={selectedWorkflow?.name === workflow.name ? "selected" : undefined}>
                                                    <TableCell>
                                                        <Button
                                                            testid={`workflow-name-${workflow.name}`}
                                                            title={workflow.displayName || workflow.name}
                                                            variant="link"
                                                            onClick={e => {
                                                                e.stopPropagation();
                                                                handleSelectWorkflow(workflow);
                                                            }}
                                                        >
                                                            {workflow.displayName || workflow.name}
                                                        </Button>
                                                    </TableCell>
                                                    <TableCell className="text-(--secondary-text-wMain)">{workflow.version || "N/A"}</TableCell>
                                                    <TableCell>
                                                        <div className="flex items-center gap-2">
                                                            <div className={`h-2 w-2 rounded-full ${STATUS_COLOUR_MAP[workflow.status ?? ""] ?? "bg-(--secondary-text-wMain)"}`}></div>
                                                            <span>{STATUS_LABEL_MAP[workflow.status ?? ""] ?? "Unknown"}</span>
                                                            <HealthProblemsIndicator problems={workflow.runtimeProblems} testIdPrefix="workflowHealth" />
                                                        </div>
                                                    </TableCell>
                                                    <TableCell className="w-12" onClick={e => e.stopPropagation()}>
                                                        <WorkflowActionsPopover
                                                            workflowId={workflowIdByName[workflow.name]}
                                                            workflowName={workflow.displayName || workflow.name}
                                                            onOpen={() => navigate(buildWorkflowNavigationUrl(workflow.name))}
                                                            canDelete={canDelete}
                                                            canEdit={canEdit}
                                                            canViewAccess={canViewAccess}
                                                            onEditInBuilder={onEditInBuilder ? () => onEditInBuilder(workflow) : undefined}
                                                        >
                                                            {MorePopoverButton}
                                                        </WorkflowActionsPopover>
                                                    </TableCell>
                                                </TableRow>
                                            ))}
                                        </TableBody>
                                    </Table>
                                </div>
                            );
                        })()}
                    </div>
                </div>
                <PaginationControls />
            </div>

            {selectedWorkflow && (
                <div className={`h-full overflow-hidden transition-[width] duration-300 ease-in-out ${isSidePanelOpen ? "w-full sm:w-[400px]" : "w-0"}`}>
                    <div className={`h-full transition-opacity duration-300 ${isSidePanelOpen ? "opacity-100 delay-100" : "pointer-events-none opacity-0"}`}>
                        <WorkflowDetailPanel workflow={selectedWorkflow} onClose={handleCloseSidePanel} showManageButton={false} />
                    </div>
                </div>
            )}
        </div>
    );
};
