// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { WorkflowNodeConfig } from "@/utils/agentUtils";

import type { VisualizerStep } from "@/types";

import type { DecisionResult } from "../../decisionResult";

export interface LayoutNode {
    id: string;
    type: "agent" | "tool" | "llm" | "user" | "switch" | "decision" | "loop" | "map" | "group" | "workflow" | "parallelBlock";
    data: {
        label: string;
        visualizerStepId?: string;
        description?: string;
        status?: string;
        variant?: "default" | "pill";
        condition?: string;
        cases?: { condition: string; node: string }[];
        defaultBranch?: string;
        selectedBranch?: string;
        selectedCaseIndex?: number;
        decision?: DecisionResult;
        escalationRoute?: string;
        waitFor?: string[];
        joinStrategy?: string;
        joinN?: number;
        maxIterations?: number;
        loopDelay?: string;
        currentIteration?: number;
        iterationIndex?: number;
        nodeId?: string;
        dependsOn?: string[];
        toolName?: string;
        workflowName?: string;
        createdArtifacts?: Array<{
            filename: string;
            version?: number;
            mimeType?: string;
            description?: string;
        }>;
        isTopNode?: boolean;
        isBottomNode?: boolean;
        isSkipped?: boolean;
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        [key: string]: any;
    };

    x: number;
    y: number;
    width: number;
    height: number;

    children: LayoutNode[];
    parallelBranches?: LayoutNode[][];

    owningTaskId?: string;
    parentTaskId?: string;
    functionCallId?: string;
}

export interface Edge {
    id: string;
    source: string;
    target: string;
    sourceX: number;
    sourceY: number;
    targetX: number;
    targetY: number;
    visualizerStepId?: string;
    label?: string;
    isError?: boolean;
    isSelected?: boolean;
}

export type WorkflowDefinitions = Record<string, WorkflowNodeConfig[]>;

export interface BuildContext {
    steps: VisualizerStep[];
    stepIndex: number;
    nodeCounter: number;
    workflowDefinitions?: WorkflowDefinitions;

    taskToNodeMap: Map<string, LayoutNode>;

    functionCallToNodeMap: Map<string, LayoutNode>;

    currentAgentNode: LayoutNode | null;

    rootNodes: LayoutNode[];

    agentNameMap: Record<string, string>;

    onUnknownAgent?: (agentName: string) => void;

    parallelContainerMap: Map<string, LayoutNode>;

    currentBranchMap: Map<string, LayoutNode[]>;

    hasTopUserNode: boolean;
    hasBottomUserNode: boolean;

    parallelPeerGroupMap: Map<string, Set<string>>;

    parallelBlockMap: Map<string, LayoutNode>;

    subWorkflowParentMap: Map<string, LayoutNode>;

    workflowFunctionCallIdMap: Map<string, string>;

    workflowParentTaskFunctionCallIds: Map<string, Set<string>>;

    eagerlyProcessedStepIds?: Set<string>;

    dagLevelsByGroup?: Map<string, Map<string, number>>;
    dagLevelBlocks?: Map<string, LayoutNode>;

    subWorkflowNodeMeta?: Map<string, { nodeId: string; dependsOn?: string[] }>;

    escalationRouting?: Map<string, EscalationRouting>;
}

export interface EscalationRouting {
    pruned: string[];
    candidates: { nodeId: string; dependsOn: string[] }[];
}

export interface LayoutResult {
    nodes: LayoutNode[];
    edges: Edge[];
    totalWidth: number;
    totalHeight: number;
}
