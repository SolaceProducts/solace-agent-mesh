// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useRef } from "react";

import AgentNode from "./nodes/AgentNode";
import ConditionPillNode from "./nodes/ConditionPillNode";
import DecisionNode from "./nodes/DecisionNode";
import EndNode from "./nodes/EndNode";
import LoopNode from "./nodes/LoopNode";
import MapNode from "./nodes/MapNode";
import StartNode from "./nodes/StartNode";
import SwitchNode from "./nodes/SwitchNode";
import ToolNode from "./nodes/ToolNode";
import WorkflowRefNode from "./nodes/WorkflowRefNode";
import type { LayoutNode } from "./utils/types";

interface WorkflowNodeRendererProps {
    nodes: LayoutNode[];
    selectedNodeId?: string;
    highlightedNodeIds?: Set<string>;
    onNodeClick?: (node: LayoutNode) => void;
    onExpand?: (nodeId: string) => void;
    onCollapse?: (nodeId: string) => void;
    onHighlightNodes?: (nodeIds: string[]) => void;
    knownNodeIds?: Set<string>;
    nodeRefs: React.MutableRefObject<Map<string, HTMLDivElement>>;
    currentWorkflowName?: string;
    parentPath?: string[];
}

const WorkflowNodeRenderer: React.FC<WorkflowNodeRendererProps> = ({ nodes, selectedNodeId, highlightedNodeIds, onNodeClick, onExpand, onCollapse, onHighlightNodes, knownNodeIds, nodeRefs, currentWorkflowName, parentPath }) => {
    const mountedNodeIds = useRef<Set<string>>(new Set());

    useEffect(() => {
        const currentNodeIds = new Set(nodes.map(n => n.id));

        for (const nodeId of mountedNodeIds.current) {
            if (!currentNodeIds.has(nodeId)) {
                nodeRefs.current.delete(nodeId);
            }
        }

        mountedNodeIds.current = currentNodeIds;
    }, [nodes, nodeRefs]);

    const setNodeRef = useCallback(
        (nodeId: string, element: HTMLDivElement | null) => {
            if (element) {
                nodeRefs.current.set(nodeId, element);
            } else {
                nodeRefs.current.delete(nodeId);
            }
        },
        [nodeRefs]
    );
    const renderChildren = (children: LayoutNode[]) => {
        return children.map(child => (
            <div
                key={child.id}
                style={{
                    position: "relative",
                }}
            >
                {renderNode(child)}
            </div>
        ));
    };

    const renderNode = (node: LayoutNode) => {
        const isSelected = node.id === selectedNodeId;
        const isHighlighted = highlightedNodeIds?.has(node.id) ?? false;
        const commonProps = {
            node,
            isSelected,
            isHighlighted,
            onClick: onNodeClick,
            onExpand,
            onCollapse,
            onHighlightNodes,
            knownNodeIds,
            currentWorkflowName,
            parentPath,
        };

        switch (node.type) {
            case "start":
                return <StartNode {...commonProps} />;

            case "end":
                return <EndNode {...commonProps} />;

            case "agent":
                return <AgentNode {...commonProps} />;

            case "workflow":
                return <WorkflowRefNode {...commonProps} />;

            case "tool":
                return <ToolNode {...commonProps} />;

            case "map":
                return <MapNode {...commonProps} renderChildren={renderChildren} />;

            case "loop":
                return <LoopNode {...commonProps} renderChildren={renderChildren} />;

            case "switch":
                return <SwitchNode {...commonProps} />;

            case "decision":
                return <DecisionNode {...commonProps} />;

            case "condition":
                return <ConditionPillNode {...commonProps} />;

            default:
                return null;
        }
    };

    return (
        <>
            {nodes.map(node => (
                <div
                    key={node.id}
                    ref={el => setNodeRef(node.id, el)}
                    style={{
                        position: "absolute",
                        left: `${node.x}px`,
                        top: `${node.y}px`,
                    }}
                >
                    {renderNode(node)}
                </div>
            ))}
        </>
    );
};

export default WorkflowNodeRenderer;
