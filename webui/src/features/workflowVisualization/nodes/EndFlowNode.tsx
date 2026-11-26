// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { Handle, type Node, type NodeProps, Position } from "@xyflow/react";

import type { WorkflowFlowNodeData } from "../utils/types";

import EndNode from "./EndNode";

const HANDLE_STYLE = { width: 1, height: 1, background: "transparent", border: "none", minWidth: 0, minHeight: 0 };

export default function EndFlowNode({ data }: NodeProps<Node<WorkflowFlowNodeData>>) {
    return (
        <>
            <Handle type="target" position={Position.Top} id="top" style={HANDLE_STYLE} />
            <EndNode node={data.layoutNode} />
        </>
    );
}
