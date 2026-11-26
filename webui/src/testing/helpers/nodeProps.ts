// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { NodeProps } from "@xyflow/react";

export function makeNodeProps(data: object, selected = false): NodeProps {
    return {
        id: "n",
        type: "agent",
        data: data as Record<string, unknown>,
        selected,
        dragging: false,
        zIndex: 0,
        isConnectable: false,
        positionAbsoluteX: 0,
        positionAbsoluteY: 0,
        deletable: false,
        draggable: false,
        selectable: false,
        width: 0,
        height: 0,
    } as unknown as NodeProps;
}
