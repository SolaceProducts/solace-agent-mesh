// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { FC } from "react";

import type { Edge } from "../utils/types";

interface EdgeLayerProps {
    edges: Edge[];
    width: number;
    height: number;
}

const EdgeLayer: FC<EdgeLayerProps> = ({ edges, width, height }) => {
    const generatePath = (edge: Edge): string => {
        const { sourceX, sourceY, targetX, targetY, isStraight } = edge;

        if (isStraight) {
            return `M ${sourceX} ${sourceY} L ${targetX} ${targetY}`;
        }

        const dx = Math.abs(targetX - sourceX);
        const dy = Math.abs(targetY - sourceY);
        const isHorizontal = dx > dy;

        if (isHorizontal) {
            const landingLength = 12;
            const departX = sourceX + landingLength;
            const landingX = targetX - landingLength;
            const curveWidth = Math.abs(landingX - departX);
            const controlOffset = Math.min(curveWidth * 0.5, 40);

            return `M ${sourceX} ${sourceY} L ${departX} ${sourceY} C ${departX + controlOffset} ${sourceY}, ${landingX - controlOffset} ${targetY}, ${landingX} ${targetY} L ${targetX} ${targetY}`;
        }

        const landingLength = 12;
        const landingY = targetY - landingLength;
        const controlOffset = Math.min(Math.abs(landingY - sourceY) * 0.5, 40);

        return `M ${sourceX} ${sourceY} C ${sourceX} ${sourceY + controlOffset}, ${targetX} ${landingY - controlOffset}, ${targetX} ${landingY} L ${targetX} ${targetY}`;
    };

    return (
        <svg className="pointer-events-none absolute top-0 left-0" width={width} height={height} style={{ overflow: "visible" }}>
            <defs>
                <marker id="arrowhead" markerWidth="12" markerHeight="12" refX="9" refY="6" orient="auto" markerUnits="userSpaceOnUse">
                    <path d="M 0 0 L 12 6 L 0 12 z" className="fill-(--secondary-w40)" />
                </marker>
            </defs>

            {edges.map(edge => {
                const isConditionPillEdge = edge.target.startsWith("__condition_");

                return (
                    <g key={edge.id}>
                        <path d={generatePath(edge)} className="fill-none stroke-(--secondary-w40)" strokeWidth={2} markerEnd={isConditionPillEdge ? undefined : "url(#arrowhead)"} />

                        {edge.label && (
                            <text x={(edge.sourceX + edge.targetX) / 2} y={(edge.sourceY + edge.targetY) / 2 - 8} textAnchor="middle" className="fill-(--secondary-text-wMain) text-xs">
                                {edge.label}
                            </text>
                        )}
                    </g>
                );
            })}
        </svg>
    );
};

export default EdgeLayer;
