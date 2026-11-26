// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { PanelRightOpen, Sparkles, Workflow } from "lucide-react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";

import { useAgentCards } from "@/api/agent-cards";
import { useWorkflowsQuery } from "@/api/workflows";

import { useChatContext, useUserContext } from "@/providers/contexts";

import { EmptyState } from "@/common/status/EmptyState";

import { getWorkflowConfig, isWorkflowAgent } from "@/utils/agentUtils";

import { Button } from "@/ui/button";

import type { PanZoomCanvasRef } from "@/features/activities/flowChart/PanZoomCanvas";
import { kindToTab } from "@/features/agents/utils/filters";
import { resolveWorkflowByName } from "@/features/workflows/resolveWorkflow";
import { toAgentCardInfo } from "@/features/workflows/toAgentCardInfo";

import { PageLayout } from "@/common";
import { type BreadcrumbItem, Header } from "@/common";
import { useIsMobile } from "@/hooks";
import type { AgentCardInfo } from "@/types";

import CanvasControls from "./CanvasControls";
import { extractNodeIdsFromConfig } from "./utils/expressionParser";
import { processWorkflowConfig } from "./utils/layoutEngine";
import type { LayoutNode } from "./utils/types";
import { buildWorkflowNavigationUrl, parseWorkflowParentPath } from "./utils/workflowNavigation";
import WorkflowDetailsSidePanel, { type WorkflowPanelView } from "./WorkflowDetailsSidePanel";
import WorkflowDiagram from "./WorkflowDiagram";
import WorkflowNodeDetailPanel from "./WorkflowNodeDetailPanel";

const DETAIL_PANEL_WIDTHS = { default: 400, min: 280, max: 800 };

interface WorkflowVisualizationPageProps {
    onEdit?: (workflow: AgentCardInfo) => void;
}

export function WorkflowVisualizationPage({ onEdit }: Readonly<WorkflowVisualizationPageProps> = {}) {
    const { workflowName } = useParams<{ workflowName: string }>();
    const [searchParams] = useSearchParams();
    const navigate = useNavigate();
    const { agents } = useChatContext();
    const { userHasCapability, CAPABILITIES } = useUserContext();
    const { isLoading: agentsLoading, error: agentsError } = useAgentCards();
    const { data: platformWorkflows = [], isLoading: workflowsLoading, error: workflowsError } = useWorkflowsQuery({ enabled: userHasCapability(CAPABILITIES.readWorkflows) });

    const parentPath = useMemo(() => parseWorkflowParentPath(searchParams.get("from")), [searchParams]);

    const [selectedNode, setSelectedNode] = useState<LayoutNode | null>(null);
    const [workflowPanelView, setWorkflowPanelView] = useState<WorkflowPanelView | null>(null);
    const [panelWidth, setPanelWidth] = useState<number>(DETAIL_PANEL_WIDTHS.default);
    const [shouldAnimate, setShouldAnimate] = useState(false);
    const isMobile = useIsMobile();
    const [highlightedNodeIds, setHighlightedNodeIds] = useState<Set<string>>(new Set());
    const [currentZoom, setCurrentZoom] = useState(1);
    const prevSelectedRef = useRef<LayoutNode | null>(null);
    const prevWorkflowPanelRef = useRef<WorkflowPanelView | null>(null);
    const containerRef = useRef<HTMLDivElement>(null);
    const canvasRef = useRef<PanZoomCanvasRef>(null);
    const isResizing = useRef(false);

    useEffect(() => {
        const nodeJustOpened = selectedNode && !prevSelectedRef.current;
        const workflowPanelJustOpened = workflowPanelView && !prevWorkflowPanelRef.current;

        prevSelectedRef.current = selectedNode;
        prevWorkflowPanelRef.current = workflowPanelView;

        if (nodeJustOpened || workflowPanelJustOpened) {
            setShouldAnimate(true);
            const timer = setTimeout(() => setShouldAnimate(false), 300);
            return () => clearTimeout(timer);
        }
    }, [selectedNode, workflowPanelView]);

    const handleResizeStart = useCallback((e: React.MouseEvent) => {
        e.preventDefault();
        isResizing.current = true;
        document.body.style.cursor = "col-resize";
        document.body.style.userSelect = "none";

        const handleMouseMove = (e: MouseEvent) => {
            if (!isResizing.current || !containerRef.current) return;
            const containerRect = containerRef.current.getBoundingClientRect();
            const newWidth = containerRect.right - e.clientX;
            setPanelWidth(Math.max(DETAIL_PANEL_WIDTHS.min, Math.min(DETAIL_PANEL_WIDTHS.max, newWidth)));
        };

        const handleMouseUp = () => {
            isResizing.current = false;
            document.body.style.cursor = "";
            document.body.style.userSelect = "";
            document.removeEventListener("mousemove", handleMouseMove);
            document.removeEventListener("mouseup", handleMouseUp);
        };

        document.addEventListener("mousemove", handleMouseMove);
        document.addEventListener("mouseup", handleMouseUp);
    }, []);

    const { workflow, config, knownWorkflows, workflowExists } = useMemo(() => {
        const workflowAgents = agents.filter(isWorkflowAgent);

        const cardFromAgents = workflowAgents.find(agent => agent.name === workflowName) ?? workflowAgents.find(agent => agent.displayName === workflowName);
        const storeMatch = workflowName ? resolveWorkflowByName(platformWorkflows, workflowName) : undefined;
        const storeCard = storeMatch ? toAgentCardInfo(storeMatch) : undefined;

        const foundWorkflow = storeCard && getWorkflowConfig(storeCard) ? storeCard : cardFromAgents;
        const workflowConfig = foundWorkflow ? getWorkflowConfig(foundWorkflow) : null;

        const knownWorkflowNames = new Set([...workflowAgents.map(w => w.name), ...platformWorkflows.map(w => w.name)]);

        return {
            workflow: foundWorkflow,
            config: workflowConfig,
            knownWorkflows: knownWorkflowNames,
            workflowExists: !!(storeMatch || cardFromAgents),
        };
    }, [agents, platformWorkflows, workflowName]);

    const knownNodeIds = useMemo(() => {
        if (!config) return new Set<string>();
        return extractNodeIdsFromConfig(config);
    }, [config]);

    const handleHighlightNodes = useCallback((nodeIds: string[]) => {
        setHighlightedNodeIds(new Set(nodeIds));
    }, []);

    const handleNodeSelect = useCallback((node: LayoutNode | null) => {
        setWorkflowPanelView(null);
        setSelectedNode(node);
    }, []);

    const handleOpenWorkflowDetails = useCallback(() => {
        setSelectedNode(null);
        setWorkflowPanelView("details");
    }, []);

    const handleCloseWorkflowPanel = useCallback(() => {
        setWorkflowPanelView(null);
    }, []);

    const handleSwitchPanelView = useCallback((view: WorkflowPanelView) => {
        setWorkflowPanelView(view);
    }, []);

    const handleTransformChange = useCallback((transform: { scale: number; x: number; y: number }) => {
        setCurrentZoom(transform.scale);
    }, []);

    const handleZoomIn = useCallback(() => {
        canvasRef.current?.zoomIn({ animated: true });
    }, []);

    const handleZoomOut = useCallback(() => {
        canvasRef.current?.zoomOut({ animated: true });
    }, []);

    const handleFitToView = useCallback(() => {
        canvasRef.current?.fitToContent({ animated: true });
    }, []);

    const handleNavigateToNode = useCallback(
        (nodeId: string) => {
            if (!config) return;

            const layout = processWorkflowConfig(config, new Set(), knownWorkflows);

            const findNode = (nodes: LayoutNode[]): LayoutNode | null => {
                for (const node of nodes) {
                    if (node.id === nodeId) return node;
                    if (node.children) {
                        const found = findNode(node.children);
                        if (found) return found;
                    }
                }
                return null;
            };

            const targetNode = findNode(layout.nodes);
            if (targetNode) {
                const centerX = targetNode.x + targetNode.width / 2;
                const centerY = targetNode.y + targetNode.height / 2;
                canvasRef.current?.panToPoint(centerX, centerY, { animated: true });
            }
        },
        [config, knownWorkflows]
    );

    const breadcrumbs: BreadcrumbItem[] = useMemo(() => {
        const items: BreadcrumbItem[] = [{ label: "Agent Mesh", onClick: () => navigate(`/agent-mesh?tab=${kindToTab("workflow")}`) }];

        parentPath.forEach((parentName, index) => {
            const workflowAgents = agents.filter(isWorkflowAgent);
            const parentWorkflow = workflowAgents.find(agent => agent.name === parentName) ?? workflowAgents.find(agent => agent.displayName === parentName);
            const displayLabel = parentWorkflow?.displayName || parentWorkflow?.name || parentName;

            const parentOfParent = parentPath.slice(index + 1);
            items.push({
                label: displayLabel,
                onClick: () => navigate(buildWorkflowNavigationUrl(parentName, parentOfParent)),
            });
        });

        items.push({
            label: workflow?.displayName || workflow?.name || workflowName || "Workflow",
        });

        return items;
    }, [navigate, parentPath, workflow, workflowName, agents]);

    if (agentsLoading || workflowsLoading) {
        return (
            <PageLayout>
                <Header
                    title={
                        <div className="flex items-center gap-2">
                            <Workflow className="h-5 w-5 text-(--brand-wMain)" />
                            <span>{workflowName || "Workflow"}</span>
                        </div>
                    }
                    breadcrumbs={breadcrumbs}
                />
                <EmptyState title="Loading..." variant="loading" />
            </PageLayout>
        );
    }

    const loadError = agentsError ?? workflowsError;
    if (loadError && (!workflow || !config)) {
        return (
            <PageLayout>
                <Header
                    title={
                        <div className="flex items-center gap-2">
                            <Workflow className="h-5 w-5 text-(--brand-wMain)" />
                            <span>{workflowName || "Workflow"}</span>
                        </div>
                    }
                    breadcrumbs={breadcrumbs}
                />
                <EmptyState variant="error" title="Error loading data" subtitle={loadError.message} />
            </PageLayout>
        );
    }

    if (workflowExists && !config) {
        return (
            <PageLayout>
                <Header
                    title={
                        <div className="flex items-center gap-2">
                            <Workflow className="h-5 w-5 text-(--brand-wMain)" />
                            <span>{workflowName || "Workflow"}</span>
                        </div>
                    }
                    breadcrumbs={breadcrumbs}
                />
                <EmptyState variant="noImage" title="No diagram to display" subtitle={`"${workflowName}" has no workflow diagram yet.`} />
            </PageLayout>
        );
    }

    if (!workflow || !config) {
        return (
            <PageLayout>
                <Header
                    title={
                        <div className="flex items-center gap-2">
                            <Workflow className="h-5 w-5 text-(--brand-wMain)" />
                            <span>{workflowName || "Workflow"}</span>
                        </div>
                    }
                    breadcrumbs={breadcrumbs}
                />
                <EmptyState variant="error" title="Workflow not found" subtitle={`Could not find a workflow named "${workflowName}"`} />
            </PageLayout>
        );
    }

    return (
        <PageLayout>
            <Header
                title={
                    <div className="flex items-center gap-2">
                        <Workflow className="h-5 w-5 text-(--brand-wMain)" />
                        <span>{workflow.displayName || workflow.name}</span>
                        {config.version && <span className="rounded bg-(--secondary-w10) px-2 py-0.5 text-xs text-(--secondary-text-wMain)">v{config.version}</span>}
                    </div>
                }
                breadcrumbs={breadcrumbs}
                buttons={[
                    <Button variant="ghost" key="details" onClick={handleOpenWorkflowDetails} tooltip="Open Workflow Details">
                        <PanelRightOpen className="h-4 w-4" />
                        <span className="hidden sm:inline">Open Workflow Details</span>
                    </Button>,
                    ...(onEdit
                        ? [
                              <Button variant="outline" key="edit" onClick={() => onEdit(workflow)} tooltip="Edit">
                                  <Sparkles className="h-4 w-4" />
                                  <span className="hidden sm:inline">Edit</span>
                              </Button>,
                          ]
                        : []),
                ]}
            />

            <CanvasControls zoomLevel={currentZoom} onZoomIn={handleZoomIn} onZoomOut={handleZoomOut} onFitToView={handleFitToView} minZoom={0.25} maxZoom={2} />

            <div ref={containerRef} className="relative min-h-0 flex-1">
                <WorkflowDiagram
                    config={config}
                    knownWorkflows={knownWorkflows}
                    onNodeSelect={handleNodeSelect}
                    selectedNodeId={selectedNode?.id ?? null}
                    highlightedNodeIds={highlightedNodeIds}
                    onHighlightNodes={handleHighlightNodes}
                    knownNodeIds={knownNodeIds}
                    onTransformChange={handleTransformChange}
                    canvasRef={canvasRef}
                    currentWorkflowName={workflow.name}
                    parentPath={parentPath}
                />

                {selectedNode && (
                    <div
                        className={`absolute top-4 right-4 bottom-4 z-10 overflow-hidden rounded-lg border shadow-lg ${shouldAnimate ? "animate-in slide-in-from-right duration-300" : ""}`}
                        style={{ width: isMobile ? "calc(100% - 2rem)" : panelWidth }}
                    >
                        <WorkflowNodeDetailPanel node={selectedNode} workflowConfig={config} agents={agents} onHighlightNodes={handleHighlightNodes} knownNodeIds={knownNodeIds} onNavigateToNode={handleNavigateToNode} />
                    </div>
                )}

                {workflowPanelView && (
                    <div className={`absolute top-0 right-0 bottom-0 z-10 flex ${shouldAnimate ? "animate-in slide-in-from-right duration-300" : ""}`} style={{ width: isMobile ? "100%" : panelWidth }}>
                        <div className="relative flex w-px cursor-col-resize items-center justify-center bg-(--secondary-w40) after:absolute after:inset-y-0 after:left-1/2 after:w-1 after:-translate-x-1/2" onMouseDown={handleResizeStart} />
                        <div className="min-w-0 flex-1 bg-(--background-w10)">
                            <WorkflowDetailsSidePanel workflow={workflow} config={config} view={workflowPanelView} onClose={handleCloseWorkflowPanel} onViewChange={handleSwitchPanelView} />
                        </div>
                    </div>
                )}
            </div>
        </PageLayout>
    );
}

export default WorkflowVisualizationPage;
