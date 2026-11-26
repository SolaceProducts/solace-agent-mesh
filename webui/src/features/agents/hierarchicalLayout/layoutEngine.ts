// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Edge, Node } from "@xyflow/react";

import { type LayoutConfig, LayoutType } from "@/types/a2ui";
import { type AgentCardInfoFE } from "@/types/agent";
import { type AgentNodeData, type CommunicationEdgeData } from "@/types/graph";

export interface NodeConnectionInfo {
    nodeId: string;
    incomingCount: number;
    outgoingCount: number;
    bidirectionalCount: number;
    totalConnections: number;
    connectedNodes: string[];
}

export interface LayoutResult {
    nodes: Node<AgentNodeData>[];
    connectionInfo: Map<string, NodeConnectionInfo>;
}

const NODE_DIMENSIONS = {
    width: 200,
    height: 80,
    horizontalBuffer: 40,
    verticalBuffer: 20,
    maxVerticalSpacing: 160,
};

function getMinimumSpacing(): { horizontal: number; vertical: number } {
    return {
        horizontal: NODE_DIMENSIONS.width + NODE_DIMENSIONS.horizontalBuffer,
        vertical: NODE_DIMENSIONS.height + NODE_DIMENSIONS.verticalBuffer,
    };
}

function wouldNodesOverlap(pos1: { x: number; y: number }, pos2: { x: number; y: number }): boolean {
    const dx = Math.abs(pos1.x - pos2.x);
    const dy = Math.abs(pos1.y - pos2.y);

    return dx < NODE_DIMENSIONS.width && dy < NODE_DIMENSIONS.height;
}

function getAngleBetweenPoints(point1: { x: number; y: number }, point2: { x: number; y: number }): number {
    const dx = point2.x - point1.x;
    const dy = point2.y - point1.y;
    return Math.atan2(dy, dx) * (180 / Math.PI);
}

function getDistanceBetweenPoints(point1: { x: number; y: number }, point2: { x: number; y: number }): number {
    const dx = point2.x - point1.x;
    const dy = point2.y - point1.y;
    return Math.sqrt(dx * dx + dy * dy);
}

function areEdgesLikelyToOverlap(edge1: { source: string; target: string }, edge2: { source: string; target: string }, nodePositions: Map<string, { x: number; y: number }>): boolean {
    const node1A = nodePositions.get(edge1.source);
    const node1B = nodePositions.get(edge1.target);
    const node2A = nodePositions.get(edge2.source);
    const node2B = nodePositions.get(edge2.target);

    if (!node1A || !node1B || !node2A || !node2B) return false;

    const angle1 = getAngleBetweenPoints(node1A, node1B);
    const angle2 = getAngleBetweenPoints(node2A, node2B);

    const normalizedAngle1 = Math.abs(angle1) > 90 ? Math.abs(angle1) - 180 : Math.abs(angle1);
    const normalizedAngle2 = Math.abs(angle2) > 90 ? Math.abs(angle2) - 180 : Math.abs(angle2);

    const angleDifference = Math.abs(normalizedAngle1 - normalizedAngle2);

    const areParallel = angleDifference < 20 || angleDifference > 160;

    if (!areParallel) return false;

    const midpoint1 = {
        x: (node1A.x + node1B.x) / 2,
        y: (node1A.y + node1B.y) / 2,
    };
    const midpoint2 = {
        x: (node2A.x + node2B.x) / 2,
        y: (node2A.y + node2B.y) / 2,
    };

    const midpointDistance = getDistanceBetweenPoints(midpoint1, midpoint2);

    return midpointDistance < 100;
}

function detectOverlappingEdges(edges: Edge<CommunicationEdgeData>[], nodePositions: Map<string, { x: number; y: number }>): Array<{ edges: Edge<CommunicationEdgeData>[]; severity: number }> {
    const overlapGroups: Array<{ edges: Edge<CommunicationEdgeData>[]; severity: number }> = [];
    const processedPairs = new Set<string>();

    for (let i = 0; i < edges.length; i++) {
        for (let j = i + 1; j < edges.length; j++) {
            const edge1 = edges[i];
            const edge2 = edges[j];
            const pairKey = `${edge1.id}-${edge2.id}`;

            if (processedPairs.has(pairKey)) continue;
            processedPairs.add(pairKey);

            if (areEdgesLikelyToOverlap(edge1, edge2, nodePositions)) {
                let addedToExistingGroup = false;

                for (const group of overlapGroups) {
                    if (group.edges.some(e => e.id === edge1.id || e.id === edge2.id)) {
                        if (!group.edges.some(e => e.id === edge1.id)) group.edges.push(edge1);
                        if (!group.edges.some(e => e.id === edge2.id)) group.edges.push(edge2);
                        addedToExistingGroup = true;
                        break;
                    }
                }

                if (!addedToExistingGroup) {
                    overlapGroups.push({
                        edges: [edge1, edge2],
                        severity: 1.0,
                    });
                }
            }
        }
    }

    return overlapGroups;
}

function applyStaggeredPositioning(nodes: Node<AgentNodeData>[], overlapGroups: Array<{ edges: Edge<CommunicationEdgeData>[]; severity: number }>): Node<AgentNodeData>[] {
    const adjustedNodes = [...nodes];
    const nodeMap = new Map<string, number>();

    adjustedNodes.forEach((node, index) => {
        nodeMap.set(node.id, index);
    });

    overlapGroups.forEach(group => {
        const affectedNodeIds = new Set<string>();

        group.edges.forEach(edge => {
            affectedNodeIds.add(edge.source);
            affectedNodeIds.add(edge.target);
        });

        const affectedNodes = Array.from(affectedNodeIds)
            .map(id => nodeMap.get(id))
            .filter(index => index !== undefined)
            .map(index => adjustedNodes[index!]);

        affectedNodes.forEach((node, index) => {
            const nodeIndex = nodeMap.get(node.id);
            if (nodeIndex === undefined) return;

            const offsetX = (index % 2) * 50 - 25;
            const offsetY = Math.floor(index / 2) * 50 - 50;

            adjustedNodes[nodeIndex] = {
                ...node,
                position: {
                    x: node.position.x + offsetX,
                    y: node.position.y + offsetY,
                },
            };
        });
    });

    return adjustedNodes;
}

function preventOverlaps(nodes: Node<AgentNodeData>[]): Node<AgentNodeData>[] {
    const adjustedNodes = [...nodes];
    const maxIterations = 10;

    for (let iteration = 0; iteration < maxIterations; iteration++) {
        let hasOverlap = false;

        for (let i = 0; i < adjustedNodes.length; i++) {
            for (let j = i + 1; j < adjustedNodes.length; j++) {
                const nodeA = adjustedNodes[i];
                const nodeB = adjustedNodes[j];

                if (wouldNodesOverlap(nodeA.position, nodeB.position)) {
                    hasOverlap = true;

                    const dx = nodeB.position.x - nodeA.position.x;
                    const dy = nodeB.position.y - nodeA.position.y;
                    const distance = Math.sqrt(dx * dx + dy * dy) || 1;

                    const requiredDistance = Math.max(NODE_DIMENSIONS.width, NODE_DIMENSIONS.height);
                    const separationForce = (requiredDistance - distance) / 2;

                    const separationX = (dx / distance) * separationForce;
                    const separationY = (dy / distance) * separationForce;

                    adjustedNodes[i] = {
                        ...nodeA,
                        position: {
                            x: nodeA.position.x - separationX,
                            y: nodeA.position.y - separationY,
                        },
                    };

                    adjustedNodes[j] = {
                        ...nodeB,
                        position: {
                            x: nodeB.position.x + separationX,
                            y: nodeB.position.y + separationY,
                        },
                    };
                }
            }
        }

        if (!hasOverlap) break;
    }

    return adjustedNodes;
}

export function analyzeConnections(agents: AgentCardInfoFE[], edges: Edge<CommunicationEdgeData>[]): Map<string, NodeConnectionInfo> {
    const connectionInfo = new Map<string, NodeConnectionInfo>();

    agents.forEach(agent => {
        connectionInfo.set(agent.name, {
            nodeId: agent.name,
            incomingCount: 0,
            outgoingCount: 0,
            bidirectionalCount: 0,
            totalConnections: 0,
            connectedNodes: [],
        });
    });

    edges.forEach(edge => {
        const sourceInfo = connectionInfo.get(edge.source);
        const targetInfo = connectionInfo.get(edge.target);

        if (sourceInfo && targetInfo) {
            if (edge.data?.communicationType === "bidirectional") {
                sourceInfo.bidirectionalCount++;
                targetInfo.bidirectionalCount++;
            } else {
                sourceInfo.outgoingCount++;
                targetInfo.incomingCount++;
            }

            if (!sourceInfo.connectedNodes.includes(edge.target)) {
                sourceInfo.connectedNodes.push(edge.target);
            }
            if (!targetInfo.connectedNodes.includes(edge.source)) {
                targetInfo.connectedNodes.push(edge.source);
            }
        }
    });

    connectionInfo.forEach(info => {
        info.totalConnections = info.incomingCount + info.outgoingCount + info.bidirectionalCount;
    });

    return connectionInfo;
}

function calculateHierarchicalLevels(agents: AgentCardInfoFE[], edges: Edge<CommunicationEdgeData>[]): Map<string, number> {
    const nodeLevels = new Map<string, number>();
    const adjacencyList = new Map<string, string[]>();
    const inDegree = new Map<string, number>();

    agents.forEach(agent => {
        adjacencyList.set(agent.name, []);
        inDegree.set(agent.name, 0);
        nodeLevels.set(agent.name, 0);
    });

    edges.forEach(edge => {
        if (edge.data?.communicationType !== "bidirectional") {
            const sourceList = adjacencyList.get(edge.source) || [];
            sourceList.push(edge.target);
            adjacencyList.set(edge.source, sourceList);

            const targetInDegree = inDegree.get(edge.target) || 0;
            inDegree.set(edge.target, targetInDegree + 1);
        }
    });

    const rootNodes: string[] = [];
    inDegree.forEach((degree, nodeId) => {
        if (degree === 0) {
            rootNodes.push(nodeId);
        }
    });

    if (rootNodes.length === 0) {
        const connectionInfo = analyzeConnections(agents, edges);
        let maxOutgoing = 0;
        connectionInfo.forEach(info => {
            if (info.outgoingCount > maxOutgoing) {
                maxOutgoing = info.outgoingCount;
            }
        });

        connectionInfo.forEach((info, nodeId) => {
            if (info.outgoingCount === maxOutgoing) {
                rootNodes.push(nodeId);
            }
        });
    }

    if (rootNodes.length === 0) {
        agents.forEach(agent => rootNodes.push(agent.name));
    }

    const queue: Array<{ nodeId: string; level: number }> = [];
    const visited = new Set<string>();

    rootNodes.forEach(rootId => {
        queue.push({ nodeId: rootId, level: 0 });
        nodeLevels.set(rootId, 0);
    });

    while (queue.length > 0) {
        const { nodeId, level } = queue.shift()!;

        if (visited.has(nodeId)) continue;
        visited.add(nodeId);

        const children = adjacencyList.get(nodeId) || [];
        children.forEach(childId => {
            if (!visited.has(childId)) {
                const newLevel = level + 1;
                const currentLevel = nodeLevels.get(childId) || 0;

                if (newLevel > currentLevel) {
                    nodeLevels.set(childId, newLevel);
                }

                queue.push({ nodeId: childId, level: newLevel });
            }
        });
    }

    return nodeLevels;
}

export function calculateHierarchicalLayout(agents: AgentCardInfoFE[], edges: Edge<CommunicationEdgeData>[], config: LayoutConfig): LayoutResult {
    const connectionInfo = analyzeConnections(agents, edges);
    const minSpacing = getMinimumSpacing();

    const nodeLevels = calculateHierarchicalLevels(agents, edges);

    const levelGroups = new Map<number, string[]>();
    let maxLevel = 0;

    nodeLevels.forEach((level, nodeId) => {
        maxLevel = Math.max(maxLevel, level);

        if (!levelGroups.has(level)) {
            levelGroups.set(level, []);
        }
        levelGroups.get(level)!.push(nodeId);
    });

    const layers: Array<{ nodes: string[]; level: number }> = [];
    for (let level = 0; level <= maxLevel; level++) {
        const nodesAtLevel = levelGroups.get(level) || [];
        if (nodesAtLevel.length > 0) {
            layers.push({ nodes: nodesAtLevel, level });
        }
    }

    const isolatedNodes = agents.filter(agent => connectionInfo.get(agent.name)?.totalConnections === 0).map(agent => agent.name);

    if (isolatedNodes.length > 0) {
        layers.push({ nodes: isolatedNodes, level: maxLevel + 1 });
    }

    const layerCount = layers.length;
    const availableWidth = config.viewport.width - config.padding * 2;
    const availableHeight = config.viewport.height - config.padding * 2;

    const layerHeight = Math.max(minSpacing.vertical * 2, availableHeight / Math.max(layerCount, 1));

    const nodes: Node<AgentNodeData>[] = [];

    layers.forEach((layer, layerIndex) => {
        const nodesInLayer = layer.nodes.length;

        const requiredWidth = (nodesInLayer - 1) * minSpacing.horizontal;
        const horizontalSpacing = Math.max(minSpacing.horizontal, requiredWidth <= availableWidth ? availableWidth / (nodesInLayer + 1) : minSpacing.horizontal);

        const layerWidth = (nodesInLayer - 1) * horizontalSpacing;
        const startX = config.padding + (availableWidth - layerWidth) / 2;

        layer.nodes.forEach((nodeId, nodeIndex) => {
            const agent = agents.find(a => a.name === nodeId);
            if (!agent) return;

            const x = nodesInLayer === 1 ? config.padding + availableWidth / 2 : startX + nodeIndex * horizontalSpacing;
            const y = config.padding + (layerIndex + 0.5) * layerHeight;

            nodes.push({
                id: agent.name,
                type: "agentCommNode",
                position: { x, y },
                data: {
                    label: agent.displayName || agent.name,
                    agentName: agent.name,
                    status: agent.last_seen ? "online" : "offline",
                    description: agent.description || undefined,
                },
            });
        });
    });

    let adjustedNodes = preventOverlaps(nodes);

    const nodePositions = new Map<string, { x: number; y: number }>();
    adjustedNodes.forEach(node => {
        nodePositions.set(node.id, node.position);
    });

    const overlapGroups = detectOverlappingEdges(edges, nodePositions);
    if (overlapGroups.length > 0) {
        adjustedNodes = applyStaggeredPositioning(adjustedNodes, overlapGroups);
    }

    return { nodes: adjustedNodes, connectionInfo };
}

export function calculateLayout(agents: AgentCardInfoFE[], edges: Edge<CommunicationEdgeData>[], config: LayoutConfig): LayoutResult {
    return calculateHierarchicalLayout(agents, edges, config);
}

export function getDefaultLayoutConfig(layoutType: string | LayoutType, viewport?: { width: number; height: number }): LayoutConfig {
    const defaultViewport = viewport || { width: 800, height: 600 };

    const baseConfig = {
        type: layoutType,
        spacing: {
            horizontal: 200,
            vertical: 150,
        },
        viewport: defaultViewport,
        padding: 50,
    };

    return baseConfig;
}
