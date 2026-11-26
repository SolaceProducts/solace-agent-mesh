// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import "@xyflow/react/dist/style.css";
import "./workflowDiagram.css";

import React, { useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react";
import { type Edge as RFEdge, MarkerType, type Node, type NodeMouseHandler, type OnNodeDrag, ReactFlow, ReactFlowProvider, useNodesState, useReactFlow } from "@xyflow/react";

import type { WorkflowConfig } from "@/utils/agentUtils";

import type { PanZoomCanvasRef } from "@/features/activities/flowChart/PanZoomCanvas";

import AgentFlowNode from "./nodes/AgentFlowNode";
import ConditionPillFlowNode from "./nodes/ConditionPillFlowNode";
import DecisionFlowNode from "./nodes/DecisionFlowNode";
import EndFlowNode from "./nodes/EndFlowNode";
import LoopFlowNode from "./nodes/LoopFlowNode";
import MapFlowNode from "./nodes/MapFlowNode";
import StartFlowNode from "./nodes/StartFlowNode";
import SwitchFlowNode from "./nodes/SwitchFlowNode";
import ToolFlowNode from "./nodes/ToolFlowNode";
import WorkflowRefFlowNode from "./nodes/WorkflowRefFlowNode";
import { processWorkflowConfig } from "./utils/layoutEngine";
import { escalationAnswerHandles, type LayoutNode, type WorkflowFlowNodeData } from "./utils/types";
import { WorkflowDiagramEdge } from "./WorkflowDiagramEdge";

const nodeTypes = {
    start: StartFlowNode,
    end: EndFlowNode,
    agent: AgentFlowNode,
    workflow: WorkflowRefFlowNode,
    tool: ToolFlowNode,
    switch: SwitchFlowNode,
    decision: DecisionFlowNode,
    map: MapFlowNode,
    loop: LoopFlowNode,
    condition: ConditionPillFlowNode,
};

const EMPTY_WORKFLOWS = new Set<string>();

const edgeTypes = {
    "workflow-edge": WorkflowDiagramEdge,
};

const DEFAULT_EDGE_MARKER = {
    type: MarkerType.ArrowClosed,
    width: 16,
    height: 16,
};

interface WorkflowDiagramProps {
    config: WorkflowConfig;
    knownWorkflows?: Set<string>;
    sidePanelWidth?: number;
    onNodeSelect?: (node: LayoutNode | null) => void;
    selectedNodeId?: string | null;
    highlightedNodeIds?: Set<string>;
    onHighlightNodes?: (nodeIds: string[]) => void;
    knownNodeIds?: Set<string>;
    onTransformChange?: (transform: { scale: number; x: number; y: number }) => void;
    canvasRef?: React.RefObject<PanZoomCanvasRef | null>;
    currentWorkflowName?: string;
    parentPath?: string[];
    className?: string;
}

const WorkflowDiagram: React.FC<WorkflowDiagramProps> = props => {
    const { className, ...innerProps } = props;
    return (
        <div className={`workflow-diagram ${className ?? ""} relative h-full w-full`}>
            <ReactFlowProvider>
                <WorkflowDiagramInner {...innerProps} />
            </ReactFlowProvider>
        </div>
    );
};

type WorkflowDiagramInnerProps = Omit<WorkflowDiagramProps, "className">;

const WorkflowDiagramInner: React.FC<WorkflowDiagramInnerProps> = ({
    config,
    knownWorkflows = EMPTY_WORKFLOWS,
    onNodeSelect,
    selectedNodeId: controlledSelectedNodeId,
    highlightedNodeIds: controlledHighlightedNodeIds,
    onHighlightNodes: controlledOnHighlightNodes,
    knownNodeIds: controlledKnownNodeIds,
    onTransformChange,
    canvasRef,
    currentWorkflowName,
    parentPath,
}) => {
    const [internalSelectedNodeId, setInternalSelectedNodeId] = useState<string | null>(null);
    const selectedNodeId = controlledSelectedNodeId === undefined ? internalSelectedNodeId : controlledSelectedNodeId;
    const [collapsedNodes, setCollapsedNodes] = useState<Set<string>>(new Set());
    const [internalHighlightedNodeIds, setInternalHighlightedNodeIds] = useState<Set<string>>(new Set());

    const rf = useReactFlow();

    useImperativeHandle(
        canvasRef,
        () => ({
            fitToContent: options => {
                rf.fitView({ padding: 0.15, duration: options?.animated ? 300 : 0, maxZoom: options?.maxFitScale ?? 1.5 });
            },
            zoomIn: options => {
                rf.zoomIn({ duration: options?.animated ? 300 : 0 });
            },
            zoomOut: options => {
                rf.zoomOut({ duration: options?.animated ? 300 : 0 });
            },
            zoomTo: (scale, options) => {
                rf.zoomTo(scale, { duration: options?.animated ? 300 : 0 });
            },
            panToPoint: (x, y, options) => {
                rf.setCenter(x, y, { duration: options?.animated ? 300 : 0 });
            },
            resetTransform: () => {
                rf.fitView({ padding: 0.15 });
            },
            getTransform: () => {
                const v = rf.getViewport();
                return { scale: v.zoom, x: v.x, y: v.y };
            },
        }),
        [rf]
    );

    const layout = useMemo(() => {
        return processWorkflowConfig(config, collapsedNodes, knownWorkflows);
    }, [config, collapsedNodes, knownWorkflows]);

    const computedKnownNodeIds = useMemo(() => {
        const ids = new Set<string>();
        const collectNodeIds = (nodes: LayoutNode[]) => {
            for (const node of nodes) {
                ids.add(node.id);
                if (node.children && node.children.length > 0) {
                    collectNodeIds(node.children);
                }
            }
        };
        collectNodeIds(layout.nodes);
        return ids;
    }, [layout.nodes]);

    const knownNodeIds = controlledKnownNodeIds ?? computedKnownNodeIds;

    const highlightedNodeIds = controlledHighlightedNodeIds ?? internalHighlightedNodeIds;

    const handleHighlightNodes = useCallback(
        (nodeIds: string[]) => {
            if (controlledOnHighlightNodes) {
                controlledOnHighlightNodes(nodeIds);
            } else {
                setInternalHighlightedNodeIds(new Set(nodeIds));
            }
        },
        [controlledOnHighlightNodes]
    );

    const handleNodeClick = useCallback(
        (node: LayoutNode) => {
            setInternalSelectedNodeId(node.id);
            onNodeSelect?.(node);
        },
        [onNodeSelect]
    );

    const handleExpand = useCallback((nodeId: string) => {
        setCollapsedNodes(prev => {
            const next = new Set(prev);
            next.delete(nodeId);
            return next;
        });
    }, []);

    const handleCollapse = useCallback((nodeId: string) => {
        setCollapsedNodes(prev => {
            const next = new Set(prev);
            next.add(nodeId);
            return next;
        });
    }, []);

    const initialNodes = useMemo<Node<WorkflowFlowNodeData>[]>(() => {
        return layout.nodes.map(n => ({
            id: n.id,
            type: n.type,
            position: { x: n.x, y: n.y },
            data: {
                layoutNode: n,
                onNodeClick: handleNodeClick,
                onExpand: handleExpand,
                onCollapse: handleCollapse,
                onHighlightNodes: handleHighlightNodes,
                knownNodeIds,
                currentWorkflowName,
                parentPath,
            } as WorkflowFlowNodeData,
            width: n.width,
            height: n.height,
            selectable: true,
        }));
    }, [layout.nodes, handleNodeClick, handleExpand, handleCollapse, handleHighlightNodes, knownNodeIds, currentWorkflowName, parentPath]);

    const [nodes, setNodes, onNodesChange] = useNodesState<Node<WorkflowFlowNodeData>>(initialNodes);

    useEffect(() => {
        setNodes(initialNodes);
    }, [initialNodes, setNodes]);

    const { pillToTarget, targetToPills } = useMemo(() => {
        const p2t = new Map<string, string>();
        const t2p = new Map<string, string[]>();
        for (const n of layout.nodes) {
            if (n.type === "condition" && n.data.targetNodeId) {
                p2t.set(n.id, n.data.targetNodeId);
                const existing = t2p.get(n.data.targetNodeId);
                if (existing) {
                    existing.push(n.id);
                } else {
                    t2p.set(n.data.targetNodeId, [n.id]);
                }
            }
        }
        return { pillToTarget: p2t, targetToPills: t2p };
    }, [layout.nodes]);

    const dragStartPositions = useRef<Map<string, { x: number; y: number }>>(new Map());

    const handleNodeDragStart: OnNodeDrag<Node<WorkflowFlowNodeData>> = useCallback(
        (_event, node) => {
            dragStartPositions.current.clear();
            dragStartPositions.current.set(node.id, { x: node.position.x, y: node.position.y });

            const pairedIds: string[] = [];
            const targetId = pillToTarget.get(node.id);
            if (targetId) pairedIds.push(targetId);
            const pillIds = targetToPills.get(node.id);
            if (pillIds) pairedIds.push(...pillIds);

            for (const pairedId of pairedIds) {
                const pairedNode = nodes.find(n => n.id === pairedId);
                if (pairedNode) {
                    dragStartPositions.current.set(pairedId, { x: pairedNode.position.x, y: pairedNode.position.y });
                }
            }
        },
        [pillToTarget, targetToPills, nodes]
    );

    const handleNodeDrag: OnNodeDrag<Node<WorkflowFlowNodeData>> = useCallback(
        (_event, node) => {
            const startPos = dragStartPositions.current.get(node.id);
            if (!startPos || dragStartPositions.current.size <= 1) return;

            const dx = node.position.x - startPos.x;
            const dy = node.position.y - startPos.y;

            setNodes(prev =>
                prev.map(n => {
                    if (n.id === node.id) return n;
                    const nStart = dragStartPositions.current.get(n.id);
                    if (!nStart) return n;
                    return { ...n, position: { x: nStart.x + dx, y: nStart.y + dy } };
                })
            );
        },
        [setNodes]
    );

    const rfEdges = useMemo<RFEdge[]>(() => {
        return layout.edges.map(e => ({
            id: e.id,
            source: e.source,
            target: e.target,
            ...(e.escalationAnswer ? escalationAnswerHandles(e.answerSide ?? "right") : { sourceHandle: "bottom", targetHandle: "top" }),
            type: "workflow-edge",
            data: { isStraight: e.isStraight, escalationAnswer: e.escalationAnswer },
            markerEnd: e.target.startsWith("__condition_") ? undefined : DEFAULT_EDGE_MARKER,
        }));
    }, [layout.edges]);

    const handleRFNodeClick: NodeMouseHandler<Node<WorkflowFlowNodeData>> = useCallback(
        (_event, node) => {
            handleNodeClick(node.data.layoutNode);
        },
        [handleNodeClick]
    );

    const handlePaneClick = useCallback(() => {
        setInternalSelectedNodeId(null);
        onNodeSelect?.(null);
    }, [onNodeSelect]);

    const nodesWithState = useMemo<Node<WorkflowFlowNodeData>[]>(() => {
        return nodes.map(n => ({
            ...n,
            selected: n.id === selectedNodeId,
            data: {
                ...n.data,
                selectedNodeId,
                highlightedNodeIds,
            },
        }));
    }, [nodes, selectedNodeId, highlightedNodeIds]);

    return (
        <ReactFlow
            nodes={nodesWithState}
            edges={rfEdges}
            nodeTypes={nodeTypes}
            edgeTypes={edgeTypes}
            onNodesChange={onNodesChange}
            onNodeClick={handleRFNodeClick}
            onNodeDragStart={handleNodeDragStart}
            onNodeDrag={handleNodeDrag}
            onPaneClick={handlePaneClick}
            onMove={onTransformChange ? (_event, viewport) => onTransformChange({ scale: viewport.zoom, x: viewport.x, y: viewport.y }) : undefined}
            nodesDraggable={true}
            nodesConnectable={false}
            elementsSelectable={true}
            fitView
            fitViewOptions={{ padding: 0.15 }}
            minZoom={0.25}
            maxZoom={2}
            proOptions={{ hideAttribution: true }}
        />
    );
};

export default WorkflowDiagram;
