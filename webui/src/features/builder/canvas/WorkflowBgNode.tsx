// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { memo } from "react";
import type { NodeProps } from "@xyflow/react";

function WorkflowBgNodeInner({ data }: NodeProps) {
    const { width, height } = data as { width: number; height: number };
    return <div className="rounded border border-dashed bg-(--stateLayer-w10)" style={{ width, height }} />;
}

export const WorkflowBgNode = memo(WorkflowBgNodeInner);
