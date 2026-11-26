// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Handle, type Node, type NodeProps, Position } from "@xyflow/react";

import type { WorkflowFlowNodeData } from "../utils/types";

import AgentNode from "./AgentNode";
import { EscalationAnswerHandles } from "./EscalationAnswerHandles";

const HANDLE_STYLE = { width: 1, height: 1, background: "transparent", border: "none", minWidth: 0, minHeight: 0 };

export default function AgentFlowNode({ data, selected }: NodeProps<Node<WorkflowFlowNodeData>>) {
    const isHighlighted = data.highlightedNodeIds?.has(data.layoutNode.id) ?? false;
    return (
        <>
            <Handle type="target" position={Position.Top} id="top" style={HANDLE_STYLE} />
            <AgentNode node={data.layoutNode} isSelected={selected} isHighlighted={isHighlighted} />
            <Handle type="source" position={Position.Bottom} id="bottom" style={HANDLE_STYLE} />
            <EscalationAnswerHandles type="source" />
        </>
    );
}
