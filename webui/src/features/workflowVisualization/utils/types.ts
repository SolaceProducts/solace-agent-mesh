// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { WorkflowNodeConfig } from "@/utils/agentUtils";
import type { DecisionBranchKind, DecisionQuestion } from "@/utils/decisionNode";

export type WorkflowVisualNodeType = "start" | "end" | "agent" | "workflow" | "tool" | "switch" | "decision" | "map" | "loop" | "condition";

export interface LayoutNode {
    id: string;
    type: WorkflowVisualNodeType;
    data: {
        label: string;
        agentName?: string;
        workflowName?: string;
        toolName?: string;
        connectorName?: string;
        condition?: string;
        cases?: Array<{ condition: string; node: string }>;
        defaultCase?: string;
        items?: string;
        maxIterations?: number;
        delay?: string;
        childNodeId?: string;
        questions?: DecisionQuestion[];
        routeOn?: string;
        conditionLabel?: string;
        switchNodeId?: string;
        targetNodeId?: string;
        isDefaultCase?: boolean;
        caseNumber?: number;
        branchKind?: DecisionBranchKind;
        originalConfig?: WorkflowNodeConfig;
        icons?: Array<{ name: string; url?: string }>;
    };
    x: number;
    y: number;
    width: number;
    height: number;
    children: LayoutNode[];
    branches?: Array<{
        label: string;
        isDefault: boolean;
        nodes: LayoutNode[];
    }>;
    isCollapsed?: boolean;
    level?: number;
}

export interface Edge {
    id: string;
    source: string;
    target: string;
    sourceX: number;
    sourceY: number;
    targetX: number;
    targetY: number;
    label?: string;
    isStraight?: boolean;
    escalationAnswer?: boolean;
    answerSide?: EscalationAnswerSide;
}

export interface LayoutResult {
    nodes: LayoutNode[];
    edges: Edge[];
    totalWidth: number;
    totalHeight: number;
}

export interface NodeProps {
    node: LayoutNode;
    isSelected?: boolean;
    isHighlighted?: boolean;
    onClick?: (node: LayoutNode) => void;
    onExpand?: (nodeId: string) => void;
    onCollapse?: (nodeId: string) => void;
    onHighlightNodes?: (nodeIds: string[]) => void;
    knownNodeIds?: Set<string>;
    currentWorkflowName?: string;
    parentPath?: string[];
}

export type EscalationAnswerSide = "left" | "right";
export const ESCALATION_ANSWER_SIDES: readonly EscalationAnswerSide[] = ["left", "right"];

export function escalationAnswerHandles(side: EscalationAnswerSide): { sourceHandle: string; targetHandle: string } {
    return { sourceHandle: `answer-out-${side}`, targetHandle: `answer-in-${side}` };
}

export function escalationAnswerLabelProps(labelX: number, labelY: number) {
    return {
        label: "answer",
        labelX,
        labelY,
        labelStyle: { fill: "var(--warning-wMain)", fontSize: 12 },
        labelShowBg: true,
        labelBgStyle: { fill: "var(--background-w10)" },
        labelBgPadding: [4, 2] as [number, number],
        labelBgBorderRadius: 4,
    };
}

export const NODE_HIGHLIGHT_CLASSES = "ring-2 ring-(--warning-wMain)";

export const NODE_BASE_STYLES = {
    RECTANGULAR: "card-surface group relative flex cursor-pointer items-center justify-between rounded bg-(--background-w10) px-4 py-3 transition-all duration-200 ease-in-out card-surface-hover",
    RECTANGULAR_COMPACT: "card-surface group relative flex cursor-pointer items-center justify-between rounded bg-(--background-w10) px-3 py-2 transition-all duration-200 card-surface-hover",
    PILL: "card-surface flex items-center justify-center gap-2 rounded-full bg-(--primary-w10) px-4 py-2",
    CONTAINER_HEADER: "card-surface group relative mx-auto w-fit cursor-pointer rounded bg-(--background-w10) transition-all duration-200 card-surface-hover",
    CONDITION_PILL: "card-surface flex cursor-pointer items-center gap-1.5 rounded-md px-2 py-1 text-xs font-medium transition-all duration-200 bg-(--background-w10) card-surface-hover",
    SWITCH: "card-surface group relative cursor-pointer rounded bg-(--background-w10) transition-all duration-200 card-surface-hover",
} as const;

export const NODE_ID_BADGE_CLASSES =
    "absolute -bottom-2 left-1/2 -translate-x-1/2 rounded bg-(--secondary-w80) px-2 py-0.5 font-mono text-xs text-(--secondary-w10) opacity-0 transition-opacity duration-[750ms] ease-in group-hover:opacity-100 group-hover:duration-75 group-hover:ease-out";

export const NODE_SELECTED_CLASS = "!border-(--accent-n2-wMain)";

export const NODE_SELECTED_CLASS_COMPACT = "!border-(--accent-n2-wMain)";

export interface WorkflowFlowNodeData extends Record<string, unknown> {
    layoutNode: LayoutNode;
    onNodeClick?: (node: LayoutNode) => void;
    onExpand?: (nodeId: string) => void;
    onCollapse?: (nodeId: string) => void;
    onHighlightNodes?: (nodeIds: string[]) => void;
    selectedNodeId?: string | null;
    highlightedNodeIds?: Set<string>;
    knownNodeIds?: Set<string>;
    currentWorkflowName?: string;
    parentPath?: string[];
}

export const LAYOUT_CONSTANTS = {
    NODE_WIDTHS: {
        START: 100,
        END: 100,
        AGENT: 280,
        WORKFLOW: 280,
        TOOL: 280,
        SWITCH_COLLAPSED: 280,
        DECISION: 280,
        MAP_MIN: 320,
        LOOP_MIN: 320,
        CONDITION_PILL: 80,
    },
    NODE_HEIGHTS: {
        PILL: 40,
        AGENT: 56,
        SWITCH_COLLAPSED: 80,
        SWITCH_CASE_ROW: 32,
        BRANCHING_HEADER: 44,
        BRANCHING_ROW: 28,
        BRANCHING_ROW_GAP: 6,
        BRANCHING_BODY_PADDING: 12,
        CONTAINER_HEADER: 44,
        CONTAINER_COLLAPSED: 80,
        CONDITION_PILL: 28,
        LOOP_CONDITION_ROW: 28,
    },
    SPACING: {
        VERTICAL: 80,
        VERTICAL_BRANCH: 100,
        HORIZONTAL: 32,
        CONTAINER_PADDING: 24,
        BRANCH_GAP: 24,
    },
    COLLAPSE_THRESHOLDS: {
        SWITCH_CASES: 3,
        CONTAINER_CHILDREN: 3,
    },
};
