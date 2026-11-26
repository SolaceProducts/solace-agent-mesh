// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useMemo, useRef, useState } from "react";
import { Eye } from "lucide-react";
import type { ComponentProps } from "react";

import { type WorkflowConfig } from "@/utils/agentUtils";

import { Button } from "@/ui/button";

import WorkflowDiagram from "@/features/workflowVisualization/WorkflowDiagram";
import WorkflowNodeDetailPanel from "@/features/workflowVisualization/WorkflowNodeDetailPanel";

import type { CanvasNodeSelection } from "./BuilderCanvas";
import { extractWorkflowConfig } from "./builderUtils";

type NodeType = ComponentProps<typeof WorkflowNodeDetailPanel>["node"];

interface WorkflowBuilderPreviewProps {
    yamlText: string;
    onAgentDrillDown?: (agentName: string) => void;
    onNodeSelected?: (node: CanvasNodeSelection | null) => void;
    onViewportChange?: (transform: { scale: number; x: number; y: number }) => void;
}

export function WorkflowBuilderPreview({ yamlText, onAgentDrillDown, onNodeSelected, onViewportChange }: WorkflowBuilderPreviewProps) {
    const [selectedNode, setSelectedNode] = useState<NodeType>(null);

    const lastClickRef = useRef<{ nodeId: string; time: number } | null>(null);

    const config: WorkflowConfig | null = useMemo(() => {
        return extractWorkflowConfig(yamlText);
    }, [yamlText]);

    const handleNodeSelect = useCallback(
        (node: NodeType) => {
            setSelectedNode(node);
            onNodeSelected?.(node ? { type: node.type, name: node.data.agentName || node.data.label || node.id } : null);

            if (node && node.type === "agent" && node.data.agentName && onAgentDrillDown) {
                const now = Date.now();
                const last = lastClickRef.current;
                if (last && last.nodeId === node.id && now - last.time < 400) {
                    onAgentDrillDown(node.data.agentName);
                    lastClickRef.current = null;
                    return;
                }
                lastClickRef.current = { nodeId: node.id, time: now };
            } else {
                lastClickRef.current = null;
            }
        },
        [onAgentDrillDown, onNodeSelected]
    );

    const handleHighlightNodes = useCallback((_nodeIds: string[]) => {}, []);

    const handleDrillDownClick = useCallback(() => {
        if (selectedNode?.type === "agent" && selectedNode.data.agentName && onAgentDrillDown) {
            onAgentDrillDown(selectedNode.data.agentName);
        }
    }, [selectedNode, onAgentDrillDown]);

    if (!config) {
        return (
            <div className="flex h-full items-center justify-center p-4">
                <p className="text-sm">Unable to parse workflow configuration.</p>
            </div>
        );
    }

    const isAgentNode = selectedNode?.type === "agent" && !!selectedNode.data.agentName;

    return (
        <div className="relative h-full w-full">
            <WorkflowDiagram config={config} onNodeSelect={handleNodeSelect} onTransformChange={onViewportChange} />

            {selectedNode && (
                <div className="absolute top-2 right-2 bottom-2 z-10 flex w-72 flex-col overflow-hidden rounded-lg border shadow-lg">
                    <div className="min-h-0 flex-1 overflow-hidden">
                        <WorkflowNodeDetailPanel node={selectedNode} workflowConfig={config} agents={[]} onHighlightNodes={handleHighlightNodes} knownNodeIds={new Set()} />
                    </div>

                    {isAgentNode && onAgentDrillDown && (
                        <Button variant="ghost" size="sm" onClick={handleDrillDownClick} className="flex h-auto items-center justify-center gap-1.5 rounded-none border-t px-3 py-2 text-xs font-medium transition-colors">
                            <Eye className="h-3.5 w-3.5" />
                            Preview Agent
                        </Button>
                    )}
                </div>
            )}
        </div>
    );
}
