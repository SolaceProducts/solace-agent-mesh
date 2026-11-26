// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Handle, type Node, type NodeProps, Position } from "@xyflow/react";

import type { LayoutNode, WorkflowFlowNodeData } from "../utils/types";

import { EscalationAnswerHandles } from "./EscalationAnswerHandles";
import LoopNode from "./LoopNode";
import { renderWorkflowChild } from "./renderWorkflowChild";

const HANDLE_STYLE = { width: 1, height: 1, background: "transparent", border: "none", minWidth: 0, minHeight: 0 };

export default function LoopFlowNode({ data, selected }: NodeProps<Node<WorkflowFlowNodeData>>) {
    const isHighlighted = data.highlightedNodeIds?.has(data.layoutNode.id) ?? false;

    const renderChildren = (children: LayoutNode[]) =>
        children.map(child => (
            <div key={child.id} style={{ position: "relative" }}>
                {renderWorkflowChild(child, data)}
            </div>
        ));

    return (
        <>
            <Handle type="target" position={Position.Top} id="top" style={HANDLE_STYLE} />
            <LoopNode node={data.layoutNode} isSelected={selected} isHighlighted={isHighlighted} onExpand={data.onExpand} onCollapse={data.onCollapse} renderChildren={renderChildren} />
            <Handle type="source" position={Position.Bottom} id="bottom" style={HANDLE_STYLE} />
            <EscalationAnswerHandles type="source" />
            <EscalationAnswerHandles type="target" />
        </>
    );
}
