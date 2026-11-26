// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { LayoutNode } from "./types";

export interface DependencyEdge {
    sourceId: string;
    targetId: string;
}

interface DagLayoutIds {
    ran: Map<string, string>;
    drawn: Map<string, string>;
}

interface GroupScan {
    dagNodes: LayoutNode[];
    startNode?: LayoutNode;
    finishNode?: LayoutNode;
}

function isPill(child: LayoutNode, label: string): boolean {
    return child.data.variant === "pill" && child.data.label === label;
}

function scanGroupChildren(group: LayoutNode): GroupScan {
    const scan: GroupScan = { dagNodes: [] };

    for (const child of group.children) {
        if (child.type === "parallelBlock") {
            scan.dagNodes.push(...child.children.filter(c => c.data.nodeId));
        } else if (isPill(child, "Start")) {
            scan.startNode = child;
        } else if (isPill(child, "Finish")) {
            scan.finishNode = child;
        } else if (child.data.nodeId) {
            scan.dagNodes.push(child);
        }
    }

    return scan;
}

function incomingEdges(node: LayoutNode, layoutIds: DagLayoutIds, startNode: LayoutNode | undefined, isDependedOn: Set<string>): DependencyEdge[] {
    if (node.data.isSkipped) {
        return skippedNodeEdges(node, layoutIds.drawn);
    }

    const deps = (node.data.dependsOn ?? []).filter(d => layoutIds.ran.has(d));
    if (deps.length === 0) {
        return startNode ? [{ sourceId: startNode.id, targetId: node.id }] : [];
    }

    const edges: DependencyEdge[] = [];
    for (const dep of deps) {
        isDependedOn.add(dep);
        edges.push({ sourceId: layoutIds.ran.get(dep)!, targetId: node.id });
    }
    return edges;
}

function skippedNodeEdges(node: LayoutNode, drawn: Map<string, string>): DependencyEdge[] {
    return (node.data.dependsOn ?? []).filter(d => drawn.has(d)).map(dep => ({ sourceId: drawn.get(dep)!, targetId: node.id }));
}

function sinkEdges(dagNodes: LayoutNode[], isDependedOn: Set<string>, finishNode: LayoutNode): DependencyEdge[] {
    return dagNodes.filter(n => !n.data.isSkipped && !isDependedOn.has(n.data.nodeId!)).map(n => ({ sourceId: n.id, targetId: finishNode.id }));
}

export function computeDependencyEdges(group: LayoutNode): DependencyEdge[] | null {
    const { dagNodes, startNode, finishNode } = scanGroupChildren(group);

    if (!dagNodes.some(n => n.data.dependsOn !== undefined)) {
        return null;
    }

    const layoutIds: DagLayoutIds = {
        ran: new Map(dagNodes.filter(n => !n.data.isSkipped).map(n => [n.data.nodeId!, n.id])),
        drawn: new Map(dagNodes.map(n => [n.data.nodeId!, n.id])),
    };
    const isDependedOn = new Set<string>();
    const edges: DependencyEdge[] = [];

    for (const node of dagNodes) {
        edges.push(...incomingEdges(node, layoutIds, startNode, isDependedOn));
    }
    if (finishNode) {
        edges.push(...sinkEdges(dagNodes, isDependedOn, finishNode));
    }

    return edges;
}
