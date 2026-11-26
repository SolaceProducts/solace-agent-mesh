// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { type EdgeProps, getBezierPath } from "@xyflow/react";

const AgentCommEdge: React.FC<EdgeProps> = ({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, style = {}, markerEnd, markerStart }) => {
    const [edgePath] = getBezierPath({
        sourceX,
        sourceY,
        sourcePosition,
        targetX,
        targetY,
        targetPosition,
    });

    const getEdgeStyle = () => {
        const baseStyle = {
            strokeWidth: 1.5,
            stroke: "var(--secondary-text-w50)",
            ...style,
        };

        return baseStyle;
    };

    return (
        <>
            <path id={id} style={getEdgeStyle()} className="react-flow__edge-path" d={edgePath} markerStart={markerStart} markerEnd={markerEnd} />
        </>
    );
};

export { AgentCommEdge };
