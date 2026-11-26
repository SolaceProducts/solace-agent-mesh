// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import {
    BASE_TIME,
    createCompletedResponseEvent,
    createRequestEvent,
    createSignalEvent,
    createTask,
    offsetTime,
    pendingAuthToolCallTask,
    REPORT_TOOL_FINAL_RESULT_TIME,
    reportToolCallStartEvents,
    reportToolResultEvent,
    reportToolSignInPlaceholderEvents,
} from "@/testing/data";
import type { A2AEventSSEPayload, VisualizerStep } from "@/types";

import { processTaskForVisualization } from "../../taskVisualizerProcessor";

import { computeDependencyEdges } from "./dependencyEdges";
import { detectCollapsibleDetail, processSteps } from "./layoutEngine";
import type { LayoutNode } from "./types";

let stepCounter = 0;

function userRequest(taskId: string, agentName: string): VisualizerStep {
    return {
        id: `vstep-user-${stepCounter++}`,
        type: "USER_REQUEST",
        timestamp: new Date().toISOString(),
        title: "User Request",
        source: "User",
        target: agentName,
        data: { text: "go" },
        rawEventIds: [],
        nestingLevel: 0,
        owningTaskId: taskId,
    };
}

function llmCall(taskId: string, agentName: string): VisualizerStep {
    return {
        id: `vstep-llmcall-${stepCounter++}`,
        type: "AGENT_LLM_CALL",
        timestamp: new Date().toISOString(),
        title: `${agentName}: LLM Call`,
        source: agentName,
        target: "LLM",
        data: { llmCall: { modelName: "mock", promptPreview: "" } },
        rawEventIds: [],
        nestingLevel: 0,
        owningTaskId: taskId,
    };
}

function toolDecisionParallel(taskId: string, agentName: string, decisions: { functionCallId: string; toolName: string }[]): VisualizerStep {
    return {
        id: `vstep-tooldecision-${stepCounter++}`,
        type: "AGENT_LLM_RESPONSE_TOOL_DECISION",
        timestamp: new Date().toISOString(),
        title: "LLM: Tool Decision (Parallel)",
        source: "LLM",
        target: agentName,
        data: {
            toolDecision: {
                decisions: decisions.map(d => ({
                    functionCallId: d.functionCallId,
                    toolName: d.toolName,
                    toolArguments: {},
                    isPeerDelegation: false,
                })),
                isParallel: decisions.length > 1,
            },
        },
        rawEventIds: [],
        nestingLevel: 0,
        owningTaskId: taskId,
    };
}

function toolInvocationStart(taskId: string, agentName: string, functionCallId: string, toolName: string, parallelGroupId: string): VisualizerStep {
    return {
        id: `vstep-toolinvokestart-${stepCounter++}`,
        type: "AGENT_TOOL_INVOCATION_START",
        timestamp: new Date().toISOString(),
        title: `${agentName}: Executing tool ${toolName}`,
        source: agentName,
        target: toolName,
        data: {
            toolInvocationStart: {
                functionCallId,
                toolName,
                toolArguments: {},
                isPeerInvocation: false,
                parallelGroupId,
            },
        },
        rawEventIds: [],
        nestingLevel: 0,
        owningTaskId: taskId,
    };
}

function toolResult(taskId: string, functionCallId: string, toolName: string): VisualizerStep {
    return {
        id: `vstep-toolresult-${stepCounter++}`,
        type: "AGENT_TOOL_EXECUTION_RESULT",
        timestamp: new Date().toISOString(),
        title: "Tool result",
        source: toolName,
        target: "Agent",
        data: { toolResult: { functionCallId, toolName, resultData: {} } },
        rawEventIds: [],
        nestingLevel: 0,
        owningTaskId: taskId,
        functionCallId,
    };
}

function agentResponse(taskId: string, agentName: string): VisualizerStep {
    return {
        id: `vstep-response-${stepCounter++}`,
        type: "AGENT_RESPONSE_TEXT",
        timestamp: new Date().toISOString(),
        title: "Agent Response",
        source: agentName,
        target: "User",
        data: { text: "done" },
        rawEventIds: [],
        nestingLevel: 0,
        owningTaskId: taskId,
    };
}

function buildPaginationFixture(numIterations: number, toolName: string): VisualizerStep[] {
    stepCounter = 0;
    const taskId = "task-1";
    const agentName = "Orchestrator";
    const steps: VisualizerStep[] = [userRequest(taskId, agentName)];

    for (let i = 0; i < numIterations; i++) {
        const fcA = `fc-${i}-a`;
        const fcB = `fc-${i}-b`;
        const groupId = `llm_batch_iter${i}`;
        steps.push(llmCall(taskId, agentName));
        steps.push(
            toolDecisionParallel(taskId, agentName, [
                { functionCallId: fcA, toolName },
                { functionCallId: fcB, toolName },
            ])
        );
        steps.push(toolInvocationStart(taskId, agentName, fcA, toolName, groupId));
        steps.push(toolInvocationStart(taskId, agentName, fcB, toolName, groupId));
        steps.push(toolResult(taskId, fcA, toolName));
        steps.push(toolResult(taskId, fcB, toolName));
    }

    steps.push(llmCall(taskId, agentName));
    steps.push(agentResponse(taskId, agentName));
    return steps;
}

function findAgent(nodes: LayoutNode[]): LayoutNode {
    const agent = nodes.find(n => n.type === "agent");
    if (!agent) throw new Error("No agent node found");
    return agent;
}

function countParallelBlocks(node: LayoutNode): number {
    let count = node.type === "parallelBlock" ? 1 : 0;
    for (const child of node.children) count += countParallelBlocks(child);
    return count;
}

describe("FlowChart layout — synthetic VisualizerStep path", () => {
    test("four iterations with distinct group IDs each get their own parallel block", () => {
        const N = 4;
        const steps = buildPaginationFixture(N, "transform_data_with_jmespath");
        const { nodes } = processSteps(steps);
        const agent = findAgent(nodes);

        const blocks = agent.children.filter(c => c.type === "parallelBlock");
        expect(blocks).toHaveLength(N);
        for (const block of blocks) expect(block.children).toHaveLength(2);
        expect(countParallelBlocks(agent)).toBe(N);
    });
});

function workflowStart(executionId: string, workflowName: string): VisualizerStep {
    return {
        id: `vstep-wfstart-${stepCounter++}`,
        type: "WORKFLOW_EXECUTION_START",
        timestamp: new Date().toISOString(),
        title: `Workflow ${workflowName} started`,
        source: workflowName,
        target: workflowName,
        data: { workflowExecutionStart: { workflowName, executionId } },
        rawEventIds: [],
        nestingLevel: 0,
        owningTaskId: executionId,
    };
}

function workflowToolNodeStart(executionId: string, nodeId: string, parallelGroupId?: string, iterationIndex?: number, dependsOn?: string[]): VisualizerStep {
    return {
        id: `vstep-wfnode-${stepCounter++}`,
        type: "WORKFLOW_NODE_EXECUTION_START",
        timestamp: new Date().toISOString(),
        title: `Node ${nodeId} started`,
        source: nodeId,
        target: nodeId,
        data: {
            workflowNodeExecutionStart: {
                nodeId,
                nodeType: "tool",
                parallelGroupId,
                iterationIndex,
                dependsOn,
                toolName: `${nodeId}_tool`,
            },
        },
        rawEventIds: [],
        nestingLevel: 0,
        owningTaskId: executionId,
    };
}

function workflowExecutionResult(executionId: string): VisualizerStep {
    return {
        id: `vstep-wfresult-${stepCounter++}`,
        type: "WORKFLOW_EXECUTION_RESULT",
        timestamp: new Date().toISOString(),
        title: "Workflow finished",
        source: "Workflow",
        target: "Workflow",
        data: { workflowExecutionResult: { status: "success" } },
        rawEventIds: [],
        nestingLevel: 0,
        owningTaskId: executionId,
    };
}

function workflowToolNodeResult(executionId: string, nodeId: string): VisualizerStep {
    return {
        id: `vstep-wfnoderesult-${stepCounter++}`,
        type: "WORKFLOW_NODE_EXECUTION_RESULT",
        timestamp: new Date().toISOString(),
        title: `Node ${nodeId} finished`,
        source: nodeId,
        target: nodeId,
        data: { workflowNodeExecutionResult: { nodeId, status: "success" } },
        rawEventIds: [],
        nestingLevel: 0,
        owningTaskId: executionId,
    };
}

describe("FlowChart layout — tool-only workflow nodes", () => {
    function buildToolWorkflowFixture(): VisualizerStep[] {
        stepCounter = 0;
        const executionId = "wf-exec-1";
        const groupId = "parallel-group-1";
        const fanOut = ["health", "agent_perf_q", "token_q", "feedback_q", "tasks_q"];

        const steps: VisualizerStep[] = [workflowStart(executionId, "WeeklyObservabilityReport")];
        fanOut.forEach((nodeId, i) => steps.push(workflowToolNodeStart(executionId, nodeId, groupId, i)));
        fanOut.forEach(nodeId => steps.push(workflowToolNodeResult(executionId, nodeId)));
        steps.push(workflowToolNodeStart(executionId, "build"));
        steps.push(workflowToolNodeResult(executionId, "build"));
        return steps;
    }

    test("tool nodes get type 'tool', not 'agent'", () => {
        const { nodes } = processSteps(buildToolWorkflowFixture());
        const group = nodes.find(n => n.type === "group");
        expect(group).toBeDefined();

        const collectTypes = (node: LayoutNode, acc: Map<string, string>) => {
            if (node.data.nodeId) acc.set(node.data.nodeId as string, node.type);
            for (const child of node.children) collectTypes(child, acc);
            return acc;
        };
        const typesByNodeId = collectTypes(group!, new Map<string, string>());

        for (const nodeId of ["health", "agent_perf_q", "token_q", "feedback_q", "tasks_q", "build"]) {
            expect(typesByNodeId.get(nodeId)).toBe("tool");
        }
    });

    test("tool nodes sharing a parallel_group_id land in one parallel block; serial nodes stay inline", () => {
        const { nodes } = processSteps(buildToolWorkflowFixture());
        const group = nodes.find(n => n.type === "group");
        expect(group).toBeDefined();

        const blocks = group!.children.filter(c => c.type === "parallelBlock");
        expect(blocks).toHaveLength(1);
        expect(blocks[0].children).toHaveLength(5);
        expect(blocks[0].children.map(c => c.data.iterationIndex)).toEqual([0, 1, 2, 3, 4]);
        for (const child of blocks[0].children) expect(child.type).toBe("tool");

        const serialBuild = group!.children.find(c => c.data.nodeId === "build");
        expect(serialBuild).toBeDefined();
        expect(serialBuild!.type).toBe("tool");
    });

    test("node results update tool node status", () => {
        const { nodes } = processSteps(buildToolWorkflowFixture());
        const group = nodes.find(n => n.type === "group");

        const collectStatuses = (node: LayoutNode, acc: string[]) => {
            if (node.type === "tool") acc.push(node.data.status ?? "");
            for (const child of node.children) collectStatuses(child, acc);
            return acc;
        };
        const statuses = collectStatuses(group!, []);
        expect(statuses).toHaveLength(6);
        for (const status of statuses) expect(status).toBe("completed");
    });
});

describe("FlowChart layout — depends_on dependency mode", () => {
    const DEPS: Record<string, string[]> = {
        health: [],
        agent_perf_q: [],
        token_q: [],
        feedback_q: [],
        tasks_q: [],
        sql_status: ["health"],
        sql_agent: ["agent_perf_q"],
        sql_token: ["token_q"],
        sql_tpd: ["tasks_q"],
        sql_kpi: ["health", "feedback_q"],
        sql_feedback: ["feedback_q"],
        build: ["sql_kpi", "sql_status", "sql_tpd", "sql_agent", "sql_token", "sql_feedback"],
    };
    const START_ORDER = ["health", "agent_perf_q", "token_q", "feedback_q", "tasks_q", "sql_status", "sql_agent", "sql_token", "sql_tpd", "sql_kpi", "sql_feedback", "build"];

    function buildDependencyFixture(): VisualizerStep[] {
        stepCounter = 0;
        const executionId = "wf-exec-dep";
        const steps: VisualizerStep[] = [workflowStart(executionId, "WeeklyObservabilityReport")];
        for (const nodeId of START_ORDER) {
            steps.push(workflowToolNodeStart(executionId, nodeId, undefined, undefined, DEPS[nodeId]));
        }
        for (const nodeId of START_ORDER) {
            steps.push(workflowToolNodeResult(executionId, nodeId));
        }
        steps.push(workflowExecutionResult(executionId));
        return steps;
    }

    test("nodes are placed one row per topological level", () => {
        const { nodes } = processSteps(buildDependencyFixture());
        const group = nodes.find(n => n.type === "group");
        expect(group).toBeDefined();

        const blocks = group!.children.filter(c => c.type === "parallelBlock");
        expect(blocks.map(b => b.children.map(c => c.data.nodeId).sort())).toEqual([["agent_perf_q", "feedback_q", "health", "tasks_q", "token_q"], ["sql_agent", "sql_feedback", "sql_kpi", "sql_status", "sql_token", "sql_tpd"], ["build"]]);
        for (const block of blocks) {
            for (const child of block.children) {
                expect(child.type).toBe("tool");
                expect(child.data.toolName).toBe(`${child.data.nodeId}_tool`);
            }
        }
    });

    test("computeDependencyEdges returns the true DAG edges incl. Start and Finish anchors", () => {
        const { nodes } = processSteps(buildDependencyFixture());
        const group = nodes.find(n => n.type === "group")!;

        const edges = computeDependencyEdges(group);
        expect(edges).not.toBeNull();

        const labelByLayoutId = new Map<string, string>();
        const collect = (n: LayoutNode) => {
            labelByLayoutId.set(n.id, n.data.nodeId || n.data.label);
            for (const c of n.children) collect(c);
        };
        collect(group);

        const got = edges!.map(e => `${labelByLayoutId.get(e.sourceId)}->${labelByLayoutId.get(e.targetId)}`).sort();

        const want = [...["health", "agent_perf_q", "token_q", "feedback_q", "tasks_q"].map(n => `Start->${n}`), ...Object.entries(DEPS).flatMap(([nodeId, deps]) => deps.map(d => `${d}->${nodeId}`)), "build->Finish"].sort();

        expect(got).toEqual(want);
    });

    test("map and loop nodes record their DAG level so dependents land on the following row", () => {
        stepCounter = 0;
        const executionId = "wf-exec-containers";
        const containerStart = (nodeId: string, nodeType: "map" | "loop", dependsOn: string[]): VisualizerStep => ({
            id: `vstep-wfnode-${stepCounter++}`,
            type: "WORKFLOW_NODE_EXECUTION_START",
            timestamp: new Date().toISOString(),
            title: `Node ${nodeId} started`,
            source: nodeId,
            target: nodeId,
            data: { workflowNodeExecutionStart: { nodeId, nodeType, dependsOn } },
            rawEventIds: [],
            nestingLevel: 0,
            owningTaskId: executionId,
        });

        const steps: VisualizerStep[] = [
            workflowStart(executionId, "ContainerWorkflow"),
            workflowToolNodeStart(executionId, "seed", undefined, undefined, []),
            containerStart("fan_out", "map", ["seed"]),
            containerStart("retry", "loop", ["seed"]),
            workflowToolNodeStart(executionId, "collect", undefined, undefined, ["fan_out", "retry"]),
        ];

        const { nodes } = processSteps(steps);
        const group = nodes.find(n => n.type === "group")!;

        expect(group.children.find(c => c.type === "map")?.data.nodeId).toBe("fan_out");
        expect(group.children.find(c => c.type === "loop")?.data.nodeId).toBe("retry");

        const blocks = group.children.filter(c => c.type === "parallelBlock");
        expect(blocks.map(b => b.children.map(c => c.data.nodeId))).toEqual([["seed"], ["collect"]]);
    });

    test("a node whose dependencies are all unrendered anchors to Start instead of floating", () => {
        stepCounter = 0;
        const executionId = "wf-exec-partial";
        const steps: VisualizerStep[] = [
            workflowStart(executionId, "PartialWorkflow"),
            workflowToolNodeStart(executionId, "visible_root", undefined, undefined, []),
            workflowToolNodeStart(executionId, "consumer", undefined, undefined, ["skipped_dep"]),
            workflowToolNodeResult(executionId, "visible_root"),
            workflowToolNodeResult(executionId, "consumer"),
            workflowExecutionResult(executionId),
        ];

        const { nodes } = processSteps(steps);
        const group = nodes.find(n => n.type === "group")!;
        const edges = computeDependencyEdges(group)!;

        const labelByLayoutId = new Map<string, string>();
        const collect = (n: LayoutNode) => {
            labelByLayoutId.set(n.id, n.data.nodeId || n.data.label);
            for (const c of n.children) collect(c);
        };
        collect(group);
        const got = edges.map(e => `${labelByLayoutId.get(e.sourceId)}->${labelByLayoutId.get(e.targetId)}`).sort();

        expect(got).toEqual(["Start->consumer", "Start->visible_root", "consumer->Finish", "visible_root->Finish"]);
    });

    test("a sub-workflow node joins the DAG instead of floating disconnected", () => {
        stepCounter = 0;
        const executionId = "wf-exec-parent";
        const subTaskId = "sub-task-1";

        const workflowNodeStart: VisualizerStep = {
            id: `vstep-wfnode-${stepCounter++}`,
            type: "WORKFLOW_NODE_EXECUTION_START",
            timestamp: new Date().toISOString(),
            title: "Node sub started",
            source: "sub",
            target: "sub",
            data: { workflowNodeExecutionStart: { nodeId: "sub", nodeType: "workflow", agentName: "ChildWorkflow", dependsOn: ["prep"], subTaskId } },
            rawEventIds: [],
            nestingLevel: 0,
            owningTaskId: executionId,
        };
        const subWorkflowStart: VisualizerStep = {
            id: `vstep-wfstart-${stepCounter++}`,
            type: "WORKFLOW_EXECUTION_START",
            timestamp: new Date().toISOString(),
            title: "Workflow ChildWorkflow started",
            source: "ChildWorkflow",
            target: "ChildWorkflow",
            data: { workflowExecutionStart: { workflowName: "ChildWorkflow", executionId: "wf-exec-child" } },
            rawEventIds: [],
            nestingLevel: 1,
            owningTaskId: subTaskId,
        };

        const steps: VisualizerStep[] = [
            workflowStart(executionId, "ParentWorkflow"),
            workflowToolNodeStart(executionId, "prep", undefined, undefined, []),
            workflowNodeStart,
            subWorkflowStart,
            workflowToolNodeStart(executionId, "post", undefined, undefined, ["sub"]),
            workflowToolNodeResult(executionId, "prep"),
            workflowToolNodeResult(executionId, "post"),
            workflowExecutionResult(executionId),
        ];

        const { nodes } = processSteps(steps);
        const parentGroup = nodes.find(n => n.type === "group" && n.data.label === "ParentWorkflow")!;

        const subGroup = parentGroup.children.flatMap(c => (c.type === "parallelBlock" ? c.children : [c])).find(c => c.type === "group");
        expect(subGroup?.data.nodeId).toBe("sub");

        const edges = computeDependencyEdges(parentGroup)!;
        const labelByLayoutId = new Map<string, string>();
        const collect = (n: LayoutNode) => {
            labelByLayoutId.set(n.id, n.data.nodeId || n.data.label);
            for (const c of n.children) collect(c);
        };
        collect(parentGroup);
        const got = edges.map(e => `${labelByLayoutId.get(e.sourceId)}->${labelByLayoutId.get(e.targetId)}`).sort();

        expect(got).toEqual(["Start->prep", "post->Finish", "prep->sub", "sub->post"]);
    });

    test("sibling sub-workflow nodes on the same DAG level share one row", () => {
        stepCounter = 0;
        const executionId = "wf-exec-fanout";

        const subWorkflowNodeStart = (nodeId: string, childName: string, subTaskId: string): VisualizerStep => ({
            id: `vstep-wfnode-${stepCounter++}`,
            type: "WORKFLOW_NODE_EXECUTION_START",
            timestamp: new Date().toISOString(),
            title: `Node ${nodeId} started`,
            source: nodeId,
            target: nodeId,
            data: { workflowNodeExecutionStart: { nodeId, nodeType: "workflow", agentName: childName, dependsOn: [], subTaskId } },
            rawEventIds: [],
            nestingLevel: 0,
            owningTaskId: executionId,
        });

        const childWorkflowStart = (childName: string, childExecutionId: string, subTaskId: string): VisualizerStep => ({
            id: `vstep-wfstart-${stepCounter++}`,
            type: "WORKFLOW_EXECUTION_START",
            timestamp: new Date().toISOString(),
            title: `Workflow ${childName} started`,
            source: childName,
            target: childName,
            data: { workflowExecutionStart: { workflowName: childName, executionId: childExecutionId } },
            rawEventIds: [],
            nestingLevel: 1,
            owningTaskId: subTaskId,
        });

        const steps: VisualizerStep[] = [workflowStart(executionId, "DetectAndAnnotateFourImages")];
        for (const n of [1, 2, 3, 4]) {
            steps.push(subWorkflowNodeStart(`annotate_${n}`, "DetectAndAnnotateFromUrl", `sub-task-${n}`));
            steps.push(childWorkflowStart("DetectAndAnnotateFromUrl", `wf-exec-child-${n}`, `sub-task-${n}`));
        }
        steps.push(workflowExecutionResult(executionId));

        const { nodes } = processSteps(steps);
        const parentGroup = nodes.find(n => n.type === "group" && n.data.label === "DetectAndAnnotateFourImages")!;

        const blocks = parentGroup.children.filter(c => c.type === "parallelBlock");
        expect(blocks).toHaveLength(1);
        expect(parentGroup.children.filter(c => c.type === "group")).toHaveLength(0);

        const columns = blocks[0].children;
        expect(columns.every(c => c.type === "group")).toBe(true);
        expect(columns.map(c => c.data.nodeId).sort()).toEqual(["annotate_1", "annotate_2", "annotate_3", "annotate_4"]);

        expect(columns.map(c => c.data.iterationIndex).sort()).toEqual([0, 1, 2, 3]);
    });

    test("without depends_on the stream stays in heuristic mode", () => {
        stepCounter = 0;
        const executionId = "wf-exec-legacy";
        const steps: VisualizerStep[] = [workflowStart(executionId, "LegacyWorkflow"), workflowToolNodeStart(executionId, "a", "group-1", 0), workflowToolNodeStart(executionId, "b", "group-1", 1)];

        const { nodes } = processSteps(steps);
        const group = nodes.find(n => n.type === "group")!;
        expect(computeDependencyEdges(group)).toBeNull();
        expect(group.children.filter(c => c.type === "parallelBlock")).toHaveLength(1);
    });
});

describe("FlowChart layout — decision nodes", () => {
    const executionId = "wf-exec-decision";
    const pruned = (nodeId: string, parentType = "decision", parentId = "triage") => nodeResult(nodeId, "skipped", { skip_reason: `not selected by ${parentType} "${parentId}"` });

    function decisionStart(nodeId: string, dependsOn: string[]): VisualizerStep {
        return {
            id: `vstep-wfnode-${stepCounter++}`,
            type: "WORKFLOW_NODE_EXECUTION_START",
            timestamp: new Date().toISOString(),
            title: `Node ${nodeId} started`,
            source: nodeId,
            target: nodeId,
            data: { workflowNodeExecutionStart: { nodeId, nodeType: "decision", dependsOn } },
            rawEventIds: [],
            nestingLevel: 0,
            owningTaskId: executionId,
        };
    }

    function nodeResult(nodeId: string, status: "success" | "skipped", metadata: Record<string, unknown>): VisualizerStep {
        return {
            id: `vstep-wfnoderesult-${stepCounter++}`,
            type: "WORKFLOW_NODE_EXECUTION_RESULT",
            timestamp: new Date().toISOString(),
            title: `Node ${nodeId} finished`,
            source: nodeId,
            target: nodeId,
            data: { workflowNodeExecutionResult: { nodeId, status, metadata } },
            rawEventIds: [],
            nestingLevel: 0,
            owningTaskId: executionId,
        };
    }

    const confidentResult = {
        answers: { category: { type: "choice", choice: "billing", confidence: 0.92, probabilities: { billing: 0.92, technical: 0.08 } } },
        escalated: false,
        escalation_reason: "",
        model: "jev-1",
        duration_ms: 41,
        selected_branch: "billing_agent",
        routed_branch: "billing_agent",
    };

    function buildDecisionRun(result: Record<string, unknown>, prunedTargets: string[], chosen: string): VisualizerStep[] {
        stepCounter = 0;
        return [
            workflowStart(executionId, "SupportTriage"),
            workflowToolNodeStart(executionId, "intake", undefined, undefined, []),
            workflowToolNodeResult(executionId, "intake"),
            decisionStart("triage", ["intake"]),
            nodeResult("triage", "success", result),
            ...prunedTargets.map(target => pruned(target)),
            workflowToolNodeStart(executionId, chosen, undefined, undefined, ["triage"]),
            workflowToolNodeResult(executionId, chosen),
            workflowExecutionResult(executionId),
        ];
    }

    function findByNodeId(root: LayoutNode, nodeId: string): LayoutNode | undefined {
        if (root.data.nodeId === nodeId) return root;
        for (const child of root.children) {
            const found = findByNodeId(child, nodeId);
            if (found) return found;
        }
        return undefined;
    }

    test("a decision node is typed as a decision and labelled with its node id", () => {
        const { nodes } = processSteps(buildDecisionRun(confidentResult, ["technical_agent"], "billing_agent"));
        const group = nodes.find(n => n.type === "group")!;

        const decision = findByNodeId(group, "triage")!;
        expect(decision.type).toBe("decision");
        expect(decision.data.label).toBe("triage");
        expect(decision.width).toBeGreaterThan(0);
    });

    test("a confident decision highlights its routed branch and dims the branches it pruned", () => {
        const { nodes } = processSteps(buildDecisionRun(confidentResult, ["technical_agent", "human_review"], "billing_agent"));
        const group = nodes.find(n => n.type === "group")!;

        const decision = findByNodeId(group, "triage")!;
        expect(decision.data.status).toBe("completed");
        expect(decision.data.selectedBranch).toBe("billing_agent");
        expect(decision.data.decision?.answers).toEqual([{ key: "category", type: "choice", choice: "billing", confidence: 0.92, value: undefined, probability: undefined, score: undefined, level: undefined }]);
        expect(decision.data.decision?.escalated).toBe(false);

        const branchRow = group.children.filter(c => c.type === "parallelBlock").find(block => block.children.some(c => c.data.nodeId === "billing_agent"))!;
        const byId = new Map(branchRow.children.map(c => [c.data.nodeId, c]));
        expect([...byId.keys()].sort()).toEqual(["billing_agent", "human_review", "technical_agent"]);
        expect(byId.get("billing_agent")!.data.isSkipped).toBeUndefined();
        for (const skipped of ["technical_agent", "human_review"]) {
            expect(byId.get(skipped)!.data.isSkipped).toBe(true);
            expect(byId.get(skipped)!.data.status).toBe("skipped");
        }

        const labelByLayoutId = new Map<string, string>();
        const collect = (n: LayoutNode) => {
            labelByLayoutId.set(n.id, n.data.nodeId || n.data.label);
            n.children.forEach(collect);
        };
        collect(group);
        const edges = computeDependencyEdges(group)!.map(e => `${labelByLayoutId.get(e.sourceId)}->${labelByLayoutId.get(e.targetId)}`);
        expect(edges).toEqual(expect.arrayContaining(["intake->triage", "triage->billing_agent", "triage->technical_agent", "triage->human_review"]));
    });

    test("a route that also waits on the pruned escalation target sits beside the pruned branches, which lead nowhere", () => {
        stepCounter = 0;
        const steps = [
            workflowStart(executionId, "SupportTriage"),
            decisionStart("triage", []),
            nodeResult("triage", "success", confidentResult),
            pruned("technical_agent"),
            pruned("classify"),
            workflowToolNodeStart(executionId, "billing_agent", undefined, undefined, ["triage", "classify"]),
            workflowToolNodeResult(executionId, "billing_agent"),
            workflowExecutionResult(executionId),
        ];
        const { nodes } = processSteps(steps);
        const group = nodes.find(n => n.type === "group")!;

        const branchRow = group.children.filter(c => c.type === "parallelBlock").find(block => block.children.some(c => c.data.nodeId === "billing_agent"))!;
        expect(branchRow.children.map(c => c.data.nodeId).sort()).toEqual(["billing_agent", "classify", "technical_agent"]);

        const labelByLayoutId = new Map<string, string>();
        const collect = (n: LayoutNode) => {
            labelByLayoutId.set(n.id, n.data.nodeId || n.data.label);
            n.children.forEach(collect);
        };
        collect(group);
        const edges = computeDependencyEdges(group)!.map(e => `${labelByLayoutId.get(e.sourceId)}->${labelByLayoutId.get(e.targetId)}`);
        expect(edges).toEqual(expect.arrayContaining(["triage->billing_agent", "triage->technical_agent", "triage->classify", "billing_agent->Finish"]));
        expect(edges.filter(e => e.startsWith("classify->") || e.startsWith("technical_agent->"))).toEqual([]);
    });

    test("an escalated decision highlights the escalation target and records why it escalated", () => {
        const escalated = {
            answers: { category: { type: "choice", choice: "billing", confidence: 0.41 } },
            escalated: true,
            escalation_reason: "low_confidence",
            model: "jev-1",
            duration_ms: 38,
            selected_branch: "human_review",
        };
        const { nodes } = processSteps(buildDecisionRun(escalated, ["billing_agent", "technical_agent"], "human_review"));
        const group = nodes.find(n => n.type === "group")!;

        const decision = findByNodeId(group, "triage")!;
        expect(decision.data.selectedBranch).toBe("human_review");
        expect(decision.data.decision?.escalated).toBe(true);
        expect(decision.data.decision?.escalationReason).toBe("low_confidence");
        expect(findByNodeId(group, "billing_agent")!.data.isSkipped).toBe(true);
        expect(findByNodeId(group, "human_review")!.data.isSkipped).toBeUndefined();
    });

    test("after a rerouting escalation the routed branch comes from the route that started, and the routes the escalation pruned are dimmed", () => {
        stepCounter = 0;
        const escalated = {
            answers: { category: { type: "choice", choice: "billing", confidence: 0.42 } },
            escalated: true,
            escalation_reason: "low_confidence",
            model: "jev-1",
            duration_ms: 38,
            selected_branch: "classify",
        };
        const steps = [
            workflowStart(executionId, "SupportTriage"),
            decisionStart("triage", []),
            nodeResult("triage", "success", escalated),
            workflowToolNodeStart(executionId, "classify", undefined, undefined, ["triage"]),
            pruned("billing_agent", "agent", "classify"),
            pruned("technical_agent", "agent", "classify"),
            workflowToolNodeResult(executionId, "classify"),
            workflowToolNodeStart(executionId, "general_agent", undefined, undefined, ["triage", "classify"]),
            workflowToolNodeResult(executionId, "general_agent"),
            workflowExecutionResult(executionId),
        ];
        const { nodes } = processSteps(steps);
        const group = nodes.find(n => n.type === "group")!;

        const decision = findByNodeId(group, "triage")!;
        expect(decision.data.selectedBranch).toBe("classify");
        expect(decision.data.escalationRoute).toBe("general_agent");

        const routeRow = group.children.filter(c => c.type === "parallelBlock").find(block => block.children.some(c => c.data.nodeId === "general_agent"))!;
        expect(routeRow.children.map(c => c.data.nodeId).sort()).toEqual(["billing_agent", "general_agent", "technical_agent"]);
        for (const pruned of ["billing_agent", "technical_agent"]) {
            expect(findByNodeId(group, pruned)!.data.isSkipped).toBe(true);
        }

        const labelByLayoutId = new Map<string, string>();
        const collect = (n: LayoutNode) => {
            labelByLayoutId.set(n.id, n.data.nodeId || n.data.label);
            n.children.forEach(collect);
        };
        collect(group);
        const edges = computeDependencyEdges(group)!.map(e => `${labelByLayoutId.get(e.sourceId)}->${labelByLayoutId.get(e.targetId)}`);
        expect(edges).toEqual(expect.arrayContaining(["triage->classify", "classify->billing_agent", "classify->technical_agent", "classify->general_agent", "general_agent->Finish"]));
        expect(edges.filter(e => e.startsWith("billing_agent->") || e.startsWith("technical_agent->"))).toEqual([]);
    });

    describe("which start is the route an escalation picked", () => {
        const escalatedToClassify = {
            answers: { category: { type: "choice", choice: "billing", confidence: 0.42 } },
            escalated: true,
            escalation_reason: "low_confidence",
            selected_branch: "classify",
        };

        function typedNodeStart(nodeId: string, nodeType: string, dependsOn: string[]): VisualizerStep {
            return { ...decisionStart(nodeId, dependsOn), data: { workflowNodeExecutionStart: { nodeId, nodeType, dependsOn } } };
        }

        function routeAfterEscalation(steps: VisualizerStep[]): string | undefined {
            const { nodes } = processSteps(steps);
            return findByNodeId(
                nodes.find(n => n.type === "group")!,
                "triage"
            )!.data.escalationRoute;
        }

        function escalatedRun(...after: VisualizerStep[]): VisualizerStep[] {
            stepCounter = 0;
            return [workflowStart(executionId, "SupportTriage"), decisionStart("triage", []), nodeResult("triage", "success", escalatedToClassify), workflowToolNodeStart(executionId, "classify", undefined, undefined, ["triage"]), ...after];
        }

        test("a plain escalation, with no prunes by its escalate target, marks no route", () => {
            const steps = escalatedRun(workflowToolNodeResult(executionId, "classify"), workflowToolNodeStart(executionId, "notify", undefined, undefined, ["triage", "classify"]));
            expect(routeAfterEscalation(steps)).toBeUndefined();
        });

        test("a later fan-in waiting on the same nodes does not overwrite the route", () => {
            const steps = escalatedRun(
                pruned("billing_agent", "agent", "classify"),
                workflowToolNodeResult(executionId, "classify"),
                workflowToolNodeStart(executionId, "general_agent", undefined, undefined, ["triage", "classify"]),
                workflowToolNodeStart(executionId, "summarize", undefined, undefined, ["triage", "classify", "general_agent"]),
                workflowToolNodeStart(executionId, "audit", undefined, undefined, ["triage", "classify"])
            );
            expect(routeAfterEscalation(steps)).toBe("general_agent");
        });

        test("a decision escalate target's prunes before its own result are the escalation's, and its later ones its own", () => {
            stepCounter = 0;
            const reclassified = {
                answers: { department: { type: "choice", choice: "general", confidence: 0.97 } },
                escalated: false,
                selected_branch: "general_agent",
                routed_branch: "general_agent",
            };
            const steps = [
                workflowStart(executionId, "SupportTriage"),
                decisionStart("triage", []),
                nodeResult("triage", "success", { ...escalatedToClassify, selected_branch: "reclassify" }),
                decisionStart("reclassify", ["triage"]),
                pruned("billing_agent", "decision", "reclassify"),
                pruned("technical_agent", "decision", "reclassify"),
                nodeResult("reclassify", "success", reclassified),
                pruned("reclassify_followup", "decision", "reclassify"),
                workflowToolNodeStart(executionId, "general_agent", undefined, undefined, ["triage", "reclassify"]),
            ];
            const { nodes } = processSteps(steps);
            const group = nodes.find(n => n.type === "group")!;
            expect(findByNodeId(group, "triage")!.data.escalationRoute).toBe("general_agent");
            for (const skipped of ["billing_agent", "technical_agent", "reclassify_followup"]) {
                expect(findByNodeId(group, skipped)!.data.isSkipped).toBe(true);
            }
        });

        test("a plain escalation to a decision marks no route from that decision's own prunes", () => {
            stepCounter = 0;
            const reclassified = {
                answers: { department: { type: "choice", choice: "general", confidence: 0.97 } },
                escalated: false,
                selected_branch: "rc_general",
                routed_branch: "rc_general",
            };
            const steps = [
                workflowStart(executionId, "SupportTriage"),
                decisionStart("triage", []),
                nodeResult("triage", "success", { ...escalatedToClassify, selected_branch: "reclassify" }),
                decisionStart("reclassify", ["triage"]),
                nodeResult("reclassify", "success", reclassified),
                pruned("rc_billing", "decision", "reclassify"),
                workflowToolNodeStart(executionId, "rc_general", undefined, undefined, ["triage", "reclassify"]),
            ];
            const { nodes } = processSteps(steps);
            const group = nodes.find(n => n.type === "group")!;
            expect(findByNodeId(group, "triage")!.data.escalationRoute).toBeUndefined();
            expect(findByNodeId(group, "rc_billing")!.data.isSkipped).toBe(true);
        });

        test("the route is found when its start arrives before the escalation's prunes", () => {
            const steps = escalatedRun(
                workflowToolNodeStart(executionId, "general_agent", undefined, undefined, ["triage", "classify"]),
                pruned("billing_agent", "agent", "classify"),
                pruned("technical_agent", "agent", "classify"),
                workflowToolNodeResult(executionId, "classify")
            );
            expect(routeAfterEscalation(steps)).toBe("general_agent");
        });

        test("a node waiting on a pruned branch is never taken for the route", () => {
            const steps = escalatedRun(
                pruned("billing_agent", "agent", "classify"),
                workflowToolNodeResult(executionId, "classify"),
                workflowToolNodeStart(executionId, "billing_followup", undefined, undefined, ["triage", "classify", "billing_agent"])
            );
            expect(routeAfterEscalation(steps)).toBeUndefined();
        });

        test("a route of any node kind is marked, not only agents and tools", () => {
            const steps = escalatedRun(pruned("billing_agent", "agent", "classify"), workflowToolNodeResult(executionId, "classify"), typedNodeStart("general_router", "switch", ["triage", "classify"]));
            expect(routeAfterEscalation(steps)).toBe("general_router");
        });

        test("a prune by an agent other than the escalate target proves nothing about the route and is drawn as a plain skip", () => {
            const steps = escalatedRun(pruned("billing_agent", "agent", "other_agent"), workflowToolNodeResult(executionId, "classify"), workflowToolNodeStart(executionId, "general_agent", undefined, undefined, ["triage", "classify"]));
            const { nodes } = processSteps(steps);
            const group = nodes.find(n => n.type === "group")!;
            expect(findByNodeId(group, "billing_agent")!.data.isSkipped).toBe(true);
            expect(findByNodeId(group, "triage")!.data.escalationRoute).toBeUndefined();
        });

        test("a switch that is the escalate target prunes its own branches, which are drawn as plain skips that mark no route", () => {
            stepCounter = 0;
            const steps = [
                workflowStart(executionId, "SupportTriage"),
                decisionStart("triage", []),
                nodeResult("triage", "success", { ...escalatedToClassify, selected_branch: "router" }),
                typedNodeStart("router", "switch", ["triage"]),
                pruned("case_b", "switch", "router"),
            ];
            const { nodes } = processSteps(steps);
            const group = nodes.find(n => n.type === "group")!;
            expect(findByNodeId(group, "case_b")!.data.isSkipped).toBe(true);
            expect(findByNodeId(group, "triage")!.data.escalationRoute).toBeUndefined();
        });

        test("a Go-escaped pruner id still anchors its prune", () => {
            stepCounter = 0;
            const steps = [
                workflowStart(executionId, "SupportTriage"),
                decisionStart('tri"age', []),
                nodeResult('tri"age', "success", confidentResult),
                nodeResult("technical_agent", "skipped", { skip_reason: 'not selected by decision "tri\\"age"' }),
            ];
            const { nodes } = processSteps(steps);
            expect(
                findByNodeId(
                    nodes.find(n => n.type === "group")!,
                    "technical_agent"
                )!.data.isSkipped
            ).toBe(true);
        });
    });

    test("a pruned branch is labelled with the agent its skip names, falling back to its node id", () => {
        stepCounter = 0;
        const steps = [
            workflowStart(executionId, "SupportTriage"),
            decisionStart("triage", []),
            nodeResult("triage", "success", confidentResult),
            nodeResult("technical_agent", "skipped", { skip_reason: 'not selected by decision "triage"', agent_name: "TechnicalAgent" }),
            pruned("human_review"),
        ];
        const { nodes } = processSteps(steps, { TechnicalAgent: "Technical Agent" });
        const group = nodes.find(n => n.type === "group")!;
        expect(findByNodeId(group, "technical_agent")!.data.label).toBe("Technical Agent");
        expect(findByNodeId(group, "human_review")!.data.label).toBe("human_review");
    });

    test("an agent's skip is a decision's branch only when it is an escalated decision's escalate target", () => {
        stepCounter = 0;
        const steps = [workflowStart(executionId, "SupportTriage"), decisionStart("triage", []), nodeResult("triage", "success", confidentResult), pruned("downstream", "agent", "some_agent")];
        const { nodes } = processSteps(steps);
        const group = nodes.find(n => n.type === "group")!;
        expect(findByNodeId(group, "downstream")!.data.isSkipped).toBe(true);

        const labelByLayoutId = new Map<string, string>();
        const collect = (n: LayoutNode) => {
            labelByLayoutId.set(n.id, n.data.nodeId || n.data.label);
            n.children.forEach(collect);
        };
        collect(group);
        const edges = (computeDependencyEdges(group) ?? []).map(e => `${labelByLayoutId.get(e.sourceId)}->${labelByLayoutId.get(e.targetId)}`);
        expect(edges).not.toContain("triage->downstream");
    });

    test("skips not caused by a decision are drawn as plain skips", () => {
        stepCounter = 0;
        const steps = [
            workflowStart(executionId, "SupportTriage"),
            decisionStart("triage", []),
            nodeResult("triage", "success", confidentResult),
            pruned("switch_target", "switch", "router"),
            pruned("unknown_decision_target", "decision", "elsewhere"),
            nodeResult("cascade", "skipped", { skip_reason: "all dependencies skipped" }),
        ];
        const { nodes } = processSteps(steps);
        const group = nodes.find(n => n.type === "group")!;

        for (const nodeId of ["switch_target", "unknown_decision_target", "cascade"]) {
            expect(findByNodeId(group, nodeId)!.data.isSkipped).toBe(true);
        }
    });

    test("a result without decision metadata leaves the node without a selection", () => {
        stepCounter = 0;
        const { nodes } = processSteps([workflowStart(executionId, "SupportTriage"), decisionStart("triage", []), nodeResult("triage", "success", {})]);
        const decision = findByNodeId(
            nodes.find(n => n.type === "group")!,
            "triage"
        )!;

        expect(decision.data.status).toBe("completed");
        expect(decision.data.decision).toBeUndefined();
        expect(decision.data.selectedBranch).toBeUndefined();
    });
});

describe("FlowChart layout — skipped nodes", () => {
    const executionId = "wf-exec-skips";
    const workflowName = "DependencyUpdate";

    function nodeStart(nodeId: string, nodeType: string, dependsOn: string[]): VisualizerStep {
        return {
            id: `vstep-wfnode-${stepCounter++}`,
            type: "WORKFLOW_NODE_EXECUTION_START",
            timestamp: new Date().toISOString(),
            title: `Node ${nodeId} started`,
            source: nodeId,
            target: nodeId,
            data: { workflowNodeExecutionStart: { nodeId, nodeType, dependsOn } },
            rawEventIds: [],
            nestingLevel: 0,
            owningTaskId: executionId,
        };
    }

    function nodeResult(nodeId: string, status: "success" | "skipped", metadata: Record<string, unknown> = {}): VisualizerStep {
        return {
            id: `vstep-wfnoderesult-${stepCounter++}`,
            type: "WORKFLOW_NODE_EXECUTION_RESULT",
            timestamp: new Date().toISOString(),
            title: `Node ${nodeId} finished`,
            source: nodeId,
            target: nodeId,
            data: { workflowNodeExecutionResult: { nodeId, status, metadata } },
            rawEventIds: [],
            nestingLevel: 0,
            owningTaskId: executionId,
        };
    }

    function rowsOf(group: LayoutNode): string[][] {
        return group.children.filter(c => c.type === "parallelBlock").map(block => block.children.map(c => c.data.nodeId!));
    }

    function skippedNode(group: LayoutNode, nodeId: string): LayoutNode {
        const node = group.children.flatMap(c => c.children).find(c => c.data.nodeId === nodeId)!;
        expect(node.data.isSkipped).toBe(true);
        expect(node.data.status).toBe("skipped");
        return node;
    }

    function buildRun(): VisualizerStep[] {
        stepCounter = 0;
        return [
            workflowStart(executionId, workflowName),
            nodeStart("intake", "tool", []),
            nodeResult("intake", "success"),
            nodeStart("assess", "agent", ["intake"]),
            nodeResult("assess", "success"),
            nodeResult("notify", "skipped", { skip_reason: "condition not met", node_type: "tool", depends_on: ["intake"], tool_name: "slack_post", agent_name: "" }),
            nodeResult("archive", "skipped", { skip_reason: "all dependencies skipped (parent: notify)" }),
            nodeResult("fix", "skipped", { skip_reason: "condition not met" }),
            nodeResult("mystery", "skipped", { skip_reason: "condition not met" }),
            workflowExecutionResult(executionId),
        ];
    }

    const definitions = { [workflowName]: [{ id: "fix", type: "agent" as const, agent_name: "fixer", depends_on: ["assess"] }] };

    test("a skip result that says where the node sits places it beside the nodes on its level", () => {
        const group = processSteps(buildRun(), { fixer: "Fixer" }, undefined, definitions).nodes.find(n => n.type === "group")!;

        const notify = skippedNode(group, "notify");
        expect(notify.type).toBe("tool");
        expect(notify.data.label).toBe("notify");
        expect(notify.data.toolName).toBe("slack_post");
        expect(notify.data.dependsOn).toEqual(["intake"]);
        expect(notify.data.description).toBeUndefined();
        expect(rowsOf(group)).toContainEqual(["assess", "notify"]);
    });

    test("a node the run never placed takes its type and dependencies from the deployed workflow", () => {
        const group = processSteps(buildRun(), { fixer: "Fixer" }, undefined, definitions).nodes.find(n => n.type === "group")!;

        const fix = skippedNode(group, "fix");
        expect(fix.type).toBe("agent");
        expect(fix.data.label).toBe("Fixer");
        expect(fix.data.dependsOn).toEqual(["assess"]);
        expect(fix.data.description).toMatch(/currently deployed workflow/);
    });

    test("a cascade skip sits below the parent it names, and a node nothing places goes after the deepest row", () => {
        const group = processSteps(buildRun(), { fixer: "Fixer" }, undefined, definitions).nodes.find(n => n.type === "group")!;

        const archive = skippedNode(group, "archive");
        expect(archive.data.dependsOn).toEqual(["notify"]);
        expect(archive.data.description).toMatch(/skip reason/);
        const mystery = skippedNode(group, "mystery");
        expect(mystery.data.dependsOn).toBeUndefined();
        expect(mystery.data.description).toMatch(/after the last row/);
        expect(rowsOf(group)).toEqual([["intake"], ["assess", "notify"], ["archive", "fix"], ["mystery"]]);
    });

    test("without the deployed workflow a skip with no placement is still drawn, labelled by its node id", () => {
        const group = processSteps(buildRun()).nodes.find(n => n.type === "group")!;

        const fix = skippedNode(group, "fix");
        expect(fix.type).toBe("agent");
        expect(fix.data.label).toBe("fix");
        expect(rowsOf(group)).toEqual([["intake"], ["assess", "notify"], ["archive"], ["fix", "mystery"]]);
    });

    test("a branch a decision pruned is drawn as the node type it would have run", () => {
        stepCounter = 0;
        const steps = [
            workflowStart(executionId, workflowName),
            nodeStart("triage", "decision", []),
            nodeResult("triage", "success", { selected_branch: "reply", routed_branch: "reply" }),
            nodeResult("open_ticket", "skipped", { skip_reason: 'not selected by decision "triage"', node_type: "tool", depends_on: ["triage"], tool_name: "jira_create" }),
            nodeStart("reply", "agent", ["triage"]),
            nodeResult("reply", "success"),
        ];
        const group = processSteps(steps).nodes.find(n => n.type === "group")!;

        const pruned = skippedNode(group, "open_ticket");
        expect(pruned.type).toBe("tool");
        expect(pruned.data.toolName).toBe("jira_create");
        expect(pruned.data.dependsOn).toEqual(["triage"]);
        expect(pruned.data.description).toBeUndefined();
    });

    test("nodes skipped below a pruned branch sit in the rows beneath it, not at the top", () => {
        stepCounter = 0;
        const steps = [
            workflowStart(executionId, workflowName),
            nodeStart("intake", "tool", []),
            nodeResult("intake", "success"),
            nodeStart("triage", "decision", ["intake"]),
            nodeResult("triage", "success", { selected_branch: "reply", routed_branch: "reply" }),
            nodeResult("open_ticket", "skipped", { skip_reason: 'not selected by decision "triage"', node_type: "tool", depends_on: ["triage"] }),
            nodeResult("notify_ticket", "skipped", { skip_reason: "all dependencies skipped (parent: open_ticket)", node_type: "agent", depends_on: ["open_ticket"] }),
            nodeResult("close_ticket", "skipped", { skip_reason: "all dependencies skipped (parent: notify_ticket)", node_type: "tool", depends_on: ["notify_ticket"] }),
            nodeStart("reply", "agent", ["triage"]),
            nodeResult("reply", "success"),
        ];
        const group = processSteps(steps).nodes.find(n => n.type === "group")!;
        const rows = rowsOf(group);
        const rowIndex = (nodeId: string) => rows.findIndex(row => row.includes(nodeId));

        expect(rows[rowIndex("open_ticket")]).toEqual(["open_ticket", "reply"]);
        expect(rows[rowIndex("intake")]).toEqual(["intake"]);
        expect(rowIndex("notify_ticket")).toBe(rowIndex("open_ticket") + 1);
        expect(rowIndex("close_ticket")).toBe(rowIndex("notify_ticket") + 1);
    });

    test("a second skip result for a drawn node leaves it skipped", () => {
        stepCounter = 0;
        const skip = { skip_reason: "condition not met", node_type: "tool", depends_on: ["intake"] };
        const steps = [workflowStart(executionId, workflowName), nodeStart("intake", "tool", []), nodeResult("intake", "success"), nodeResult("notify", "skipped", skip), nodeResult("notify", "skipped", skip)];
        const group = processSteps(steps).nodes.find(n => n.type === "group")!;

        skippedNode(group, "notify");
        expect(
            rowsOf(group)
                .flat()
                .filter(nodeId => nodeId === "notify")
        ).toHaveLength(1);
    });

    test("a skip in the parent workflow is drawn there even when a sub-workflow ran a node with the same id", () => {
        stepCounter = 0;
        const subTaskId = "sub-task-notify";
        const childExecutionId = "wf-exec-child";
        const steps: VisualizerStep[] = [
            workflowStart(executionId, workflowName),
            { ...nodeStart("sub", "workflow", []), data: { workflowNodeExecutionStart: { nodeId: "sub", nodeType: "workflow", agentName: "ChildWorkflow", dependsOn: [], subTaskId } } },
            { ...workflowStart(childExecutionId, "ChildWorkflow"), owningTaskId: subTaskId, nestingLevel: 1 },
            workflowToolNodeStart(childExecutionId, "notify", undefined, undefined, []),
            workflowToolNodeResult(childExecutionId, "notify"),
            nodeResult("notify", "skipped", { skip_reason: "condition not met", node_type: "tool", depends_on: ["sub"] }),
            workflowExecutionResult(executionId),
        ];
        const parentGroup = processSteps(steps).nodes.find(n => n.type === "group" && n.data.label === workflowName)!;

        skippedNode(parentGroup, "notify");
        const subGroup = parentGroup.children.flatMap(c => (c.type === "parallelBlock" ? c.children : [c])).find(c => c.type === "group")!;
        const childNotify = subGroup.children.flatMap(c => [c, ...c.children]).find(c => c.data.nodeId === "notify")!;
        expect(childNotify.data.status).toBe("completed");
    });

    describe("dependency edges", () => {
        function edgesOf(group: LayoutNode): string[] {
            const labelByLayoutId = new Map<string, string>();
            const collect = (n: LayoutNode) => {
                labelByLayoutId.set(n.id, n.data.nodeId || n.data.label);
                n.children.forEach(collect);
            };
            collect(group);
            return computeDependencyEdges(group)!.map(e => `${labelByLayoutId.get(e.sourceId)}->${labelByLayoutId.get(e.targetId)}`);
        }

        test("a last node skipped by its condition leaves the node before it connected to Finish", () => {
            stepCounter = 0;
            const steps = [
                workflowStart(executionId, workflowName),
                nodeStart("intake", "tool", []),
                nodeResult("intake", "success"),
                nodeStart("assess", "agent", ["intake"]),
                nodeResult("assess", "success"),
                nodeResult("notify", "skipped", { skip_reason: "condition not met", node_type: "tool", depends_on: ["assess"] }),
                workflowExecutionResult(executionId),
            ];
            const group = processSteps(steps).nodes.find(n => n.type === "group")!;

            expect(edgesOf(group).sort()).toEqual(["Start->intake", "assess->Finish", "assess->notify", "intake->assess"]);
        });

        test("a cascade hangs below the skip that caused it, not from Start", () => {
            stepCounter = 0;
            const steps = [
                workflowStart(executionId, workflowName),
                nodeStart("a", "tool", []),
                nodeResult("a", "success"),
                nodeStart("b", "tool", ["a"]),
                nodeResult("b", "success"),
                nodeResult("c", "skipped", { skip_reason: "condition not met", node_type: "tool", depends_on: ["b"] }),
                nodeResult("d", "skipped", { skip_reason: "all dependencies skipped (parent: c)", node_type: "tool", depends_on: ["c"] }),
                workflowExecutionResult(executionId),
            ];
            const group = processSteps(steps).nodes.find(n => n.type === "group")!;

            expect(edgesOf(group).sort()).toEqual(["Start->a", "a->b", "b->Finish", "b->c", "c->d"]);
        });

        test("a skip the run cannot place gets no edge", () => {
            const group = processSteps(buildRun()).nodes.find(n => n.type === "group")!;

            const edges = edgesOf(group);
            expect(edges.filter(e => e.endsWith("->mystery") || e.endsWith("->fix"))).toEqual([]);
            expect(edges).toContain("assess->Finish");
        });
    });
});

describe("FlowChart layout — failed nodes", () => {
    const executionId = "wf-exec-failed";

    test('a node and a run the Go executor reports as "error" are drawn as failed', () => {
        stepCounter = 0;
        const steps: VisualizerStep[] = [
            workflowStart(executionId, "Deploy"),
            workflowToolNodeStart(executionId, "deploy", undefined, undefined, []),
            { ...workflowToolNodeResult(executionId, "deploy"), data: { workflowNodeExecutionResult: { nodeId: "deploy", status: "error" } } },
            { ...workflowExecutionResult(executionId), data: { workflowExecutionResult: { status: "error" } } },
        ];
        const group = processSteps(steps).nodes.find(n => n.type === "group")!;

        expect(group.children.flatMap(c => [c, ...c.children]).find(c => c.data.nodeId === "deploy")!.data.status).toBe("error");
        expect(group.children.find(c => c.data.label === "Finish")!.data.status).toBe("error");
    });
});

const TASK_ID = "task-1";
const AGENT_NAME = "JiraConfluenceAgent";

let timeMs = 0;
function tick(ms = 100): string {
    timeMs += ms;
    return offsetTime(timeMs);
}

function llmInvocation(): A2AEventSSEPayload {
    return createSignalEvent(
        "llm_invocation",
        {
            request: {
                model: "mock",
                contents: [{ role: "user", parts: [{ text: "go" }] }],
            },
        },
        AGENT_NAME,
        tick(),
        TASK_ID
    );
}

function llmResponseSingleToolCall(functionCallId: string, toolName: string): A2AEventSSEPayload {
    return createSignalEvent(
        "llm_response",
        {
            data: {
                content: {
                    parts: [
                        {
                            function_call: {
                                id: functionCallId,
                                name: toolName,
                                args: {},
                            },
                        },
                    ],
                },
                partial: false,
            },
        },
        AGENT_NAME,
        tick(),
        TASK_ID
    );
}

function llmResponseParallelToolCalls(calls: { id: string; name: string }[]): A2AEventSSEPayload {
    return createSignalEvent(
        "llm_response",
        {
            data: {
                content: {
                    parts: calls.map(c => ({
                        function_call: { id: c.id, name: c.name, args: {} },
                    })),
                },
                partial: false,
            },
        },
        AGENT_NAME,
        tick(),
        TASK_ID
    );
}

function toolInvocationStartEvent(functionCallId: string, toolName: string, parallelGroupId?: string): A2AEventSSEPayload {
    const data: Record<string, unknown> = {
        function_call_id: functionCallId,
        tool_name: toolName,
        tool_args: {},
    };
    if (parallelGroupId) data.parallel_group_id = parallelGroupId;
    return createSignalEvent("tool_invocation_start", data, AGENT_NAME, tick(), TASK_ID);
}

function toolResultEvent(functionCallId: string, toolName: string): A2AEventSSEPayload {
    return createSignalEvent(
        "tool_result",
        {
            function_call_id: functionCallId,
            tool_name: toolName,
            result_data: {},
        },
        AGENT_NAME,
        tick(),
        TASK_ID
    );
}

function buildJiraPaginationEvents(numIterations: number): A2AEventSSEPayload[] {
    timeMs = 0;
    const events: A2AEventSSEPayload[] = [createRequestEvent("How many Jira issues today, by project?", tick(), TASK_ID, AGENT_NAME)];

    for (let i = 0; i < numIterations; i++) {
        const searchFc = `fc-search-${i}`;
        events.push(llmInvocation());
        events.push(llmResponseSingleToolCall(searchFc, "searchJira"));
        events.push(toolInvocationStartEvent(searchFc, "searchJira"));
        events.push(toolResultEvent(searchFc, "searchJira"));

        const fcHeader = `fc-header-${i}`;
        const fcRows = `fc-rows-${i}`;
        const groupId = `llm_batch_iter${i}`;
        events.push(llmInvocation());
        events.push(
            llmResponseParallelToolCalls([
                { id: fcHeader, name: "transform_data_with_jmespath" },
                { id: fcRows, name: "transform_data_with_jmespath" },
            ])
        );
        events.push(toolInvocationStartEvent(fcHeader, "transform_data_with_jmespath", groupId));
        events.push(toolInvocationStartEvent(fcRows, "transform_data_with_jmespath", groupId));
        events.push(toolResultEvent(fcHeader, "transform_data_with_jmespath"));
        events.push(toolResultEvent(fcRows, "transform_data_with_jmespath"));
    }

    const sqlFc = "fc-sql";
    events.push(llmInvocation());
    events.push(llmResponseSingleToolCall(sqlFc, "query_data_with_sql"));
    events.push(toolInvocationStartEvent(sqlFc, "query_data_with_sql"));
    events.push(toolResultEvent(sqlFc, "query_data_with_sql"));
    events.push(createCompletedResponseEvent("done", AGENT_NAME, tick(), TASK_ID));
    return events;
}

function describeTree(node: LayoutNode, depth = 0): string {
    const indent = "  ".repeat(depth);
    const label = node.data.label || "";
    let out = `${indent}${node.type} ${label}`.trimEnd();
    if (node.type === "parallelBlock") {
        out += ` [${node.children.length} children]`;
    }
    out += "\n";
    for (const child of node.children) out += describeTree(child, depth + 1);
    return out;
}

describe("FlowChart layout — full pipeline (raw a2a events)", () => {
    test("Jira pagination: 5 iterations of (searchJira → 2 parallel transforms)", () => {
        const N = 5;
        const events = buildJiraPaginationEvents(N);
        const monitoredTasks = {
            [TASK_ID]: {
                taskId: TASK_ID,
                initialRequestText: "How many Jira issues today, by project?",
                events,
                firstSeen: new Date(BASE_TIME),
                lastUpdated: new Date(BASE_TIME),
            },
        };
        const viz = processTaskForVisualization(events, monitoredTasks, monitoredTasks[TASK_ID])!;
        const { nodes } = processSteps(viz.steps);
        const agent = findAgent(nodes);

        const treeDump = describeTree(agent);

        console.log("=== rendered tree ===\n" + treeDump);

        const blocks = agent.children.filter(c => c.type === "parallelBlock");
        expect(blocks).toHaveLength(N);
        for (const block of blocks) {
            expect(block.children).toHaveLength(2);
            for (const child of block.children) {
                expect(child.type).toBe("tool");
                expect(child.data.label).toBe("transform_data_with_jmespath");
            }
        }

        const tools = agent.children.filter(c => c.type === "tool");
        const searchJiras = tools.filter(t => t.data.label === "searchJira");
        const sqls = tools.filter(t => t.data.label === "query_data_with_sql");
        expect(searchJiras).toHaveLength(N);
        expect(sqls).toHaveLength(1);
    });
});

describe("detectCollapsibleDetail", () => {
    let nid = 0;
    const node = (type: LayoutNode["type"], children: LayoutNode[] = [], extra: Partial<LayoutNode> = {}): LayoutNode => ({
        id: `${type}-${nid++}`,
        type,
        data: { label: type },
        x: 0,
        y: 0,
        width: 0,
        height: 0,
        children,
        ...extra,
    });

    test("false for a flat activity: one top-level agent running LLM + tool nodes", () => {
        const tree = [node("user"), node("agent", [node("llm"), node("tool"), node("llm"), node("tool")])];
        expect(detectCollapsibleDetail(tree)).toBe(false);
    });

    test("true when an agent is nested inside another agent (peer delegation)", () => {
        const tree = [node("agent", [node("llm"), node("agent", [node("tool")])])];
        expect(detectCollapsibleDetail(tree)).toBe(true);
    });

    test("true when a nested agent sits under a parallel block child", () => {
        const tree = [node("agent", [node("parallelBlock", [node("agent", [node("tool")]), node("agent", [node("tool")])])])];
        expect(detectCollapsibleDetail(tree)).toBe(true);
    });

    test("false when a parallel block only contains parallel TOOL calls (tools don't collapse)", () => {
        const tree = [node("agent", [node("parallelBlock", [node("tool"), node("tool")])])];
        expect(detectCollapsibleDetail(tree)).toBe(false);
    });

    test("true for a workflow group", () => {
        expect(detectCollapsibleDetail([node("group", [node("agent")])])).toBe(true);
    });

    test("map / loop: collapsible only when a nested agent lives under them, not for tool-only iterations", () => {
        expect(detectCollapsibleDetail([node("map", [node("agent", [node("tool")])])])).toBe(true);
        expect(detectCollapsibleDetail([node("loop", [node("tool")])])).toBe(false);
        expect(detectCollapsibleDetail([node("map", [node("tool"), node("llm")])])).toBe(false);
        expect(detectCollapsibleDetail([node("map", [])])).toBe(false);
    });

    test("true when a node carries parallel branches (Map/Fork), matching collapseNestedAgents' length>0 trigger", () => {
        const withContent = [node("agent", [], { variant: "pill", parallelBranches: [[node("tool")], [node("tool")]] } as Partial<LayoutNode>)];
        expect(detectCollapsibleDetail(withContent)).toBe(true);
        const emptyBranch = [node("agent", [], { variant: "pill", parallelBranches: [[]] } as Partial<LayoutNode>)];
        expect(detectCollapsibleDetail(emptyBranch)).toBe(true);
    });

    test("false for empty input", () => {
        expect(detectCollapsibleDetail([])).toBe(false);
    });
});

describe("FlowChart layout — tool paused for OAuth sign-in", () => {
    const toolNodeAfter = (events: A2AEventSSEPayload[]): LayoutNode => {
        const task = createTask({ events });
        const viz = processTaskForVisualization(events, { "task-1": task }, task)!;
        return findAgent(processSteps(viz.steps).nodes).children.find(c => c.type === "tool")!;
    };

    test("a sign-in placeholder result leaves the tool node in progress", () => {
        expect(toolNodeAfter([...reportToolCallStartEvents, ...reportToolSignInPlaceholderEvents]).data.status).toBe("in-progress");
    });

    test("the final error result marks the tool node as errored", () => {
        expect(toolNodeAfter(pendingAuthToolCallTask.events).data.status).toBe("error");
    });

    test("a final result with status failed marks the tool node as errored", () => {
        const failedResult = reportToolResultEvent({ status: "failed", error_code: "TOOL_FAILED", message: "Report service unavailable" }, REPORT_TOOL_FINAL_RESULT_TIME);

        expect(toolNodeAfter([...reportToolCallStartEvents, ...reportToolSignInPlaceholderEvents, failedResult]).data.status).toBe("error");
    });

    test("a successful final result marks the tool node as completed", () => {
        const successfulResult = reportToolResultEvent({ rows: 3 }, REPORT_TOOL_FINAL_RESULT_TIME);

        expect(toolNodeAfter([...reportToolCallStartEvents, ...reportToolSignInPlaceholderEvents, successfulResult]).data.status).toBe("completed");
    });

    test("a tool's own result that says pending is final and completes the node", () => {
        const queuedJobResult = reportToolResultEvent({ status: "pending", job_id: "job-7" }, offsetTime(1305));

        expect(toolNodeAfter([...reportToolCallStartEvents, queuedJobResult]).data.status).toBe("completed");
    });
});
