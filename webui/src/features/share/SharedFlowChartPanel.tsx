// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useRef, useState } from "react";
import { Scan } from "lucide-react";

import type { VisualizerStep } from "@/types";
import { Button, Dialog, DialogContent, DialogDescription, DialogTitle, Switch, Tooltip, TooltipContent, TooltipTrigger, VisuallyHidden } from "@/ui";

import NodeDetailsCard from "../activities/flowChart/NodeDetailsCard";
import PanZoomCanvas, { type PanZoomCanvasRef } from "../activities/flowChart/PanZoomCanvas";
import { findNodeDetails, type NodeDetails } from "../activities/flowChart/utils/nodeDetailsHelper";
import type { Edge, LayoutNode } from "../activities/flowChart/utils/types";
import WorkflowRenderer from "../activities/flowChart/WorkflowRenderer";

interface SharedFlowChartPanelProps {
    processedSteps: VisualizerStep[];
    agentNameDisplayNameMap?: Record<string, string>;
}

const EMPTY_MAP: Record<string, string> = {};

const SharedFlowChartPanel: React.FC<SharedFlowChartPanelProps> = ({ processedSteps, agentNameDisplayNameMap = EMPTY_MAP }) => {
    const [highlightedStepId, setHighlightedStepId] = useState<string | null>(null);

    const [selectedNodeDetails, setSelectedNodeDetails] = useState<NodeDetails | null>(null);
    const [isDialogOpen, setIsDialogOpen] = useState(false);
    const [isDialogExpanded, setIsDialogExpanded] = useState(false);

    const [showDetail, setShowDetail] = useState(true);

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

            setSelectedNodeDetails(nodeDetails);
            setIsDialogOpen(true);
        },
        [processedSteps]
    );

    const handleEdgeClick = useCallback((edge: Edge) => {
        const stepId = edge.visualizerStepId;
        if (!stepId) return;
        setHighlightedStepId(stepId);
    }, []);

    const handleDialogClose = useCallback(() => {
        setIsDialogOpen(false);
        setSelectedNodeDetails(null);
        setIsDialogExpanded(false);
    }, []);

    const handlePaneClick = useCallback((event: React.MouseEvent) => {
        if (event.target === event.currentTarget) {
            setHighlightedStepId(null);
        }
    }, []);

    const handleRecenter = useCallback(() => {
        canvasRef.current?.fitToContent({ animated: true });
        hasUserInteracted.current = false;
    }, []);

    if (!processedSteps || processedSteps.length === 0) {
        return <div className="flex h-full items-center justify-center text-(--secondary-text-wMain)">No events to display.</div>;
    }

    return (
        <div style={{ height: "100%", width: "100%" }} className="relative bg-(--background-w20)">
            <div className="absolute top-4 right-4 z-50 flex items-center gap-3 rounded-sm border bg-(--background-w10) px-4 py-2 shadow-md">
                <Button onClick={handleRecenter} variant="ghost" size="sm" tooltip="Center Workflow">
                    <Scan className="h-4 w-4" />
                </Button>
                <div className="h-6 w-px border-l" />
                <span className="text-sm font-medium">Detail Mode</span>
                <Tooltip>
                    <TooltipTrigger asChild>
                        <Switch checked={showDetail} onCheckedChange={() => setShowDetail(!showDetail)} />
                    </TooltipTrigger>
                    <TooltipContent>{showDetail ? "Hide nested agent details" : "Show nested agent details"}</TooltipContent>
                </Tooltip>
            </div>

            <PanZoomCanvas ref={canvasRef} initialScale={1} minScale={0.1} maxScale={4} onUserInteraction={handleUserInteraction}>
                <div
                    style={{
                        minWidth: "100%",
                        minHeight: "100%",
                        padding: "40px",
                    }}
                    onClick={handlePaneClick}
                    role="presentation"
                >
                    <div style={{ width: "fit-content" }}>
                        <WorkflowRenderer processedSteps={processedSteps} agentNameMap={agentNameDisplayNameMap} selectedStepId={highlightedStepId} onNodeClick={handleNodeClick} onEdgeClick={handleEdgeClick} showDetail={showDetail} />
                    </div>
                </div>
            </PanZoomCanvas>

            <Dialog open={isDialogOpen} onOpenChange={setIsDialogOpen}>
                <DialogContent
                    className={`w-[90vw] ${isDialogExpanded ? "!max-w-[1600px]" : "!max-w-[1200px]"} flex max-h-[85vh] flex-col p-0 transition-all duration-200`}
                    onPointerDownOutside={e => e.preventDefault()}
                    onInteractOutside={e => e.preventDefault()}
                    showCloseButton
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
                </DialogContent>
            </Dialog>
        </div>
    );
};

export { SharedFlowChartPanel };
