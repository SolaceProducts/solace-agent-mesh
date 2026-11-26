// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { WorkflowNodeConfig } from "@/utils/agentUtils";

import type { LayoutNode, LayoutResult } from "../types";

export interface UnchangedLayoutFixture {
    name: string;
    nodes: WorkflowNodeConfig[];
    collapsed?: string[];
}

function nodeSnapshot(node: LayoutNode): unknown {
    const { id, type, x, y, width, height, level, isCollapsed } = node;
    const { label, conditionLabel, switchNodeId, targetNodeId, isDefaultCase, caseNumber, branchKind, routeOn, items, childNodeId } = node.data;
    const data = { label, conditionLabel, switchNodeId, targetNodeId, isDefaultCase, caseNumber, branchKind, routeOn, items, childNodeId };
    return { id, type, data, branches: node.branches, x, y, width, height, level, isCollapsed, children: node.children.map(nodeSnapshot) };
}

export function layoutSnapshot({ nodes, edges, totalWidth, totalHeight }: LayoutResult): unknown {
    return { nodes: nodes.map(nodeSnapshot), edges, totalWidth, totalHeight };
}

const agent = (id: string, depends_on: string[] = []) => ({ id, type: "agent", agent_name: "Helper", depends_on }) as WorkflowNodeConfig;
const loop = (id: string, node: string, depends_on: string[] = []) => ({ id, type: "loop", node, condition: "$.done == false", max_iterations: 3, depends_on }) as WorkflowNodeConfig;
const map = (id: string, node: string, depends_on: string[] = []) => ({ id, type: "map", node, items: "$.items", depends_on }) as WorkflowNodeConfig;

export const unchangedLayoutFixtures: UnchangedLayoutFixture[] = [
    {
        name: "chain with parallel branches and a join",
        nodes: [agent("a"), agent("b", ["a"]), agent("c", ["a"]), agent("d", ["b", "c"]), agent("e", ["d"])],
    },
    {
        name: "top-level switch with a join",
        nodes: [
            agent("a"),
            {
                id: "router",
                type: "switch",
                depends_on: ["a"],
                cases: [
                    { condition: "a.output.k == 1", node: "one" },
                    { condition: "a.output.k == 2", node: "two" },
                ],
                default: "other",
            } as WorkflowNodeConfig,
            agent("one"),
            agent("two"),
            agent("other"),
            agent("done", ["one", "two", "other"]),
        ],
    },
    {
        name: "top-level switch whose targets are declared out of case order",
        nodes: [
            agent("a"),
            {
                id: "router",
                type: "switch",
                depends_on: ["a"],
                cases: [
                    { condition: "a.output.k == 1", node: "one" },
                    { condition: "a.output.k == 2", node: "two" },
                    { condition: "a.output.k == 3", node: "three" },
                ],
                default: "other",
            } as WorkflowNodeConfig,
            agent("other"),
            agent("three"),
            agent("two"),
            agent("one"),
        ],
    },
    {
        name: "top-level decision that hands off on low confidence",
        nodes: [
            agent("a"),
            {
                id: "pick",
                type: "decision",
                depends_on: ["a"],
                questions: [{ key: "category", type: "choice", criteria: ["billing", "technical"] }],
                route_on: "category",
                routes: { billing: "billing_desk", technical: "tech_desk" },
                default: "general_desk",
                escalate: "human",
                escalation_mode: "handoff",
                min_confidence: 0.8,
            } as unknown as WorkflowNodeConfig,
            agent("human"),
            agent("general_desk"),
            agent("tech_desk"),
            agent("billing_desk"),
        ],
    },
    {
        name: "map over an agent with a dependent",
        nodes: [agent("start"), map("fanout", "worker", ["start"]), agent("worker"), agent("after", ["fanout"])],
    },
    {
        name: "loop over an agent, collapsed",
        nodes: [agent("start"), loop("retry", "worker", ["start"]), agent("worker"), agent("after", ["retry"])],
        collapsed: ["retry"],
    },
    {
        name: "map nested in a loop, with dependents of the outer loop",
        nodes: [agent("start"), loop("outer", "inner", ["start"]), map("inner", "worker"), agent("worker"), agent("left", ["outer"]), agent("right", ["outer"])],
    },
    {
        name: "top-level switch after a map",
        nodes: [
            agent("start"),
            map("fanout", "worker", ["start"]),
            agent("worker"),
            { id: "router", type: "switch", depends_on: ["fanout"], cases: [{ condition: "fanout.output.ok == true", node: "good" }], default: "bad" } as WorkflowNodeConfig,
            agent("good"),
            agent("bad"),
        ],
    },
    {
        name: "top-level switch routing to a loop",
        nodes: [
            agent("start"),
            { id: "router", type: "switch", depends_on: ["start"], cases: [{ condition: "start.output.k == 1", node: "retry" }], default: "skip" } as WorkflowNodeConfig,
            loop("retry", "worker"),
            agent("worker"),
            agent("skip"),
            agent("done", ["retry", "skip"]),
        ],
    },
];
