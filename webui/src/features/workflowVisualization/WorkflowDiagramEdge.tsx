// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { memo } from "react";
import { BaseEdge, type EdgeProps, getSmoothStepPath } from "@xyflow/react";

import { escalationAnswerLabelProps } from "./utils/types";

const WorkflowDiagramEdgeComponent: React.FC<EdgeProps> = ({ id, sourceX, sourceY, targetX, targetY, sourcePosition, targetPosition, markerEnd, data }) => {
    const edgeStyle = data?.escalationAnswer ? { strokeWidth: 2, stroke: "var(--warning-wMain)", strokeDasharray: "6 4" } : { strokeWidth: 2, stroke: "var(--secondary-w70)" };

    if (data?.isStraight) {
        const path = `M ${sourceX} ${sourceY} L ${targetX} ${targetY}`;
        return <BaseEdge id={id} path={path} style={edgeStyle} markerEnd={markerEnd} />;
    }

    const [edgePath, labelX, labelY] = getSmoothStepPath({
        sourceX,
        sourceY,
        targetX,
        targetY,
        sourcePosition,
        targetPosition,
        borderRadius: 16,
        offset: 24,
    });

    const label = data?.escalationAnswer ? escalationAnswerLabelProps(labelX, labelY) : {};
    return <BaseEdge id={id} path={edgePath} style={edgeStyle} markerEnd={markerEnd} {...label} />;
};

export const WorkflowDiagramEdge = memo(WorkflowDiagramEdgeComponent);
