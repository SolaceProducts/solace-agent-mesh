// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ReactNode } from "react";

import type { LayoutNode, WorkflowFlowNodeData } from "../utils/types";

import AgentNode from "./AgentNode";
import ConditionPillNode from "./ConditionPillNode";
import DecisionNode from "./DecisionNode";
import EndNode from "./EndNode";
import LoopNode from "./LoopNode";
import MapNode from "./MapNode";
import StartNode from "./StartNode";
import SwitchNode from "./SwitchNode";
import ToolNode from "./ToolNode";
import WorkflowRefNode from "./WorkflowRefNode";

export function renderWorkflowChild(child: LayoutNode, data: WorkflowFlowNodeData): ReactNode {
    const isSelected = child.id === data.selectedNodeId;
    const isHighlighted = data.highlightedNodeIds?.has(child.id) ?? false;

    const commonProps = {
        node: child,
        isSelected,
        isHighlighted,
        onClick: data.onNodeClick,
        onExpand: data.onExpand,
        onCollapse: data.onCollapse,
        onHighlightNodes: data.onHighlightNodes,
        knownNodeIds: data.knownNodeIds,
        currentWorkflowName: data.currentWorkflowName,
        parentPath: data.parentPath,
    };

    const makeRenderChildren = () => (children: LayoutNode[]) =>
        children.map(c => (
            <div key={c.id} style={{ position: "relative" }}>
                {renderWorkflowChild(c, data)}
            </div>
        ));

    switch (child.type) {
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
        case "switch":
            return <SwitchNode {...commonProps} />;
        case "decision":
            return <DecisionNode {...commonProps} />;
        case "condition":
            return <ConditionPillNode {...commonProps} />;
        case "map":
            return <MapNode {...commonProps} renderChildren={makeRenderChildren()} />;
        case "loop":
            return <LoopNode {...commonProps} renderChildren={makeRenderChildren()} />;
        default:
            return null;
    }
}
