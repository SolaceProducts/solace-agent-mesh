// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { WorkflowConfig, WorkflowNodeConfig } from "@/utils/agentUtils";
import { decisionBranches, type DecisionBranchKind, decisionBranchLabel, decisionQuestions, REROUTE_ESCALATE_TARGET_TYPES, reroutesEscalation } from "@/utils/decisionNode";

import { nonEmptyString } from "./toolName";
import type { Edge, LayoutNode, LayoutResult, WorkflowVisualNodeType } from "./types";
import { LAYOUT_CONSTANTS } from "./types";

const { NODE_WIDTHS, NODE_HEIGHTS, SPACING } = LAYOUT_CONSTANTS;

interface ProcessedNode {
    id: string;
    type: WorkflowNodeConfig["type"];
    config: WorkflowNodeConfig;
    dependsOn: string[];
    dependents: string[];
    level: number;
    isContainerChild: boolean;
    containerParentId?: string;
}

export function processWorkflowConfig(config: WorkflowConfig, collapsedNodes: Set<string> = new Set(), knownWorkflows: Set<string> = new Set()): LayoutResult {
    if (!config.nodes || config.nodes.length === 0) {
        return createEmptyLayout();
    }

    const nodeMap = buildNodeMap(config.nodes);
    dropUnknownDependencies(nodeMap);

    markContainerChildren(nodeMap, config.nodes);

    buildDependencyGraph(nodeMap);
    dropRerouteWaits(nodeMap);

    copyContainerChildDependents(nodeMap);
    breakDependencyCycles(nodeMap);

    assignLevels(nodeMap);

    const levelGroups = groupByLevel(nodeMap);

    const layoutNodes = createLayoutNodes(nodeMap, levelGroups, collapsedNodes, knownWorkflows);

    const positioned = calculatePositions(layoutNodes, nodeMap);

    const withConditionPills = insertConditionPills(positioned, nodeMap);

    const edges = generateEdges(withConditionPills, nodeMap);

    const { totalWidth, totalHeight } = calculateDimensions(withConditionPills);

    return {
        nodes: withConditionPills,
        edges,
        totalWidth,
        totalHeight,
    };
}

function toolNodeData(config: WorkflowNodeConfig): Pick<LayoutNode["data"], "connectorName" | "toolName"> {
    const connectorName = nonEmptyString(config.connector);
    const toolName = (connectorName && nonEmptyString(config.tool)) || nonEmptyString(config.tool_name);
    return { connectorName, toolName };
}

function createEmptyLayout(): LayoutResult {
    const startNode: LayoutNode = {
        id: "__start__",
        type: "start",
        data: { label: "Start" },
        x: 0,
        y: 0,
        width: NODE_WIDTHS.START,
        height: NODE_HEIGHTS.PILL,
        children: [],
    };

    const endNode: LayoutNode = {
        id: "__end__",
        type: "end",
        data: { label: "End" },
        x: 0,
        y: NODE_HEIGHTS.PILL + SPACING.VERTICAL,
        width: NODE_WIDTHS.END,
        height: NODE_HEIGHTS.PILL,
        children: [],
    };

    return {
        nodes: [startNode, endNode],
        edges: [
            {
                id: "start-end",
                source: "__start__",
                target: "__end__",
                sourceX: NODE_WIDTHS.START / 2,
                sourceY: NODE_HEIGHTS.PILL,
                targetX: NODE_WIDTHS.END / 2,
                targetY: NODE_HEIGHTS.PILL + SPACING.VERTICAL,
            },
        ],
        totalWidth: NODE_WIDTHS.START,
        totalHeight: NODE_HEIGHTS.PILL * 2 + SPACING.VERTICAL,
    };
}

function authoredDependencies(value: unknown): string[] {
    return Array.isArray(value) ? [...value] : [];
}

function buildNodeMap(nodes: WorkflowNodeConfig[]): Map<string, ProcessedNode> {
    const nodeMap = new Map<string, ProcessedNode>();

    for (const node of nodes) {
        nodeMap.set(node.id, {
            id: node.id,
            type: node.type,
            config: node,
            dependsOn: authoredDependencies(node.depends_on),
            dependents: [],
            level: -1,
            isContainerChild: false,
        });
    }

    return nodeMap;
}

function markContainerChildren(nodeMap: Map<string, ProcessedNode>, nodes: WorkflowNodeConfig[]): void {
    for (const node of nodes) {
        if ((node.type === "map" || node.type === "loop") && node.node) {
            const childNode = nodeMap.get(node.node);
            const containerNode = nodeMap.get(node.id);
            if (childNode && containerNode) {
                childNode.isContainerChild = true;
                childNode.containerParentId = node.id;
            }
        }
    }
}

function copyContainerChildDependents(nodeMap: Map<string, ProcessedNode>): void {
    for (const procNode of nodeMap.values()) {
        if (procNode.isContainerChild && procNode.containerParentId) {
            const containerId = outermostContainerId(procNode.id, nodeMap);
            const containerNode = nodeMap.get(containerId);
            if (containerNode) {
                for (const depId of procNode.dependents) {
                    if (!containerNode.dependents.includes(depId)) {
                        containerNode.dependents.push(depId);
                    }
                }

                for (const depId of procNode.dependents) {
                    const depNode = nodeMap.get(depId);
                    if (depNode) {
                        const childIndex = depNode.dependsOn.indexOf(procNode.id);
                        if (childIndex !== -1) {
                            depNode.dependsOn[childIndex] = containerId;
                        }
                    }
                }
            }
        }
    }
}

function outermostContainerId(nodeId: string, nodeMap: Map<string, ProcessedNode>): string {
    const visited = new Set<string>();
    let currentId = nodeId;
    while (!visited.has(currentId)) {
        visited.add(currentId);
        const parentId = nodeMap.get(currentId)?.containerParentId;
        if (!parentId) break;
        currentId = parentId;
    }
    return currentId;
}

function routingNode(nodeId: string, nodeMap: Map<string, ProcessedNode>): ProcessedNode | undefined {
    const visited = new Set<string>();
    let current = nodeMap.get(nodeId);
    while (current && (current.config.type === "map" || current.config.type === "loop") && !visited.has(current.id)) {
        visited.add(current.id);
        const body = nodeMap.get(current.config.node ?? "");
        if (body?.containerParentId !== current.id) break;
        current = body;
    }
    return current;
}

function routingConfig(nodeId: string, nodeMap: Map<string, ProcessedNode>): WorkflowNodeConfig | undefined {
    return routingNode(nodeId, nodeMap)?.config;
}

function buildDependencyGraph(nodeMap: Map<string, ProcessedNode>): void {
    for (const node of nodeMap.values()) {
        for (const depId of node.dependsOn) {
            const depNode = nodeMap.get(depId);
            if (depNode) {
                depNode.dependents.push(node.id);
            }
        }
    }

    for (const node of nodeMap.values()) {
        for (const { targetId } of branchTargets(node.config)) {
            const targetNode = nodeMap.get(targetId);
            if (!targetNode) continue;
            if (!targetNode.dependsOn.includes(node.id)) {
                targetNode.dependsOn.push(node.id);
            }
            if (!node.dependents.includes(targetId)) {
                node.dependents.push(targetId);
            }
        }
    }
}

function dropRerouteWaits(nodeMap: Map<string, ProcessedNode>): void {
    for (const [escalateId, { routes }] of reroutingEscalations(nodeMap)) {
        const escalate = nodeMap.get(escalateId);
        for (const routeId of routes) {
            const route = nodeMap.get(routeId);
            if (!route || !escalate) continue;
            route.dependsOn = route.dependsOn.filter(id => id !== escalateId);
            escalate.dependents = escalate.dependents.filter(id => id !== routeId);
        }
    }
}

interface BranchTarget {
    targetId: string;
    label: string;
    isDefault: boolean;
    caseNumber?: number;
    branchKind?: DecisionBranchKind;
}

function branchTargets(config: WorkflowNodeConfig): BranchTarget[] {
    if (config.type === "decision") {
        return decisionBranches(config).map(branch => ({ targetId: branch.target, label: decisionBranchLabel(branch), isDefault: false, branchKind: branch.kind }));
    }
    if (config.type !== "switch") return [];

    const cases = config.cases ?? [];
    const targets: BranchTarget[] = cases.map((c, index) => ({ targetId: c.node, label: typeof c.condition === "string" ? c.condition : "", isDefault: false, caseNumber: index + 1 })).filter(t => t.targetId);
    if (config.default) {
        targets.push({ targetId: config.default, label: "default", isDefault: true, caseNumber: cases.length + 1 });
    }
    return targets;
}

function decisionRank(config: WorkflowNodeConfig): (id: string) => number {
    const branches = decisionBranches(config);
    return id => {
        const index = branches.findIndex(branch => branch.target === id);
        if (index < 0) return branches.length;
        return branches[index].kind === "escalate" ? branches.length + 1 : index;
    };
}

function switchRank(config: WorkflowNodeConfig): (id: string) => number {
    const targets = branchTargets(config);
    return id => {
        const index = targets.findIndex(target => target.targetId === id);
        return index < 0 ? targets.length : index;
    };
}

function branchRank(config: WorkflowNodeConfig): ((id: string) => number) | undefined {
    if (config.type === "decision") return decisionRank(config);
    if (config.type === "switch") return switchRank(config);
    return undefined;
}

function groupingParentIds(procNode: ProcessedNode, nodeMap: Map<string, ProcessedNode>): string[] {
    const routingParents = [...new Set(procNode.dependsOn)].filter(parentId => {
        const parent = routingConfig(parentId, nodeMap);
        return parent !== undefined && branchTargets(parent).some(branch => branch.targetId === procNode.id);
    });
    return routingParents.length === 1 ? routingParents : procNode.dependsOn;
}

function compareIds(a: string, b: string): number {
    const left = String(a);
    const right = String(b);
    if (left < right) return -1;
    return left > right ? 1 : 0;
}

function orderBranchSiblings(siblingGroups: Map<string, LayoutNode[]>, nodeMap: Map<string, ProcessedNode>): void {
    for (const [parentKey, siblings] of siblingGroups) {
        const parent = routingConfig(parentKey, nodeMap);
        const rank = parent && branchRank(parent);
        if (rank) siblings.sort((a, b) => rank(a.id) - rank(b.id));
    }
}

function dropUnknownDependencies(nodeMap: Map<string, ProcessedNode>): void {
    for (const node of nodeMap.values()) {
        node.dependsOn = node.dependsOn.filter(id => nodeMap.has(id));
    }
}

function breakDependencyCycles(nodeMap: Map<string, ProcessedNode>): void {
    const laidOut = [...nodeMap.values()].filter(node => !node.isContainerChild);
    const walkOrder = [...laidOut.filter(node => node.dependsOn.length === 0), ...laidOut.filter(node => node.dependsOn.length > 0)];
    const walk: WalkState = { onPath: new Set(), finished: new Set() };

    for (const start of walkOrder) {
        if (!walk.finished.has(start.id)) cutBackEdgesFrom(start, nodeMap, walk);
    }
}

interface WalkState {
    onPath: Set<string>;
    finished: Set<string>;
}

function cutBackEdgesFrom(start: ProcessedNode, nodeMap: Map<string, ProcessedNode>, { onPath, finished }: WalkState): void {
    const open = (node: ProcessedNode) => {
        onPath.add(node.id);
        return { node, remaining: [...node.dependents] };
    };
    const stack = [open(start)];
    while (stack.length > 0) {
        const frame = stack[stack.length - 1];
        if (frame.remaining.length === 0) {
            stack.pop();
            onPath.delete(frame.node.id);
            finished.add(frame.node.id);
            continue;
        }
        const dependentId = frame.remaining.shift()!;
        const dependent = nodeMap.get(dependentId);
        if (!dependent || dependent.isContainerChild) continue;
        if (onPath.has(dependentId)) {
            removeDependency(dependent, frame.node);
        } else if (!finished.has(dependentId)) {
            stack.push(open(dependent));
        }
    }
}

function removeDependency(dependent: ProcessedNode, dependency: ProcessedNode): void {
    dependent.dependsOn = dependent.dependsOn.filter(id => id !== dependency.id);
    dependency.dependents = dependency.dependents.filter(id => id !== dependent.id);
}

function assignLevels(nodeMap: Map<string, ProcessedNode>): void {
    const roots: string[] = [];
    for (const node of nodeMap.values()) {
        if (node.dependsOn.length === 0 && !node.isContainerChild) {
            roots.push(node.id);
            node.level = 0;
        }
    }

    const queue = [...roots];
    while (queue.length > 0) {
        const currentId = queue.shift()!;
        const current = nodeMap.get(currentId)!;

        for (const depId of current.dependents) {
            const dep = nodeMap.get(depId);
            if (dep && !dep.isContainerChild) {
                const newLevel = current.level + 1;
                if (dep.level < newLevel) {
                    dep.level = newLevel;
                    queue.push(depId);
                }
            }
        }
    }
}

function groupByLevel(nodeMap: Map<string, ProcessedNode>): Map<number, ProcessedNode[]> {
    const groups = new Map<number, ProcessedNode[]>();

    for (const node of nodeMap.values()) {
        if (!node.isContainerChild && node.level >= 0) {
            if (!groups.has(node.level)) {
                groups.set(node.level, []);
            }
            groups.get(node.level)!.push(node);
        }
    }

    return groups;
}

function createLayoutNodes(nodeMap: Map<string, ProcessedNode>, levelGroups: Map<number, ProcessedNode[]>, collapsedNodes: Set<string>, knownWorkflows: Set<string>): LayoutNode[] {
    const layoutNodes: LayoutNode[] = [];

    layoutNodes.push({
        id: "__start__",
        type: "start",
        data: { label: "Start" },
        x: 0,
        y: 0,
        width: NODE_WIDTHS.START,
        height: NODE_HEIGHTS.PILL,
        children: [],
        level: -1,
    });

    const sortedLevels = Array.from(levelGroups.keys()).sort((a, b) => a - b);

    for (const level of sortedLevels) {
        const nodesAtLevel = levelGroups.get(level)!;

        for (const procNode of nodesAtLevel) {
            const layoutNode = createLayoutNode(procNode, nodeMap, collapsedNodes, knownWorkflows);
            layoutNode.level = level;
            layoutNodes.push(layoutNode);
        }
    }

    const maxLevel = sortedLevels.length > 0 ? sortedLevels[sortedLevels.length - 1] + 1 : 0;

    layoutNodes.push({
        id: "__end__",
        type: "end",
        data: { label: "End" },
        x: 0,
        y: 0,
        width: NODE_WIDTHS.END,
        height: NODE_HEIGHTS.PILL,
        children: [],
        level: maxLevel,
    });

    return layoutNodes;
}

function bodyToDraw(container: ProcessedNode, nodeMap: Map<string, ProcessedNode>): ProcessedNode | undefined {
    const visited = new Set<string>();
    let current = nodeMap.get(container.config.node ?? "");
    const body = current;
    while (current && !visited.has(current.id)) {
        if (current.id === container.id) return undefined;
        visited.add(current.id);
        const isContainer = current.config.type === "map" || current.config.type === "loop";
        current = isContainer ? nodeMap.get(current.config.node ?? "") : undefined;
    }
    return body;
}

function createLayoutNode(procNode: ProcessedNode, nodeMap: Map<string, ProcessedNode>, collapsedNodes: Set<string>, knownWorkflows: Set<string>): LayoutNode {
    const config = procNode.config;
    const isCollapsed = collapsedNodes.has(procNode.id);

    const isWorkflowRef = config.type === "workflow" || (config.type === "agent" && !!config.agent_name && knownWorkflows.has(config.agent_name));

    let workflowName: string | undefined;
    if (config.type === "workflow") {
        workflowName = config.workflow_name;
    } else if (isWorkflowRef) {
        workflowName = config.agent_name;
    }

    const baseNode: LayoutNode = {
        id: procNode.id,
        type: getVisualNodeType(config.type, isWorkflowRef),
        data: {
            label: config.id,
            agentName: config.agent_name ?? config.workflow_name,
            workflowName: workflowName,
            originalConfig: config,
        },
        x: 0,
        y: 0,
        width: 0,
        height: 0,
        children: [],
        isCollapsed,
    };

    switch (config.type) {
        case "agent":
            baseNode.width = isWorkflowRef ? NODE_WIDTHS.WORKFLOW : NODE_WIDTHS.AGENT;
            baseNode.height = NODE_HEIGHTS.AGENT;
            break;

        case "workflow":
            baseNode.width = NODE_WIDTHS.WORKFLOW;
            baseNode.height = NODE_HEIGHTS.AGENT;
            break;

        case "tool":
            Object.assign(baseNode.data, toolNodeData(config));
            baseNode.width = NODE_WIDTHS.TOOL;
            baseNode.height = NODE_HEIGHTS.AGENT;
            break;

        case "switch": {
            baseNode.data.cases = config.cases;
            baseNode.data.defaultCase = config.default;
            baseNode.branches = createSwitchBranches(config);

            const numCases = (config.cases?.length ?? 0) + (config.default ? 1 : 0);
            baseNode.width = NODE_WIDTHS.SWITCH_COLLAPSED;
            baseNode.height = branchingNodeHeight(new Array<number>(numCases).fill(NODE_HEIGHTS.BRANCHING_ROW));
            break;
        }

        case "decision": {
            const questions = decisionQuestions(config);
            baseNode.data.questions = questions;
            baseNode.data.routeOn = config.route_on;

            baseNode.width = NODE_WIDTHS.DECISION;
            baseNode.height = branchingNodeHeight(new Array<number>(questions.length).fill(NODE_HEIGHTS.BRANCHING_ROW));
            break;
        }

        case "map":
            baseNode.data.items = config.items;
            baseNode.data.childNodeId = config.node;

            if (config.node && !isCollapsed) {
                const childProc = bodyToDraw(procNode, nodeMap);
                if (childProc) {
                    const childLayout = createLayoutNode(childProc, nodeMap, collapsedNodes, knownWorkflows);
                    baseNode.children = [childLayout];
                }
            }

            baseNode.width = isCollapsed ? NODE_WIDTHS.SWITCH_COLLAPSED : Math.max(NODE_WIDTHS.MAP_MIN, calculateContainerWidth(baseNode.children));
            baseNode.height = isCollapsed ? NODE_HEIGHTS.CONTAINER_HEADER : NODE_HEIGHTS.CONTAINER_HEADER + calculateContainerContentHeight(baseNode.children);
            break;

        case "loop":
            {
                baseNode.data.condition = config.condition;
                baseNode.data.maxIterations = config.max_iterations;
                baseNode.data.delay = config.delay;
                baseNode.data.childNodeId = config.node;

                if (config.node && !isCollapsed) {
                    const childProc = bodyToDraw(procNode, nodeMap);
                    if (childProc) {
                        const childLayout = createLayoutNode(childProc, nodeMap, collapsedNodes, knownWorkflows);
                        baseNode.children = [childLayout];
                    }
                }

                const hasConditionRow = !!(config.condition || config.max_iterations);
                const loopHeaderHeight = NODE_HEIGHTS.CONTAINER_HEADER + (hasConditionRow && !isCollapsed ? NODE_HEIGHTS.LOOP_CONDITION_ROW : 0);

                baseNode.width = isCollapsed ? NODE_WIDTHS.SWITCH_COLLAPSED : Math.max(NODE_WIDTHS.LOOP_MIN, calculateContainerWidth(baseNode.children));
                baseNode.height = isCollapsed ? NODE_HEIGHTS.CONTAINER_HEADER : loopHeaderHeight + calculateContainerContentHeight(baseNode.children);
            }
            break;
    }

    return baseNode;
}

function branchingNodeHeight(rowHeights: number[]): number {
    if (rowHeights.length === 0) return NODE_HEIGHTS.AGENT;
    const rowsHeight = rowHeights.reduce((sum, h) => sum + h, 0);
    return NODE_HEIGHTS.BRANCHING_HEADER + NODE_HEIGHTS.BRANCHING_BODY_PADDING + rowsHeight + (rowHeights.length - 1) * NODE_HEIGHTS.BRANCHING_ROW_GAP;
}

function getVisualNodeType(configType: WorkflowNodeConfig["type"], isWorkflow: boolean): WorkflowVisualNodeType {
    if (configType === "workflow") {
        return "workflow";
    }
    if (configType === "agent" && isWorkflow) {
        return "workflow";
    }
    return configType as WorkflowVisualNodeType;
}

function createSwitchBranches(config: WorkflowNodeConfig): LayoutNode["branches"] {
    const branches: LayoutNode["branches"] = [];

    if (config.cases) {
        for (let i = 0; i < config.cases.length; i++) {
            branches.push({
                label: `${i + 1}`,
                isDefault: false,
                nodes: [],
            });
        }
    }

    if (config.default) {
        branches.push({
            label: "default",
            isDefault: true,
            nodes: [],
        });
    }

    return branches;
}

function calculateContainerWidth(children: LayoutNode[]): number {
    if (children.length === 0) {
        return NODE_WIDTHS.MAP_MIN;
    }

    const childrenWidth = children.reduce((sum, child) => sum + child.width, 0);
    const gaps = Math.max(0, children.length - 1) * SPACING.HORIZONTAL;

    return childrenWidth + gaps + SPACING.CONTAINER_PADDING * 2;
}

function calculateContainerContentHeight(children: LayoutNode[]): number {
    if (children.length === 0) {
        return SPACING.CONTAINER_PADDING * 2;
    }

    const maxChildHeight = Math.max(...children.map(c => c.height));
    return maxChildHeight + SPACING.CONTAINER_PADDING * 2;
}

function computeBarycenter(parentIds: string[], nodeById: Map<string, LayoutNode>): number {
    if (parentIds.length === 0) return 0;

    let sum = 0;
    let count = 0;
    for (const id of parentIds) {
        const parent = nodeById.get(id);
        if (parent) {
            sum += parent.x + parent.width / 2;
            count++;
        }
    }

    return count > 0 ? sum / count : 0;
}

function calculatePositions(nodes: LayoutNode[], nodeMap: Map<string, ProcessedNode>): LayoutNode[] {
    if (nodes.length === 0) return nodes;

    const nodeById = new Map<string, LayoutNode>();
    const levelMap = new Map<number, LayoutNode[]>();

    for (const node of nodes) {
        nodeById.set(node.id, node);
        const level = node.level ?? 0;
        if (!levelMap.has(level)) {
            levelMap.set(level, []);
        }
        levelMap.get(level)!.push(node);
    }

    const sortedLevels = Array.from(levelMap.keys()).sort((a, b) => a - b);

    const levelHeights = new Map<number, number>();
    for (const level of sortedLevels) {
        const nodesAtLevel = levelMap.get(level)!;
        const maxHeight = Math.max(...nodesAtLevel.map(n => n.height));
        levelHeights.set(level, maxHeight);
    }

    const levelsAfterSwitch = new Set<number>();
    for (const node of nodes) {
        const procNode = routingNode(node.id, nodeMap);
        if (node.type === "switch" || (procNode && branchTargets(procNode.config).length > 0)) {
            levelsAfterSwitch.add((node.level ?? 0) + 1);
        }
    }

    const levelYPositions = new Map<number, number>();
    let currentY = 0;
    for (const level of sortedLevels) {
        levelYPositions.set(level, currentY);
        const levelHeight = levelHeights.get(level) ?? 0;
        const spacing = levelsAfterSwitch.has(level + 1) ? SPACING.VERTICAL_BRANCH : SPACING.VERTICAL;
        currentY += levelHeight + spacing;
    }

    const firstRegularLevel = sortedLevels.find(l => l >= 0) ?? 0;
    const firstLevelNodes = levelMap.get(firstRegularLevel) || [];
    const firstLevelWidth = firstLevelNodes.reduce((sum, n) => sum + n.width, 0) + Math.max(0, firstLevelNodes.length - 1) * SPACING.HORIZONTAL;

    const startNode = nodeById.get("__start__");
    if (startNode) {
        const startLevel = startNode.level ?? -1;
        const levelY = levelYPositions.get(startLevel) ?? 0;
        const levelHeight = levelHeights.get(startLevel) ?? startNode.height;
        startNode.x = Math.max(firstLevelWidth, startNode.width) / 2 - startNode.width / 2;
        startNode.y = levelY + (levelHeight - startNode.height) / 2;
    }

    let currentX = 0;
    for (const node of firstLevelNodes) {
        const levelY = levelYPositions.get(firstRegularLevel) ?? 0;
        const levelHeight = levelHeights.get(firstRegularLevel) ?? node.height;
        node.x = currentX;
        node.y = levelY + (levelHeight - node.height) / 2;
        currentX += node.width + SPACING.HORIZONTAL;

        if (node.children.length > 0) {
            positionContainerChildren(node);
        }
    }

    for (const level of sortedLevels) {
        if (level <= firstRegularLevel) continue;

        const nodesAtLevel = levelMap.get(level)!;
        const levelY = levelYPositions.get(level) ?? 0;
        const levelHeight = levelHeights.get(level) ?? 0;

        nodesAtLevel.sort((a, b) => {
            if (a.id === "__end__") return 1;
            if (b.id === "__end__") return -1;

            const aProc = nodeMap.get(a.id);
            const bProc = nodeMap.get(b.id);
            if (!aProc || !bProc) return 0;

            const aBarycenter = computeBarycenter(aProc.dependsOn, nodeById);
            const bBarycenter = computeBarycenter(bProc.dependsOn, nodeById);

            return aBarycenter - bBarycenter;
        });

        const siblingGroups = new Map<string, LayoutNode[]>();

        for (const node of nodesAtLevel) {
            if (node.id === "__end__") continue;

            const procNode = nodeMap.get(node.id);
            if (!procNode) continue;

            const parentKey = [...groupingParentIds(procNode, nodeMap)].sort(compareIds).join(",");
            if (!siblingGroups.has(parentKey)) {
                siblingGroups.set(parentKey, []);
            }
            siblingGroups.get(parentKey)!.push(node);
        }
        orderBranchSiblings(siblingGroups, nodeMap);

        const groupInfos: Array<{
            parentKey: string;
            siblings: LayoutNode[];
            idealCenterX: number;
            totalWidth: number;
        }> = [];

        for (const [parentKey, siblings] of siblingGroups) {
            const parentIds = parentKey.split(",").filter(id => id.length > 0);
            const parents = parentIds.map(id => nodeById.get(id)).filter((n): n is LayoutNode => n !== undefined);

            let idealCenterX: number;
            if (parents.length === 0) {
                idealCenterX = firstLevelWidth / 2;
            } else if (parents.length === 1) {
                idealCenterX = parents[0].x + parents[0].width / 2;
            } else {
                const minX = Math.min(...parents.map(p => p.x));
                const maxX = Math.max(...parents.map(p => p.x + p.width));
                idealCenterX = (minX + maxX) / 2;
            }

            const siblingsWidth = siblings.reduce((sum, s) => sum + s.width, 0);
            const gaps = Math.max(0, siblings.length - 1) * SPACING.HORIZONTAL;
            const totalWidth = siblingsWidth + gaps;

            groupInfos.push({ parentKey, siblings, idealCenterX, totalWidth });
        }

        groupInfos.sort((a, b) => a.idealCenterX - b.idealCenterX);

        let nextAvailableX = -Infinity;

        for (const groupInfo of groupInfos) {
            const { siblings, idealCenterX, totalWidth } = groupInfo;

            const idealStartX = idealCenterX - totalWidth / 2;

            const actualStartX = Math.max(idealStartX, nextAvailableX);

            let currentX = actualStartX;
            for (const node of siblings) {
                node.x = currentX;
                node.y = levelY + (levelHeight - node.height) / 2;
                currentX += node.width + SPACING.HORIZONTAL;

                if (node.children.length > 0) {
                    positionContainerChildren(node);
                }
            }

            nextAvailableX = actualStartX + totalWidth + SPACING.HORIZONTAL;
        }

        const endNode = nodesAtLevel.find(n => n.id === "__end__");
        if (endNode) {
            const endDependencies = findEndNodeDependencies(nodeMap, nodeById);
            if (endDependencies.length > 0) {
                const avgX = endDependencies.reduce((sum, dep) => sum + dep.x + dep.width / 2, 0) / endDependencies.length;
                endNode.x = avgX - endNode.width / 2;
            } else {
                endNode.x = (firstLevelWidth - endNode.width) / 2;
            }
            endNode.y = levelY + (levelHeight - endNode.height) / 2;
        }
    }

    for (const level of sortedLevels) {
        const nodesAtLevel = levelMap.get(level)!;
        if (nodesAtLevel.length <= 1) continue;

        nodesAtLevel.sort((a, b) => a.x - b.x);

        for (let i = 1; i < nodesAtLevel.length; i++) {
            const prev = nodesAtLevel[i - 1];
            const curr = nodesAtLevel[i];
            const minGap = SPACING.HORIZONTAL;
            const overlap = prev.x + prev.width + minGap - curr.x;

            if (overlap > 0) {
                curr.x += overlap;
            }
        }
    }

    let minX = Infinity;
    for (const node of nodes) {
        minX = Math.min(minX, node.x);
    }
    if (minX < 0) {
        for (const node of nodes) {
            node.x -= minX;
        }
    }

    let maxLayoutWidth = 0;
    for (const level of sortedLevels) {
        const nodesAtLevel = levelMap.get(level)!;
        if (nodesAtLevel.length === 0) continue;

        const levelMinX = Math.min(...nodesAtLevel.map(n => n.x));
        const levelMaxX = Math.max(...nodesAtLevel.map(n => n.x + n.width));
        const levelWidth = levelMaxX - levelMinX;
        maxLayoutWidth = Math.max(maxLayoutWidth, levelWidth);
    }

    for (const level of sortedLevels) {
        const nodesAtLevel = levelMap.get(level)!;
        if (nodesAtLevel.length === 0) continue;

        const levelMinX = Math.min(...nodesAtLevel.map(n => n.x));
        const levelMaxX = Math.max(...nodesAtLevel.map(n => n.x + n.width));
        const levelWidth = levelMaxX - levelMinX;
        const levelCenterX = levelMinX + levelWidth / 2;
        const targetCenterX = maxLayoutWidth / 2;
        const shiftX = targetCenterX - levelCenterX;

        for (const node of nodesAtLevel) {
            node.x += shiftX;
        }
    }

    minX = Infinity;
    for (const node of nodes) {
        minX = Math.min(minX, node.x);
    }
    if (minX < 0) {
        for (const node of nodes) {
            node.x -= minX;
        }
    }

    return nodes;
}

function insertConditionPills(nodes: LayoutNode[], nodeMap: Map<string, ProcessedNode>): LayoutNode[] {
    const result = [...nodes];
    const nodeById = new Map<string, LayoutNode>();

    for (const node of nodes) {
        nodeById.set(node.id, node);
    }

    for (const branchingNode of nodes) {
        const procNode = nodeMap.get(branchingNode.id);
        const config = routingConfig(branchingNode.id, nodeMap);
        if (!procNode || !config) continue;

        for (const branch of branchTargets(config)) {
            const targetNode = nodeById.get(branch.targetId);
            if (!targetNode || !procNode.dependents.includes(branch.targetId)) continue;

            const estimatedCharWidth = 7;
            const padding = 16;
            const pillWidth = Math.min(200, Math.max(60, branch.label.length * estimatedCharWidth + padding));
            const pillHeight = NODE_HEIGHTS.CONDITION_PILL;

            const pillGapAboveTarget = 30;
            const pillY = targetNode.y - pillHeight - pillGapAboveTarget;
            const pillX = targetNode.x + targetNode.width / 2 - pillWidth / 2;

            result.push({
                id: `__condition_${branchingNode.id}_${branch.targetId}__`,
                type: "condition",
                data: {
                    label: branch.label,
                    conditionLabel: branch.label,
                    switchNodeId: branchingNode.id,
                    targetNodeId: branch.targetId,
                    isDefaultCase: branch.isDefault,
                    condition: branch.label,
                    caseNumber: branch.caseNumber,
                    branchKind: branch.branchKind,
                },
                x: pillX,
                y: pillY,
                width: pillWidth,
                height: pillHeight,
                children: [],
            });
        }
    }

    return result;
}

function findEndNodeDependencies(nodeMap: Map<string, ProcessedNode>, nodeById: Map<string, LayoutNode>): LayoutNode[] {
    const leafNodes: LayoutNode[] = [];

    for (const procNode of nodeMap.values()) {
        if (procNode.isContainerChild) continue;

        const hasNonContainerDependents = procNode.dependents.some(depId => {
            const dep = nodeMap.get(depId);
            return dep && !dep.isContainerChild;
        });

        if (!hasNonContainerDependents) {
            const layoutNode = nodeById.get(procNode.id);
            if (layoutNode) {
                leafNodes.push(layoutNode);
            }
        }
    }

    return leafNodes;
}

function positionContainerChildren(container: LayoutNode): void {
    const padding = SPACING.CONTAINER_PADDING;
    let childX = padding;
    const childY = NODE_HEIGHTS.CONTAINER_HEADER + padding;

    for (const child of container.children) {
        child.x = childX;
        child.y = childY;
        childX += child.width + SPACING.HORIZONTAL;

        if (child.children.length > 0) {
            positionContainerChildren(child);
        }
    }
}

function generateEdges(nodes: LayoutNode[], nodeMap: Map<string, ProcessedNode>): Edge[] {
    const nodePositions = positionsById(nodes);
    const endNode = nodePositions.get("__end__");
    const switchToTargetPills = conditionPillsBySwitch(nodes);
    const edges = startEdges(nodeMap, nodePositions);
    const rerouting = reroutingEscalations(nodeMap);

    for (const procNode of nodeMap.values()) {
        const sourceNode = nodePositions.get(procNode.id);
        if (procNode.isContainerChild || !sourceNode) continue;

        if (procNode.dependents.length === 0 && !rerouting.has(procNode.id) && endNode) {
            edges.push(createEdge(procNode.id, "__end__", sourceNode, endNode));
        }
        edges.push(...dependentEdges(procNode, sourceNode, nodePositions, switchToTargetPills.get(procNode.id)));
    }

    edges.push(...escalationAnswerEdges(rerouting, nodeMap, nodePositions));
    return edges;
}

function dependentEdges(procNode: ProcessedNode, sourceNode: LayoutNode, nodePositions: Map<string, LayoutNode>, pills: Map<string, LayoutNode> | undefined): Edge[] {
    const edges: Edge[] = [];
    for (const depId of new Set(procNode.dependents)) {
        const targetNode = nodePositions.get(depId);
        if (!targetNode) continue;
        const conditionPill = pills?.get(depId);
        if (conditionPill) {
            edges.push(createEdge(procNode.id, conditionPill.id, sourceNode, conditionPill));
            edges.push(createEdge(conditionPill.id, depId, conditionPill, targetNode, true));
        } else {
            edges.push(createEdge(procNode.id, depId, sourceNode, targetNode));
        }
    }
    return edges;
}

function positionsById(nodes: LayoutNode[]): Map<string, LayoutNode> {
    const positions = new Map<string, LayoutNode>();
    const visit = (nodeList: LayoutNode[]): void => {
        for (const node of nodeList) {
            positions.set(node.id, node);
            visit(node.children);
        }
    };
    visit(nodes);
    return positions;
}

function conditionPillsBySwitch(nodes: LayoutNode[]): Map<string, Map<string, LayoutNode>> {
    const pills = new Map<string, Map<string, LayoutNode>>();
    for (const node of nodes) {
        const { switchNodeId, targetNodeId } = node.data;
        if (node.type !== "condition" || !switchNodeId || !targetNodeId) continue;
        const byTarget = pills.get(switchNodeId) ?? new Map<string, LayoutNode>();
        byTarget.set(targetNodeId, node);
        pills.set(switchNodeId, byTarget);
    }
    return pills;
}

function startEdges(nodeMap: Map<string, ProcessedNode>, nodePositions: Map<string, LayoutNode>): Edge[] {
    const startNode = nodePositions.get("__start__");
    if (!startNode) return [];
    const edges: Edge[] = [];
    for (const procNode of nodeMap.values()) {
        const targetNode = nodePositions.get(procNode.id);
        if (procNode.dependsOn.length === 0 && !procNode.isContainerChild && targetNode) {
            edges.push(createEdge("__start__", procNode.id, startNode, targetNode));
        }
    }
    return edges;
}

function escalationAnswerEdges(rerouting: ReturnType<typeof reroutingEscalations>, nodeMap: Map<string, ProcessedNode>, nodePositions: Map<string, LayoutNode>): Edge[] {
    const edges: Edge[] = [];
    for (const [escalateId, { decisionIds }] of rerouting) {
        const fromId = outermostContainerId(escalateId, nodeMap);
        for (const decisionId of decisionIds) {
            const edge = escalationAnswerEdge(fromId, outermostContainerId(decisionId, nodeMap), decisionId, nodePositions);
            if (edge) edges.push(edge);
        }
    }
    return edges;
}

function escalationAnswerEdge(fromId: string, toId: string, decisionId: string, nodePositions: Map<string, LayoutNode>): Edge | undefined {
    const sourceNode = nodePositions.get(fromId);
    const targetNode = nodePositions.get(toId);
    const insideOneContainer = fromId === toId && toId !== decisionId;
    if (!sourceNode || !targetNode || insideOneContainer) return undefined;
    const answerSide = sourceNode.x + sourceNode.width / 2 < targetNode.x + targetNode.width / 2 ? "left" : "right";
    return { ...createEdge(fromId, toId, sourceNode, targetNode), id: `${fromId}-answer-${toId}`, escalationAnswer: true, answerSide };
}

function reroutingEscalations(nodeMap: Map<string, ProcessedNode>): Map<string, { decisionIds: string[]; routes: Set<string> }> {
    const byEscalate = new Map<string, { decisionIds: string[]; routes: Set<string> }>();
    for (const { id, config } of nodeMap.values()) {
        if (config.type !== "decision" || !config.escalate || !reroutesEscalation(config)) continue;
        const escalateType = nodeMap.get(config.escalate)?.config.type;
        if (!escalateType || !REROUTE_ESCALATE_TARGET_TYPES.has(escalateType)) continue;
        const entry = byEscalate.get(config.escalate) ?? { decisionIds: [], routes: new Set<string>() };
        entry.decisionIds.push(id);
        for (const branch of decisionBranches(config)) {
            if (branch.kind !== "escalate") entry.routes.add(branch.target);
        }
        byEscalate.set(config.escalate, entry);
    }
    return byEscalate;
}

function createEdge(sourceId: string, targetId: string, source: LayoutNode, target: LayoutNode, isStraight: boolean = false): Edge {
    return {
        id: `${sourceId}-${targetId}`,
        source: sourceId,
        target: targetId,
        sourceX: source.x + source.width / 2,
        sourceY: source.y + source.height,
        targetX: target.x + target.width / 2,
        targetY: target.y,
        isStraight,
    };
}

function calculateDimensions(nodes: LayoutNode[]): { totalWidth: number; totalHeight: number } {
    if (nodes.length === 0) {
        return { totalWidth: 0, totalHeight: 0 };
    }

    let maxX = 0;
    let maxY = 0;

    const traverse = (nodeList: LayoutNode[]): void => {
        for (const node of nodeList) {
            maxX = Math.max(maxX, node.x + node.width);
            maxY = Math.max(maxY, node.y + node.height);
            if (node.children.length > 0) {
                traverse(node.children);
            }
        }
    };

    traverse(nodes);

    return {
        totalWidth: maxX + SPACING.CONTAINER_PADDING,
        totalHeight: maxY + SPACING.CONTAINER_PADDING,
    };
}
