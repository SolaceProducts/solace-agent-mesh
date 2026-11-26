// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { memo } from "react";
import type { EdgeProps } from "@xyflow/react";
import { BaseEdge } from "@xyflow/react";

function biasedBezierPath(sx: number, sy: number, tx: number, ty: number): string {
    const dx = Math.abs(tx - sx);
    const cp1x = sx + dx * 0.2;
    const cp1y = sy;
    const cp2x = tx - dx * 0.7;
    const cp2y = ty;
    return `M ${sx} ${sy} C ${cp1x} ${cp1y}, ${cp2x} ${cp2y}, ${tx} ${ty}`;
}

function PlanRefEdgeInner(props: EdgeProps) {
    const path = biasedBezierPath(props.sourceX, props.sourceY, props.targetX, props.targetY);

    return <BaseEdge id={props.id} path={path} style={{ stroke: "var(--secondary-w70)", strokeWidth: 1, strokeDasharray: "6 4", opacity: 0.3 }} />;
}

function DependencyEdgeInner(props: EdgeProps) {
    const selectedNodeId = (props.data as { selectedNodeId?: string | null } | undefined)?.selectedNodeId;
    const isVisible = selectedNodeId != null && (props.source === selectedNodeId || props.target === selectedNodeId);

    if (!isVisible) return null;

    const path = biasedBezierPath(props.sourceX, props.sourceY, props.targetX, props.targetY);

    return (
        <BaseEdge
            id={props.id}
            path={path}
            style={{
                stroke: "var(--accent-n2-wMain)",
                strokeWidth: 1.5,
                strokeDasharray: "6 4",
                opacity: 0.7,
            }}
        />
    );
}

const PILL_TOP_OFFSET = 14;

function HierarchyEdgeInner(props: EdgeProps) {
    const { sourceX, sourceY, targetX } = props;
    const hasTargetPill = !(props.data as { noTargetPill?: boolean } | undefined)?.noTargetPill;
    const targetY = hasTargetPill ? props.targetY - PILL_TOP_OFFSET : props.targetY;
    const midY = (sourceY + targetY) / 2;
    const path = `M ${sourceX} ${sourceY} C ${sourceX} ${midY}, ${targetX} ${midY}, ${targetX} ${targetY}`;

    return (
        <BaseEdge
            id={props.id}
            path={path}
            style={{
                stroke: "var(--secondary-w70)",
                strokeWidth: 1.5,
                opacity: 0.5,
            }}
        />
    );
}

export const PlanRefEdge = memo(PlanRefEdgeInner);
export const DependencyEdge = memo(DependencyEdgeInner);
export const HierarchyEdge = memo(HierarchyEdgeInner);
