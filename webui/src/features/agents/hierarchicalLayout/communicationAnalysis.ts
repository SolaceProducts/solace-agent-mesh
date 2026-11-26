// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Edge, Node } from "@xyflow/react";
import { MarkerType } from "@xyflow/react";

import { type LayoutConfig, LayoutType } from "@/types/a2ui";
import { type AgentCardInfoFE } from "@/types/agent";
import { type AgentNodeData, type CommunicationEdgeData } from "@/types/graph";

import { calculateLayout, getDefaultLayoutConfig } from "./layoutEngine";

export function canAgentCall(callerAgent: AgentCardInfoFE, targetAgentName: string): boolean {
    return !!callerAgent.peerAgents?.find(peerAgentName => peerAgentName === targetAgentName);
}

function analyzeNodeSpatialRelationship(
    sourceNode: Node<AgentNodeData>,
    targetNode: Node<AgentNodeData>
): {
    relationship: "horizontal" | "vertical" | "diagonal";
    preferredHandles: {
        sourceHandle: string;
        targetHandle: string;
    };
} {
    const dx = targetNode.position.x - sourceNode.position.x;
    const dy = targetNode.position.y - sourceNode.position.y;

    const verticalThreshold = 50;
    const horizontalThreshold = 50;

    if (Math.abs(dy) <= verticalThreshold) {
        return {
            relationship: "horizontal",
            preferredHandles: {
                sourceHandle: dx > 0 ? "right" : "left",
                targetHandle: dx > 0 ? "left" : "right",
            },
        };
    } else if (Math.abs(dx) <= horizontalThreshold) {
        return {
            relationship: "vertical",
            preferredHandles: {
                sourceHandle: dy > 0 ? "bottom" : "top",
                targetHandle: dy > 0 ? "top" : "bottom",
            },
        };
    } else {
        return {
            relationship: "diagonal",
            preferredHandles: {
                sourceHandle: Math.abs(dx) > Math.abs(dy) ? (dx > 0 ? "right" : "left") : dy > 0 ? "bottom" : "top",
                targetHandle: Math.abs(dx) > Math.abs(dy) ? (dx > 0 ? "left" : "right") : dy > 0 ? "top" : "bottom",
            },
        };
    }
}

export function analyzeAgentCommunication(
    agents: AgentCardInfoFE[],
    layoutType: string | LayoutType = LayoutType.HIERARCHICAL,
    layoutConfig?: Partial<LayoutConfig>,
    viewport?: { width: number; height: number }
): {
    nodes: Node<AgentNodeData>[];
    edges: Edge<CommunicationEdgeData>[];
} {
    if (!agents || agents.length === 0) {
        return { nodes: [], edges: [] };
    }

    const edges: Edge<CommunicationEdgeData>[] = [];
    const communicationPairs = new Set<string>();

    agents.forEach(callerAgent => {
        agents.forEach(targetAgent => {
            if (callerAgent.name === targetAgent.name) return;

            const canCall = canAgentCall(callerAgent, targetAgent.name);
            const canBeCalledBy = canAgentCall(targetAgent, callerAgent.name);

            if (canCall || canBeCalledBy) {
                const pairKey = [callerAgent.name, targetAgent.name].sort().join("-");

                if (!communicationPairs.has(pairKey)) {
                    communicationPairs.add(pairKey);

                    if (canCall && canBeCalledBy) {
                        edges.push({
                            id: `${callerAgent.name}-${targetAgent.name}`,
                            source: callerAgent.name,
                            target: targetAgent.name,
                            type: "commEdge",
                            markerStart: { type: MarkerType.ArrowClosed },
                            markerEnd: { type: MarkerType.ArrowClosed },
                            data: { communicationType: "bidirectional" },
                        });
                    } else if (canCall) {
                        edges.push({
                            id: `${callerAgent.name}-${targetAgent.name}`,
                            source: callerAgent.name,
                            target: targetAgent.name,
                            type: "commEdge",
                            markerEnd: { type: MarkerType.ArrowClosed },
                            data: { communicationType: "unidirectional" },
                        });
                    } else {
                        edges.push({
                            id: `${targetAgent.name}-${callerAgent.name}`,
                            source: targetAgent.name,
                            target: callerAgent.name,
                            type: "commEdge",
                            markerEnd: { type: MarkerType.ArrowClosed },
                            data: { communicationType: "unidirectional" },
                        });
                    }
                }
            }
        });
    });

    const config = {
        ...getDefaultLayoutConfig(layoutType, viewport),
        ...layoutConfig,
    };

    const layoutResult = calculateLayout(agents, edges, config);

    const nodeMap = new Map<string, Node<AgentNodeData>>();
    layoutResult.nodes.forEach(node => nodeMap.set(node.id, node));

    const enhancedEdges = edges.map(edge => {
        if (edge.data?.communicationType === "bidirectional") {
            const sourceNode = nodeMap.get(edge.source);
            const targetNode = nodeMap.get(edge.target);

            if (sourceNode && targetNode) {
                const spatialAnalysis = analyzeNodeSpatialRelationship(sourceNode, targetNode);

                return {
                    ...edge,
                    sourceHandle: spatialAnalysis.preferredHandles.sourceHandle,
                    targetHandle: spatialAnalysis.preferredHandles.targetHandle,
                    data: {
                        ...edge.data,
                        sourceHandle: spatialAnalysis.preferredHandles.sourceHandle,
                        targetHandle: spatialAnalysis.preferredHandles.targetHandle,
                    },
                };
            }
        }

        return edge;
    });

    return { nodes: layoutResult.nodes, edges: enhancedEdges };
}

export function getCommunicationSummary(agents: AgentCardInfoFE[]): {
    totalAgents: number;
    totalConnections: number;
    bidirectionalConnections: number;
    unidirectionalConnections: number;
    isolatedAgents: number;
} {
    const { edges } = analyzeAgentCommunication(agents);

    const bidirectionalConnections = edges.filter(e => e.data?.communicationType === "bidirectional").length;
    const unidirectionalConnections = edges.filter(e => e.data?.communicationType === "unidirectional").length;

    const connectedAgents = new Set<string>();
    edges.forEach(edge => {
        connectedAgents.add(edge.source);
        connectedAgents.add(edge.target);
    });
    const isolatedAgents = agents.length - connectedAgents.size;

    return {
        totalAgents: agents.length,
        totalConnections: edges.length,
        bidirectionalConnections,
        unidirectionalConnections,
        isolatedAgents,
    };
}
