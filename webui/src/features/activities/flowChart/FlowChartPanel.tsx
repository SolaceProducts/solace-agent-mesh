// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Scan } from "lucide-react";

import { useAgentCards } from "@/api/agent-cards";

import { useChatContext, useTaskContext } from "@/providers/contexts";

import type { VisualizerStep } from "@/types";
import { Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogTitle, Switch, Tooltip, TooltipContent, TooltipTrigger, VisuallyHidden } from "@/ui";

import NodeDetailsCard from "./NodeDetailsCard";
import PanZoomCanvas, { type PanZoomCanvasRef } from "./PanZoomCanvas";
import { findNodeDetails, type NodeDetails } from "./utils/nodeDetailsHelper";
import type { Edge, LayoutNode } from "./utils/types";
import { workflowDefinitionsOf } from "./utils/workflowDefinitions";
import WorkflowRenderer from "./WorkflowRenderer";

const refetchedAgents = new Set<string>();

interface FlowChartPanelProps {
    processedSteps: VisualizerStep[];
    isRightPanelVisible?: boolean;
    agentNameOverrides?: Record<string, string>;
}

const FlowChartPanel = ({ processedSteps, isRightPanelVisible = false, agentNameOverrides }: FlowChartPanelProps) => {
    const { highlightedStepId, setHighlightedStepId } = useTaskContext();
    const { agentNameDisplayNameMap } = useChatContext();
    const { data: agentCards, refetch: agentsRefetch } = useAgentCards();
    const workflowDefinitions = useMemo(() => workflowDefinitionsOf(agentCards), [agentCards]);

    const resolvedAgentNameMap = useMemo(() => (agentNameOverrides ? { ...agentNameDisplayNameMap, ...agentNameOverrides } : agentNameDisplayNameMap), [agentNameDisplayNameMap, agentNameOverrides]);

    const pendingUnknownAgents = useRef<string[]>([]);
    const handleUnknownAgent = useCallback((agentName: string) => {
        if (!refetchedAgents.has(agentName)) {
            refetchedAgents.add(agentName);
            pendingUnknownAgents.current.push(agentName);
        }
    }, []);

    useEffect(() => {
        if (pendingUnknownAgents.current.length === 0) return;
        const agents = pendingUnknownAgents.current.splice(0);
        console.log("[FlowChart] Unknown agents detected, triggering refetch:", agents);
        agentsRefetch();
    });

    const [selectedNodeDetails, setSelectedNodeDetails] = useState<NodeDetails | null>(null);
    const [isDialogOpen, setIsDialogOpen] = useState(false);
    const [isDialogExpanded, setIsDialogExpanded] = useState(false);

    const [showDetail, setShowDetail] = useState(true);

    const [detailAvailable, setDetailAvailable] = useState(false);

    const canvasRef = useRef<PanZoomCanvasRef>(null);

    const hasUserInteracted = useRef(false);
    const prevStepCount = useRef(processedSteps.length);

    useEffect(() => {
        if (processedSteps.length <= 1) {
            hasUserInteracted.current = false;
            prevStepCount.current = 0;
        }
    }, [processedSteps.length]);

    useEffect(() => {
        const currentCount = processedSteps.length;
        if (currentCount > prevStepCount.current && !hasUserInteracted.current) {
            setTimeout(() => {
                canvasRef.current?.fitToContent({ animated: true });
            }, 150);
        }
        prevStepCount.current = currentCount;
    }, [processedSteps.length]);

    useEffect(() => {
        if (!hasUserInteracted.current) {
            setTimeout(() => {
                canvasRef.current?.fitToContent({ animated: true });
            }, 150);
        }
    }, [showDetail]);

    useEffect(() => {
        if (!hasUserInteracted.current) {
            setTimeout(() => {
                canvasRef.current?.fitToContent({ animated: true });
            }, 150);
        }
    }, [isRightPanelVisible]);

    const handleUserInteraction = useCallback(() => {
        hasUserInteracted.current = true;
    }, []);

    const handleNodeClick = useCallback(
        (node: LayoutNode) => {
            hasUserInteracted.current = true;

            const stepId = node.data.visualizerStepId;

            const nodeDetails = findNodeDetails(node, processedSteps);

            if (stepId) {
                setHighlightedStepId(stepId);
            }

            if (!isRightPanelVisible) {
                setSelectedNodeDetails(nodeDetails);
                setIsDialogOpen(true);
            }
        },
        [processedSteps, isRightPanelVisible, setHighlightedStepId]
    );

    const handleEdgeClick = useCallback(
        (edge: Edge) => {
            const stepId = edge.visualizerStepId;
            if (!stepId) return;

            setHighlightedStepId(stepId);
        },
        [setHighlightedStepId]
    );

    const handleDialogClose = useCallback(() => {
        setIsDialogOpen(false);
        setSelectedNodeDetails(null);
        setIsDialogExpanded(false);
        setHighlightedStepId(null);
    }, [setHighlightedStepId]);

    const handlePaneClick = useCallback(
        (event: React.MouseEvent) => {
            if (event.target === event.currentTarget) {
                setHighlightedStepId(null);
            }
        },
        [setHighlightedStepId]
    );

    const handleRecenter = useCallback(() => {
        canvasRef.current?.fitToContent({ animated: true });
        hasUserInteracted.current = false;
    }, []);

    return (
        <div style={{ height: "100%", width: "100%" }} className="relative bg-(--background-w20)">
            <div className="absolute top-4 right-4 z-50 flex items-center gap-3 rounded-sm border bg-(--background-w10) px-4 py-2">
                <Button onClick={handleRecenter} variant="ghost" size="sm" tooltip="Center Workflow">
                    <Scan className="h-4 w-4" />
                </Button>

                {detailAvailable && (
                    <>
                        <div className="h-6 w-px border-l" />
                        <span className="text-sm font-medium">Detail Mode</span>
                        <Tooltip>
                            <TooltipTrigger asChild>
                                <Switch checked={showDetail} onCheckedChange={() => setShowDetail(!showDetail)} />
                            </TooltipTrigger>
                            <TooltipContent>{showDetail ? "Hide nested agent details" : "Show nested agent details"}</TooltipContent>
                        </Tooltip>
                    </>
                )}
            </div>

            <PanZoomCanvas ref={canvasRef} initialScale={1} minScale={0.1} maxScale={4} onUserInteraction={handleUserInteraction}>
                <div
                    style={{
                        minWidth: "100%",
                        minHeight: "100%",
                        padding: "40px",
                    }}
                    onClick={handlePaneClick}
                >
                    <div style={{ width: "fit-content" }}>
                        <WorkflowRenderer
                            processedSteps={processedSteps}
                            agentNameMap={resolvedAgentNameMap}
                            workflowDefinitions={workflowDefinitions}
                            selectedStepId={highlightedStepId}
                            onNodeClick={handleNodeClick}
                            onEdgeClick={handleEdgeClick}
                            showDetail={showDetail}
                            onUnknownAgent={handleUnknownAgent}
                            onDetailAvailabilityChange={setDetailAvailable}
                        />
                    </div>
                </div>
            </PanZoomCanvas>

            <Dialog open={isDialogOpen} onOpenChange={setIsDialogOpen}>
                <DialogContent
                    className={`w-[90vw] ${isDialogExpanded ? "!max-w-[1600px]" : "!max-w-[1200px]"} flex max-h-[85vh] flex-col p-0 transition-all duration-200`}
                    onPointerDownOutside={e => e.preventDefault()}
                    onInteractOutside={e => e.preventDefault()}
                >
                    <VisuallyHidden>
                        <DialogTitle>Node Details</DialogTitle>
                        <DialogDescription>Details for the selected node</DialogDescription>
                    </VisuallyHidden>
                    {selectedNodeDetails && (
                        <div className="min-h-0 flex-1 overflow-hidden">
                            <NodeDetailsCard nodeDetails={selectedNodeDetails} onClose={handleDialogClose} />
                        </div>
                    )}
                    <DialogFooter className="mt-0 flex-shrink-0 border-t border-(--secondary-w20) p-4">
                        <Button variant="outline" onClick={handleDialogClose}>
                            Close
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
};

export default FlowChartPanel;
