// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { pendingAuthToolCallMonitoredTasks, pendingAuthToolCallTask } from "@/testing/data";
import type { VisualizerStep } from "@/types";

import { processTaskForVisualization } from "../../taskVisualizerProcessor";

import { processSteps } from "./layoutEngine";
import { findNodeDetails } from "./nodeDetailsHelper";
import type { LayoutNode } from "./types";

function nodeStart(id: string, nodeId: string, executionId: string): VisualizerStep {
    return {
        id,
        type: "WORKFLOW_NODE_EXECUTION_START",
        timestamp: new Date().toISOString(),
        title: `Node ${nodeId} started`,
        source: nodeId,
        target: nodeId,
        data: { workflowNodeExecutionStart: { nodeId, nodeType: "tool", toolName: "query_data_with_sql" } },
        rawEventIds: [],
        nestingLevel: 0,
        owningTaskId: executionId,
    };
}

function nodeResult(id: string, nodeId: string, executionId: string, status: "success" | "failure"): VisualizerStep {
    return {
        id,
        type: "WORKFLOW_NODE_EXECUTION_RESULT",
        timestamp: new Date().toISOString(),
        title: `Node ${nodeId} finished`,
        source: nodeId,
        target: nodeId,
        data: { workflowNodeExecutionResult: { nodeId, status } },
        rawEventIds: [],
        nestingLevel: 0,
        owningTaskId: executionId,
    };
}

function toolLayoutNode(visualizerStepId: string, nodeId: string): LayoutNode {
    return {
        id: "tool_1",
        type: "tool",
        data: { label: nodeId, nodeId, visualizerStepId },
        x: 0,
        y: 0,
        width: 0,
        height: 0,
        children: [],
    };
}

describe("findNodeDetails for workflow tool nodes", () => {
    test("pairs the start step with the next result for the same node id", () => {
        const steps = [nodeStart("start-1", "transform", "exec-1"), nodeResult("result-1", "transform", "exec-1", "success")];

        const details = findNodeDetails(toolLayoutNode("start-1", "transform"), steps);

        expect(details.nodeType).toBe("tool");
        expect(details.requestStep?.id).toBe("start-1");
        expect(details.resultStep?.id).toBe("result-1");
        expect(details.outputArtifactStep?.id).toBe("result-1");
    });

    test("a repeated node id (loop iterations) resolves its own iteration's result", () => {
        const steps = [nodeStart("start-1", "transform", "exec-1"), nodeResult("result-1", "transform", "exec-1", "success"), nodeStart("start-2", "transform", "exec-1"), nodeResult("result-2", "transform", "exec-1", "failure")];

        const details = findNodeDetails(toolLayoutNode("start-2", "transform"), steps);

        expect(details.requestStep?.id).toBe("start-2");
        expect(details.resultStep?.id).toBe("result-2");
    });

    test("a result from a different execution is not matched", () => {
        const steps = [nodeStart("start-1", "transform", "exec-1"), nodeResult("result-other", "transform", "exec-OTHER", "success")];

        const details = findNodeDetails(toolLayoutNode("start-1", "transform"), steps);

        expect(details.requestStep?.id).toBe("start-1");
        expect(details.resultStep).toBeUndefined();
    });
});

describe("findNodeDetails for a branch a decision pruned", () => {
    test("shows the skip result, since the branch never started", () => {
        const skip: VisualizerStep = {
            ...nodeResult("skip-1", "technical_agent", "exec-1", "success"),
            data: { workflowNodeExecutionResult: { nodeId: "technical_agent", status: "skipped", metadata: { skip_reason: 'not selected by decision "triage"' } } },
        };
        const placeholder: LayoutNode = { ...toolLayoutNode("skip-1", "technical_agent"), type: "agent", data: { label: "technical_agent", nodeId: "technical_agent", visualizerStepId: "skip-1", isSkipped: true } };

        const details = findNodeDetails(placeholder, [skip]);

        expect(details.nodeType).toBe("agent");
        expect(details.requestStep).toBeUndefined();
        expect(details.resultStep?.id).toBe("skip-1");
    });
});

function findNodeByType(nodes: LayoutNode[], type: LayoutNode["type"]): LayoutNode | undefined {
    for (const node of nodes) {
        if (node.type === type) return node;
        const found = findNodeByType(node.children, type);
        if (found) return found;
    }
    return undefined;
}

describe("findNodeDetails for agent tool nodes", () => {
    test("a tool that paused for sign-in resolves its final result, not the pending placeholder", () => {
        const viz = processTaskForVisualization(pendingAuthToolCallTask.events, pendingAuthToolCallMonitoredTasks, pendingAuthToolCallTask)!;
        const toolNode = findNodeByType(processSteps(viz.steps).nodes, "tool")!;

        const details = findNodeDetails(toolNode, viz.steps);

        expect(details.requestStep?.type).toBe("AGENT_TOOL_INVOCATION_START");
        expect(details.resultStep?.data.toolResult?.resultData).toMatchObject({ status: "error", message: "Report service rejected the request" });
    });
});
