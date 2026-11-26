// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it, test } from "vitest";

import type { WorkflowConfig, WorkflowNodeConfig } from "@/utils/agentUtils";

import { layoutSnapshot, unchangedLayoutFixtures } from "./__fixtures__/layoutEngineUnchanged";
import unchangedBaseline from "./__fixtures__/layoutEngineUnchanged.baseline.json";
import { processWorkflowConfig } from "./layoutEngine";
import { LAYOUT_CONSTANTS } from "./types";

describe("processWorkflowConfig – implicit switch dependencies", () => {
    it("creates edges from switch to case targets that have no explicit depends_on", () => {
        const config: WorkflowConfig = {
            nodes: [
                {
                    id: "start_agent",
                    type: "agent",
                    agent_name: "Starter",
                },
                {
                    id: "my_switch",
                    type: "switch",
                    depends_on: ["start_agent"],
                    cases: [
                        { condition: "x == 1", node: "branch_a" },
                        { condition: "x == 2", node: "branch_b" },
                    ],
                    default: "branch_default",
                },
                {
                    id: "branch_a",
                    type: "agent",
                    agent_name: "AgentA",
                },
                {
                    id: "branch_b",
                    type: "agent",
                    agent_name: "AgentB",
                },
                {
                    id: "branch_default",
                    type: "agent",
                    agent_name: "AgentDefault",
                },
            ],
        };

        const result = processWorkflowConfig(config);

        for (const targetId of ["branch_a", "branch_b", "branch_default"]) {
            const pillId = `__condition_my_switch_${targetId}__`;
            const switchToPill = result.edges.find(e => e.source === "my_switch" && e.target === pillId);
            const pillToTarget = result.edges.find(e => e.source === pillId && e.target === targetId);
            expect(switchToPill, `expected edge my_switch -> ${pillId}`).toBeDefined();
            expect(pillToTarget, `expected edge ${pillId} -> ${targetId}`).toBeDefined();
        }
    });

    it("does not duplicate dependency when case target already has explicit depends_on", () => {
        const config: WorkflowConfig = {
            nodes: [
                {
                    id: "my_switch",
                    type: "switch",
                    cases: [{ condition: "x == 1", node: "branch_a" }],
                },
                {
                    id: "branch_a",
                    type: "agent",
                    agent_name: "AgentA",
                    depends_on: ["my_switch"],
                },
            ],
        };

        const result = processWorkflowConfig(config);

        const pillId = "__condition_my_switch_branch_a__";
        const switchToPill = result.edges.filter(e => e.source === "my_switch" && e.target === pillId);
        const pillToTarget = result.edges.filter(e => e.source === pillId && e.target === "branch_a");
        expect(switchToPill).toHaveLength(1);
        expect(pillToTarget).toHaveLength(1);
    });
});

const { NODE_WIDTHS, NODE_HEIGHTS, SPACING } = LAYOUT_CONSTANTS;

function makeConfig(nodes: WorkflowNodeConfig[]): WorkflowConfig {
    return { nodes } as WorkflowConfig;
}

function agentNode(id: string, depends_on: string[] = []): WorkflowNodeConfig {
    return { id, type: "agent", agent_name: id, depends_on } as WorkflowNodeConfig;
}

function nodeById(result: ReturnType<typeof processWorkflowConfig>, id: string) {
    const node = result.nodes.find(n => n.id === id);
    if (!node) throw new Error(`Node "${id}" not found in layout result`);
    return node;
}

function expectLeftToRightOnOneRow(result: ReturnType<typeof processWorkflowConfig>, ids: string[]) {
    const centerY = (id: string) => nodeById(result, id).y + nodeById(result, id).height / 2;
    for (let i = 1; i < ids.length; i++) {
        expect(nodeById(result, ids[i - 1]).x, `${ids[i - 1]} should sit left of ${ids[i]}`).toBeLessThan(nodeById(result, ids[i]).x);
        expect(centerY(ids[i - 1]), `${ids[i - 1]} and ${ids[i]} should share a row`).toBeCloseTo(centerY(ids[i]));
    }
}

describe("empty config", () => {
    test("returns start and end nodes", () => {
        const result = processWorkflowConfig(makeConfig([]));
        expect(result.nodes).toHaveLength(2);
        expect(result.nodes[0].id).toBe("__start__");
        expect(result.nodes[1].id).toBe("__end__");
    });

    test("start and end are connected by a single edge", () => {
        const result = processWorkflowConfig(makeConfig([]));
        expect(result.edges).toHaveLength(1);
        expect(result.edges[0].source).toBe("__start__");
        expect(result.edges[0].target).toBe("__end__");
    });
});

describe("single agent node", () => {
    test("agent node has correct dimensions", () => {
        const result = processWorkflowConfig(makeConfig([agentNode("step1")]));
        const node = nodeById(result, "step1");
        expect(node.width).toBe(NODE_WIDTHS.AGENT);
        expect(node.height).toBe(NODE_HEIGHTS.AGENT);
    });

    test("start -> agent -> end edges exist", () => {
        const result = processWorkflowConfig(makeConfig([agentNode("step1")]));
        const sources = result.edges.map(e => e.source);
        const targets = result.edges.map(e => e.target);
        expect(sources).toContain("__start__");
        expect(targets).toContain("step1");
        expect(sources).toContain("step1");
        expect(targets).toContain("__end__");
    });
});

describe("single tool node", () => {
    const toolNode: WorkflowNodeConfig = { id: "make_chart", type: "tool", tool_name: "create_pptx", depends_on: [] };

    test("tool node has correct type and dimensions", () => {
        const result = processWorkflowConfig(makeConfig([toolNode]));
        const node = nodeById(result, "make_chart");
        expect(node.type).toBe("tool");
        expect(node.width).toBe(NODE_WIDTHS.TOOL);
        expect(node.height).toBe(NODE_HEIGHTS.AGENT);
    });

    test("tool node data carries the tool name", () => {
        const result = processWorkflowConfig(makeConfig([toolNode]));
        expect(nodeById(result, "make_chart").data.toolName).toBe("create_pptx");
    });

    test("connector-bound tool node data carries the connector and the connector's tool", () => {
        const connectorNode: WorkflowNodeConfig = { id: "fetch_pets", type: "tool", connector: "PetStoreAPI", tool: "find_pets_by_status", depends_on: [] };
        const data = nodeById(processWorkflowConfig(makeConfig([connectorNode])), "fetch_pets").data;
        expect(data.connectorName).toBe("PetStoreAPI");
        expect(data.toolName).toBe("find_pets_by_status");
    });

    test("connector-only tool node carries the connector and no tool name", () => {
        const connectorNode: WorkflowNodeConfig = { id: "send", type: "tool", connector: "SlackConn", depends_on: [] };
        const data = nodeById(processWorkflowConfig(makeConfig([connectorNode])), "send").data;
        expect(data.connectorName).toBe("SlackConn");
        expect(data.toolName).toBeUndefined();
    });

    test("the connector's tool wins over the deploy-resolved tool_name", () => {
        const connectorNode: WorkflowNodeConfig = { id: "fetch", type: "tool", connector: "PetStoreAPI", tool: "find", tool_name: "PetStoreAPI__find", depends_on: [] };
        expect(nodeById(processWorkflowConfig(makeConfig([connectorNode])), "fetch").data.toolName).toBe("find");
    });

    test("tool without a connector is ignored in favour of tool_name", () => {
        const stray: WorkflowNodeConfig = { id: "stray", type: "tool", tool: "find", tool_name: "create_pptx", depends_on: [] };
        expect(nodeById(processWorkflowConfig(makeConfig([stray])), "stray").data.toolName).toBe("create_pptx");
    });

    test("empty or non-string connector and tool values are treated as absent", () => {
        const empty = { id: "empty", type: "tool", connector: "", tool: "", tool_name: "create_pptx", depends_on: [] } as WorkflowNodeConfig;
        const malformed = { id: "malformed", type: "tool", connector: { a: 1 }, tool: ["x"], tool_name: "create_pptx", depends_on: [] } as unknown as WorkflowNodeConfig;
        const malformedTool = { id: "malformedTool", type: "tool", connector: "C", tool: ["x"], tool_name: "create_pptx", depends_on: [] } as unknown as WorkflowNodeConfig;
        const result = processWorkflowConfig(makeConfig([empty, malformed, malformedTool]));
        for (const id of ["empty", "malformed"]) {
            expect(nodeById(result, id).data.connectorName).toBeUndefined();
            expect(nodeById(result, id).data.toolName).toBe("create_pptx");
        }
        expect(nodeById(result, "malformedTool").data.connectorName).toBe("C");
        expect(nodeById(result, "malformedTool").data.toolName).toBe("create_pptx");
    });

    test.each([
        ["a number", 123],
        ["a list", ["create_pptx"]],
        ["a map", { name: "create_pptx" }],
        ["null", null],
        ["a boolean", true],
        ["an empty string", ""],
    ])("a tool_name that is %s is treated as absent", (_label, toolName) => {
        const malformed = { id: "malformed", type: "tool", tool_name: toolName, depends_on: [] } as unknown as WorkflowNodeConfig;
        const data = nodeById(processWorkflowConfig(makeConfig([malformed])), "malformed").data;
        expect(data.toolName).toBeUndefined();
        expect(data.label).toBe("malformed");
    });

    test("a connector node with a malformed tool_name keeps the connector and names no tool", () => {
        const connectorNode = { id: "send", type: "tool", connector: "SlackConn", tool_name: 123, depends_on: [] } as unknown as WorkflowNodeConfig;
        const data = nodeById(processWorkflowConfig(makeConfig([connectorNode])), "send").data;
        expect(data.connectorName).toBe("SlackConn");
        expect(data.toolName).toBeUndefined();
    });

    test("tool node without a connector has no connector name", () => {
        expect(nodeById(processWorkflowConfig(makeConfig([toolNode])), "make_chart").data.connectorName).toBeUndefined();
    });

    test("start -> tool -> end edges exist", () => {
        const result = processWorkflowConfig(makeConfig([toolNode]));
        const edgePairs = result.edges.map(e => `${e.source}->${e.target}`);
        expect(edgePairs).toContain("__start__->make_chart");
        expect(edgePairs).toContain("make_chart->__end__");
    });
});

describe("linear chain", () => {
    const nodes = [agentNode("a"), agentNode("b", ["a"]), agentNode("c", ["b"])];

    test("nodes are laid out vertically (increasing y)", () => {
        const result = processWorkflowConfig(makeConfig(nodes));
        const ya = nodeById(result, "a").y;
        const yb = nodeById(result, "b").y;
        const yc = nodeById(result, "c").y;
        expect(yb).toBeGreaterThan(ya);
        expect(yc).toBeGreaterThan(yb);
    });

    test("edges form the chain", () => {
        const result = processWorkflowConfig(makeConfig(nodes));
        const edgePairs = result.edges.map(e => `${e.source}->${e.target}`);
        expect(edgePairs).toContain("a->b");
        expect(edgePairs).toContain("b->c");
    });
});

describe("parallel nodes", () => {
    const nodes = [agentNode("a"), agentNode("b1", ["a"]), agentNode("b2", ["a"]), agentNode("c", ["b1", "b2"])];

    test("parallel nodes are at the same level (same y)", () => {
        const result = processWorkflowConfig(makeConfig(nodes));
        const yb1 = nodeById(result, "b1").y;
        const yb2 = nodeById(result, "b2").y;
        expect(yb1).toBe(yb2);
    });

    test("merge node is below parallel nodes", () => {
        const result = processWorkflowConfig(makeConfig(nodes));
        const yb1 = nodeById(result, "b1").y;
        const yc = nodeById(result, "c").y;
        expect(yc).toBeGreaterThan(yb1);
    });
});

describe("switch node", () => {
    const switchNode: WorkflowNodeConfig = {
        id: "router",
        type: "switch",
        depends_on: [],
        cases: [
            { condition: "x > 0", node: "pos" },
            { condition: "x < 0", node: "neg" },
        ],
        default: "zero",
    } as unknown as WorkflowNodeConfig;

    const nodes = [switchNode, agentNode("pos", ["router"]), agentNode("neg", ["router"]), agentNode("zero", ["router"]), agentNode("done", ["pos", "neg", "zero"])];

    test("switch node has correct width", () => {
        const result = processWorkflowConfig(makeConfig(nodes));
        expect(nodeById(result, "router").width).toBe(NODE_WIDTHS.SWITCH_COLLAPSED);
    });

    test("switch node height accounts for cases and default", () => {
        const result = processWorkflowConfig(makeConfig(nodes));
        expect(nodeById(result, "router").height).toBeGreaterThan(NODE_HEIGHTS.AGENT);
    });

    test("condition pill nodes are inserted between switch and targets", () => {
        const result = processWorkflowConfig(makeConfig(nodes));
        const pillNodes = result.nodes.filter(n => n.type === "condition");
        expect(pillNodes).toHaveLength(3);
    });

    test("condition pill dimensions match constants", () => {
        const result = processWorkflowConfig(makeConfig(nodes));
        const pill = result.nodes.find(n => n.type === "condition")!;
        expect(pill).toBeDefined();
        expect(pill.height).toBe(NODE_HEIGHTS.CONDITION_PILL);
    });

    test("branch targets are at the same level", () => {
        const result = processWorkflowConfig(makeConfig(nodes));
        const ypos = nodeById(result, "pos").y;
        const yneg = nodeById(result, "neg").y;
        const yzero = nodeById(result, "zero").y;
        expect(ypos).toBe(yneg);
        expect(yneg).toBe(yzero);
    });

    test("switch node data carries cases and defaultCase", () => {
        const result = processWorkflowConfig(makeConfig(nodes));
        const router = nodeById(result, "router");
        expect(router.data.cases).toHaveLength(2);
        expect(router.data.defaultCase).toBe("zero");
    });

    test("switch node height is the header plus three stacked case rows", () => {
        const result = processWorkflowConfig(makeConfig(nodes));
        expect(nodeById(result, "router").height).toBe(152);
    });

    test("switch pills carry case numbers, the default flag, and no branch kind", () => {
        const result = processWorkflowConfig(makeConfig(nodes));
        const pills = result.nodes.filter(n => n.type === "condition").map(n => ({ id: n.id, label: n.data.label, caseNumber: n.data.caseNumber, isDefaultCase: n.data.isDefaultCase, branchKind: n.data.branchKind }));
        expect(pills).toEqual([
            { id: "__condition_router_pos__", label: "x > 0", caseNumber: 1, isDefaultCase: false, branchKind: undefined },
            { id: "__condition_router_neg__", label: "x < 0", caseNumber: 2, isDefaultCase: false, branchKind: undefined },
            { id: "__condition_router_zero__", label: "default", caseNumber: 3, isDefaultCase: true, branchKind: undefined },
        ]);
    });

    describe("branch order", () => {
        const fourCases = (): WorkflowNodeConfig =>
            ({
                id: "router",
                type: "switch",
                depends_on: [],
                cases: [
                    { condition: "kind == 1", node: "first" },
                    { condition: "kind == 2", node: "second" },
                    { condition: "kind == 3", node: "third" },
                    { condition: "kind == 4", node: "fourth" },
                ],
                default: "fallback",
            }) as unknown as WorkflowNodeConfig;
        const caseOrder = ["first", "second", "third", "fourth", "fallback"];

        test("targets sit left to right in case order then default, whatever the YAML node order", () => {
            const yamlOrder = [fourCases(), agentNode("third", ["router"]), agentNode("fallback", ["router"]), agentNode("first", ["router"]), agentNode("fourth", ["router"]), agentNode("second", ["router"])];
            expectLeftToRightOnOneRow(processWorkflowConfig(makeConfig(yamlOrder)), caseOrder);
        });

        test("targets without depends_on are ordered the same way", () => {
            const yamlOrder = [fourCases(), agentNode("fallback"), agentNode("fourth"), agentNode("second"), agentNode("third"), agentNode("first")];
            expectLeftToRightOnOneRow(processWorkflowConfig(makeConfig(yamlOrder)), caseOrder);
        });

        test("a target that also waits on another node keeps its place in case order", () => {
            const setup = agentNode("setup");
            const router = { ...fourCases(), depends_on: ["setup"] } as WorkflowNodeConfig;
            const yamlOrder = [setup, router, agentNode("third", ["router"]), agentNode("fallback", ["router"]), agentNode("first", ["router"]), agentNode("fourth", ["router"]), agentNode("second", ["router", "setup"])];
            expectLeftToRightOnOneRow(processWorkflowConfig(makeConfig(yamlOrder)), caseOrder);
        });

        test("a target that lists the switch twice keeps its place in case order", () => {
            const yamlOrder = [fourCases(), agentNode("third", ["router"]), agentNode("fallback", ["router"]), agentNode("first", ["router"]), agentNode("fourth", ["router"]), agentNode("second", ["router", "router"])];
            expectLeftToRightOnOneRow(processWorkflowConfig(makeConfig(yamlOrder)), caseOrder);
        });

        test("a dangling null in depends_on, as streamed YAML can produce, does not break the layout", () => {
            const dangling = { ...agentNode("done"), depends_on: ["first", "second", null] } as unknown as WorkflowNodeConfig;
            const yamlOrder = [fourCases(), agentNode("third", ["router"]), agentNode("fallback", ["router"]), agentNode("first", ["router"]), agentNode("fourth", ["router"]), agentNode("second", ["router"]), dangling];
            expect(() => processWorkflowConfig(makeConfig(yamlOrder))).not.toThrow();
        });

        test("a case target listed twice keeps the position of its first case", () => {
            const router = {
                id: "router",
                type: "switch",
                depends_on: [],
                cases: [
                    { condition: "kind == 1", node: "first" },
                    { condition: "kind == 2", node: "second" },
                    { condition: "kind == 3", node: "first" },
                ],
            } as unknown as WorkflowNodeConfig;
            const result = processWorkflowConfig(makeConfig([router, agentNode("second", ["router"]), agentNode("first", ["router"])]));
            expectLeftToRightOnOneRow(result, ["first", "second"]);
        });

        test("case pills read in case order and default last", () => {
            const yamlOrder = [fourCases(), agentNode("fallback", ["router"]), agentNode("third", ["router"]), agentNode("first", ["router"]), agentNode("fourth", ["router"]), agentNode("second", ["router"])];
            const result = processWorkflowConfig(makeConfig(yamlOrder));
            const pillsLeftToRight = result.nodes
                .filter(n => n.type === "condition")
                .sort((a, b) => a.x - b.x)
                .map(n => n.data.caseNumber);
            expect(pillsLeftToRight).toEqual([1, 2, 3, 4, 5]);
        });

        test("another node that depends on the switch sits right of every branch target, on the same row", () => {
            const mapNode = { id: "fan_out", type: "map", node: "inner", items: "$.items", depends_on: ["router"] } as unknown as WorkflowNodeConfig;
            const yamlOrder = [fourCases(), mapNode, agentNode("inner"), agentNode("fallback", ["router"]), agentNode("second", ["router"]), agentNode("first", ["router"]), agentNode("fourth", ["router"]), agentNode("third", ["router"])];
            const result = processWorkflowConfig(makeConfig(yamlOrder));
            expectLeftToRightOnOneRow(result, [...caseOrder, "fan_out"]);
        });
    });

    test("switch with no cases keeps the agent height", () => {
        const bare = { id: "router", type: "switch" } as WorkflowNodeConfig;
        const result = processWorkflowConfig(makeConfig([bare]));
        expect(nodeById(result, "router").height).toBe(NODE_HEIGHTS.AGENT);
    });
});

describe("decision node", () => {
    const decisionNode: WorkflowNodeConfig = {
        id: "triage",
        type: "decision",
        questions: [
            { key: "category", type: "choice", instructions: "Which team?", criteria: ["billing", "technical"] },
            { key: "urgent", type: "boolean" },
        ],
        route_on: "category",
        routes: { billing: "billing_desk", technical: "tech_desk" },
        default: "general_desk",
        escalate: "human_review",
        min_confidence: 0.8,
    };

    const nodes = [decisionNode, agentNode("billing_desk"), agentNode("tech_desk"), agentNode("general_desk"), agentNode("human_review")];

    test("decision node is sized for its questions and summary lines", () => {
        const result = processWorkflowConfig(makeConfig(nodes));
        const triage = nodeById(result, "triage");
        expect(triage.type).toBe("decision");
        expect(triage.width).toBe(NODE_WIDTHS.DECISION);
        expect(triage.height).toBe(118);
    });

    test("decision node data carries questions and routeOn", () => {
        const result = processWorkflowConfig(makeConfig(nodes));
        const triage = nodeById(result, "triage");
        expect(triage.data.questions?.map(q => [q.key, q.type])).toEqual([
            ["category", "choice"],
            ["urgent", "boolean"],
        ]);
        expect(triage.data.routeOn).toBe("category");
    });

    test("decision without questions or routing keeps the agent height", () => {
        const bare = { id: "triage", type: "decision" } as WorkflowNodeConfig;
        const result = processWorkflowConfig(makeConfig([bare]));
        const triage = nodeById(result, "triage");
        expect(triage.height).toBe(NODE_HEIGHTS.AGENT);
        expect(result.nodes.filter(n => n.type === "condition")).toHaveLength(0);
    });

    test("pills label each route answer, the default, and the escalation", () => {
        const result = processWorkflowConfig(makeConfig(nodes));
        const pills = result.nodes.filter(n => n.type === "condition").map(n => ({ target: n.data.targetNodeId, label: n.data.label, branchKind: n.data.branchKind, switchNodeId: n.data.switchNodeId }));
        expect(pills).toEqual([
            { target: "billing_desk", label: "billing", branchKind: "route", switchNodeId: "triage" },
            { target: "tech_desk", label: "technical", branchKind: "route", switchNodeId: "triage" },
            { target: "general_desk", label: "default", branchKind: "default", switchNodeId: "triage" },
            { target: "human_review", label: "escalate < 80% confidence", branchKind: "escalate", switchNodeId: "triage" },
        ]);
    });

    test("branch targets without depends_on hang below the decision through their pills", () => {
        const result = processWorkflowConfig(makeConfig(nodes));
        const triage = nodeById(result, "triage");
        for (const targetId of ["billing_desk", "tech_desk", "general_desk", "human_review"]) {
            const pillId = `__condition_triage_${targetId}__`;
            expect(result.edges.find(e => e.source === "triage" && e.target === pillId)).toBeDefined();
            expect(result.edges.find(e => e.source === pillId && e.target === targetId)).toBeDefined();
            expect(nodeById(result, targetId).y).toBe(triage.y + triage.height + SPACING.VERTICAL_BRANCH);
        }
        expect(result.edges.find(e => e.source === "__start__" && e.target === "human_review")).toBeUndefined();
    });

    describe("escalation_mode", () => {
        test("rerouting, the default, draws one return edge from the escalate target back to the decision", () => {
            const result = processWorkflowConfig(makeConfig(nodes));
            const answers = result.edges.filter(e => e.escalationAnswer);
            expect(answers.map(e => [e.source, e.target, e.answerSide])).toEqual([["human_review", "triage", "right"]]);
            expect(result.edges.find(e => e.source === "human_review" && e.target === "__end__")).toBeUndefined();
        });

        test("an escalate dependency the author listed on a route is drawn as the return edge, not as an edge into the route", () => {
            const waitsOnBoth = (id: string) => ({ ...agentNode(id), depends_on: ["triage", "human_review"] });
            const listed = [decisionNode, waitsOnBoth("billing_desk"), waitsOnBoth("tech_desk"), waitsOnBoth("general_desk"), { ...agentNode("human_review"), depends_on: ["triage"] }];
            const result = processWorkflowConfig(makeConfig(listed));
            for (const targetId of ["billing_desk", "tech_desk", "general_desk"]) {
                expect(result.edges.find(e => e.source === "human_review" && (e.target === targetId || e.target === `__condition_triage_${targetId}__`))).toBeUndefined();
            }
            expect(result.edges.filter(e => e.escalationAnswer)).toHaveLength(1);
        });

        test("routes that list the escalate target are laid out exactly like routes that leave it to the engine", () => {
            const waitsOnBoth = (id: string) => ({ ...agentNode(id), depends_on: ["triage", "human_review"] });
            const escalate = { ...agentNode("human_review"), depends_on: ["triage"] };
            const listed = processWorkflowConfig(makeConfig([decisionNode, waitsOnBoth("billing_desk"), waitsOnBoth("tech_desk"), waitsOnBoth("general_desk"), escalate]));
            const unlisted = processWorkflowConfig(makeConfig([decisionNode, agentNode("billing_desk"), agentNode("tech_desk"), agentNode("general_desk"), escalate]));
            const placement = (result: typeof listed) => result.nodes.map(n => [n.id, n.x, n.y]);
            expect(placement(listed)).toEqual(placement(unlisted));
            expect(nodeById(listed, "tech_desk").y).toBe(nodeById(listed, "human_review").y);
        });

        test("a route listing the escalate target keeps its place among its siblings next to another root", () => {
            const otherRoot = [agentNode("other"), { ...agentNode("o2"), depends_on: ["other"] }];
            const escalate = { ...agentNode("human_review"), depends_on: ["triage"] };
            const routes = [agentNode("billing_desk"), agentNode("tech_desk")];
            const listed = processWorkflowConfig(makeConfig([...otherRoot, decisionNode, ...routes, { ...agentNode("general_desk"), depends_on: ["triage", "human_review"] }, escalate]));
            const unlisted = processWorkflowConfig(makeConfig([...otherRoot, decisionNode, ...routes, agentNode("general_desk"), escalate]));
            const placement = (result: typeof listed) => result.nodes.map(n => [n.id, n.x, n.y]);
            expect(placement(listed)).toEqual(placement(unlisted));
        });

        const escalateTargets: Record<string, WorkflowNodeConfig> = {
            agent: agentNode("human_review"),
            tool: { id: "human_review", type: "tool", tool_name: "review_tool" } as WorkflowNodeConfig,
            workflow: { id: "human_review", type: "workflow", workflow_name: "ReviewFlow" } as WorkflowNodeConfig,
            decision: { id: "human_review", type: "decision", questions: [{ key: "category", type: "choice", criteria: ["billing", "technical"] }] } as WorkflowNodeConfig,
        };

        test.each(Object.keys(escalateTargets))("a %s escalate target, which the engine can reroute through, draws one return edge", type => {
            const withTarget = [decisionNode, agentNode("billing_desk"), agentNode("tech_desk"), agentNode("general_desk"), escalateTargets[type]];
            const result = processWorkflowConfig(makeConfig(withTarget));
            expect(result.edges.filter(e => e.escalationAnswer).map(e => [e.source, e.target])).toEqual([["human_review", "triage"]]);
        });

        test("an escalate target the engine cannot reroute through draws no return edge", () => {
            const toSwitch = [decisionNode, agentNode("billing_desk"), agentNode("tech_desk"), agentNode("general_desk"), { id: "human_review", type: "switch", cases: [], depends_on: ["triage"] } as unknown as WorkflowNodeConfig];
            const result = processWorkflowConfig(makeConfig(toSwitch));
            expect(result.edges.some(e => e.escalationAnswer)).toBe(false);
        });

        test("handoff draws no return edge, and the escalate target ends its branch", () => {
            const handoff: WorkflowNodeConfig = { ...decisionNode, escalation_mode: "handoff" };
            const plain = [handoff, agentNode("billing_desk"), agentNode("tech_desk"), agentNode("general_desk"), agentNode("human_review"), { ...agentNode("notify"), depends_on: ["human_review"] }];
            const result = processWorkflowConfig(makeConfig(plain));
            expect(result.edges.some(e => e.escalationAnswer)).toBe(false);
            expect(result.edges.find(e => e.source === "human_review" && e.target === "notify")).toBeDefined();
        });

        test("the escalate target sits right of every route and the default, whatever the YAML order, and its return edge climbs the right side", () => {
            const escalateFirst = [decisionNode, agentNode("human_review"), agentNode("general_desk"), agentNode("tech_desk"), agentNode("billing_desk")];
            const result = processWorkflowConfig(makeConfig(escalateFirst));
            const xs = ["billing_desk", "tech_desk", "general_desk", "human_review"].map(id => nodeById(result, id).x);
            expect(xs).toEqual([...xs].sort((a, b) => a - b));
            expect(result.edges.find(e => e.escalationAnswer)?.answerSide).toBe("right");
        });

        test("an escalate target that also waits on a node above the decision still sits right of every route, with its return edge on the right", () => {
            const downstream = { ...decisionNode, depends_on: ["intake"] } as WorkflowNodeConfig;
            const yamlOrder = [agentNode("intake"), downstream, agentNode("human_review", ["triage", "intake"]), agentNode("general_desk"), agentNode("tech_desk"), agentNode("billing_desk")];
            const result = processWorkflowConfig(makeConfig(yamlOrder));
            expectLeftToRightOnOneRow(result, ["billing_desk", "tech_desk", "general_desk", "human_review"]);
            expect(result.edges.find(e => e.escalationAnswer)?.answerSide).toBe("right");
        });

        test("a route that also waits on a node above the decision still sits left of the escalate target", () => {
            const downstream = { ...decisionNode, depends_on: ["intake"] } as WorkflowNodeConfig;
            const yamlOrder = [agentNode("intake"), downstream, agentNode("human_review"), agentNode("general_desk"), agentNode("tech_desk"), agentNode("billing_desk", ["triage", "intake"])];
            const result = processWorkflowConfig(makeConfig(yamlOrder));
            expectLeftToRightOnOneRow(result, ["billing_desk", "tech_desk", "general_desk", "human_review"]);
        });

        test("another node that depends on the decision sits after the default and before the escalate target", () => {
            const notify = agentNode("notify", ["triage"]);
            const yamlOrder = [decisionNode, agentNode("human_review"), notify, agentNode("general_desk"), agentNode("tech_desk"), agentNode("billing_desk")];
            const result = processWorkflowConfig(makeConfig(yamlOrder));
            expectLeftToRightOnOneRow(result, ["billing_desk", "tech_desk", "general_desk", "notify", "human_review"]);
        });

        test("a handoff escalate target also sits right of the routes", () => {
            const handoff: WorkflowNodeConfig = { ...decisionNode, escalation_mode: "handoff" };
            const result = processWorkflowConfig(makeConfig([handoff, agentNode("human_review"), agentNode("billing_desk"), agentNode("tech_desk"), agentNode("general_desk")]));
            const escalateX = nodeById(result, "human_review").x;
            for (const route of ["billing_desk", "tech_desk", "general_desk"]) {
                expect(nodeById(result, route).x).toBeLessThan(escalateX);
            }
        });
    });
});

describe("map container node", () => {
    const childNode = agentNode("child");
    const mapNode: WorkflowNodeConfig = {
        id: "mapper",
        type: "map",
        node: "child",
        items: "$.items",
        depends_on: [],
    } as unknown as WorkflowNodeConfig;

    test("map node contains child when not collapsed", () => {
        const result = processWorkflowConfig(makeConfig([mapNode, childNode]));
        const mapper = nodeById(result, "mapper");
        expect(mapper.children).toHaveLength(1);
        expect(mapper.children[0].id).toBe("child");
    });

    test("map node has no children when collapsed", () => {
        const result = processWorkflowConfig(makeConfig([mapNode, childNode]), new Set(["mapper"]));
        const mapper = nodeById(result, "mapper");
        expect(mapper.children).toHaveLength(0);
    });

    test("collapsed map node uses SWITCH_COLLAPSED width", () => {
        const result = processWorkflowConfig(makeConfig([mapNode, childNode]), new Set(["mapper"]));
        expect(nodeById(result, "mapper").width).toBe(NODE_WIDTHS.SWITCH_COLLAPSED);
    });

    test("child node is not rendered as a top-level node", () => {
        const result = processWorkflowConfig(makeConfig([mapNode, childNode]));
        expect(result.nodes.find(n => n.id === "child")).toBeUndefined();
    });
});

describe("loop container node", () => {
    const childNode = agentNode("loop-child");
    const loopNode: WorkflowNodeConfig = {
        id: "looper",
        type: "loop",
        node: "loop-child",
        condition: "$.done == false",
        max_iterations: 10,
        depends_on: [],
    } as unknown as WorkflowNodeConfig;

    test("loop node height includes condition row when expanded", () => {
        const result = processWorkflowConfig(makeConfig([loopNode, childNode]));
        const looper = nodeById(result, "looper");
        expect(looper.height).toBeGreaterThan(NODE_HEIGHTS.CONTAINER_HEADER);
    });

    test("collapsed loop node uses CONTAINER_HEADER height", () => {
        const result = processWorkflowConfig(makeConfig([loopNode, childNode]), new Set(["looper"]));
        expect(nodeById(result, "looper").height).toBe(NODE_HEIGHTS.CONTAINER_HEADER);
    });

    test("loop node data carries condition, maxIterations, and childNodeId", () => {
        const result = processWorkflowConfig(makeConfig([loopNode, childNode]));
        const node = nodeById(result, "looper");
        expect(node.data.condition).toBe("$.done == false");
        expect(node.data.maxIterations).toBe(10);
        expect(node.data.childNodeId).toBe("loop-child");
    });

    test("loop node contains child when not collapsed", () => {
        const result = processWorkflowConfig(makeConfig([loopNode, childNode]));
        const looper = nodeById(result, "looper");
        expect(looper.children).toHaveLength(1);
        expect(looper.children[0].id).toBe("loop-child");
        expect(result.nodes.find(n => n.id === "loop-child")).toBeUndefined();
    });
});

describe("switch or decision inside a map or loop", () => {
    const START_EDGE = "__start__-start";
    const START_KEY_IS_A = "start.output.k == 'a'";
    type ContainerKind = "map" | "loop";
    type Layout = ReturnType<typeof processWorkflowConfig>;

    interface ExpectedPill {
        target: string;
        label: string;
        caseNumber?: number;
        isDefaultCase: boolean;
        branchKind?: "route" | "default" | "escalate";
    }

    interface RoutedBodyCase {
        name: string;
        nodes: WorkflowNodeConfig[];
        container: string;
        body: string;
        pills: ExpectedPill[];
        topLevel: WorkflowNodeConfig[];
    }

    const containerNode = (id: string, kind: ContainerKind, body: string, depends_on: string[]): WorkflowNodeConfig =>
        (kind === "map" ? { id, type: "map", node: body, items: "$.items", depends_on } : { id, type: "loop", node: body, condition: "$.done == false", max_iterations: 3, depends_on }) as unknown as WorkflowNodeConfig;

    const switchBody = (id: string, cases: Array<[string, string]>, defaultTarget?: string): WorkflowNodeConfig =>
        ({ id, type: "switch", cases: cases.map(([condition, node]) => ({ condition, node })), ...(defaultTarget ? { default: defaultTarget } : {}) }) as unknown as WorkflowNodeConfig;

    const decisionBody = (id: string, routes: Record<string, string>, defaultTarget: string, escalate?: string): WorkflowNodeConfig =>
        ({
            id,
            type: "decision",
            questions: [{ key: "category", type: "choice", criteria: Object.keys(routes) }],
            route_on: "category",
            routes,
            default: defaultTarget,
            ...(escalate ? { escalate, escalation_mode: "handoff", min_confidence: 0.8 } : {}),
        }) as unknown as WorkflowNodeConfig;

    const casePill = (target: string, label: string, caseNumber: number): ExpectedPill => ({ target, label, caseNumber, isDefaultCase: false });
    const defaultPill = (target: string, caseNumber: number): ExpectedPill => ({ target, label: "default", caseNumber, isDefaultCase: true });

    interface Shape {
        name: string;
        kind: ContainerKind;
        body: WorkflowNodeConfig;
        pills: ExpectedPill[];
        targetsDependOnBody?: boolean;
        joinTargets?: boolean;
        outerLoop?: boolean;
    }

    function routedBodyCase(shape: Shape): RoutedBodyCase {
        const { body, pills, kind } = shape;
        const targets = pills.map(pill => agentNode(pill.target, shape.targetsDependOnBody ? [body.id] : []));
        const join = shape.joinTargets
            ? [
                  agentNode(
                      "join",
                      pills.map(pill => pill.target)
                  ),
              ]
            : [];
        const container = shape.outerLoop ? "outer" : "box";
        const containers = shape.outerLoop ? [containerNode("outer", "loop", "box", ["start"]), containerNode("box", kind, body.id, [])] : [containerNode("box", kind, body.id, ["start"])];
        const topLevelBody = { ...body, depends_on: ["start"] } as WorkflowNodeConfig;
        return {
            name: shape.name,
            nodes: [agentNode("start"), ...containers, body, ...targets, ...join],
            container,
            body: body.id,
            pills,
            topLevel: [agentNode("start"), topLevelBody, ...targets, ...join],
        };
    }

    const routedBodies: RoutedBodyCase[] = [
        routedBodyCase({ name: "a loop whose body is a switch with one case and a default", kind: "loop", body: switchBody("sw", [[START_KEY_IS_A, "ha"]], "hb"), pills: [casePill("ha", START_KEY_IS_A, 1), defaultPill("hb", 2)] }),
        routedBodyCase({ name: "a map whose body is a switch with one case and a default", kind: "map", body: switchBody("sw", [[START_KEY_IS_A, "ha"]], "hb"), pills: [casePill("ha", START_KEY_IS_A, 1), defaultPill("hb", 2)] }),
        routedBodyCase({
            name: "a loop whose body is a switch with several cases and a default",
            kind: "loop",
            body: switchBody(
                "sw",
                [
                    ["k == 1", "one"],
                    ["k == 2", "two"],
                    ["k == 3", "three"],
                ],
                "other"
            ),
            pills: [casePill("one", "k == 1", 1), casePill("two", "k == 2", 2), casePill("three", "k == 3", 3), defaultPill("other", 4)],
        }),
        routedBodyCase({
            name: "a map whose body is a switch with cases and no default",
            kind: "map",
            body: switchBody("sw", [
                ["k == 1", "one"],
                ["k == 2", "two"],
            ]),
            pills: [casePill("one", "k == 1", 1), casePill("two", "k == 2", 2)],
        }),
        routedBodyCase({
            name: "a switch body whose targets list it in depends_on",
            kind: "loop",
            body: switchBody(
                "sw",
                [
                    ["k == 1", "one"],
                    ["k == 2", "two"],
                ],
                "other"
            ),
            pills: [casePill("one", "k == 1", 1), casePill("two", "k == 2", 2), defaultPill("other", 3)],
            targetsDependOnBody: true,
        }),
        routedBodyCase({
            name: "a switch body whose branches rejoin",
            kind: "map",
            body: switchBody(
                "sw",
                [
                    ["k == 1", "one"],
                    ["k == 2", "two"],
                ],
                "other"
            ),
            pills: [casePill("one", "k == 1", 1), casePill("two", "k == 2", 2), defaultPill("other", 3)],
            joinTargets: true,
        }),
        routedBodyCase({ name: "a map nested in a loop whose body is a switch", kind: "map", outerLoop: true, body: switchBody("sw", [["k == 1", "one"]], "other"), pills: [casePill("one", "k == 1", 1), defaultPill("other", 2)] }),
        routedBodyCase({
            name: "a loop whose body is a decision with routes and a default",
            kind: "loop",
            body: decisionBody("pick", { billing: "billing_desk", technical: "tech_desk" }, "general_desk"),
            pills: [
                { target: "billing_desk", label: "billing", isDefaultCase: false, branchKind: "route" },
                { target: "tech_desk", label: "technical", isDefaultCase: false, branchKind: "route" },
                { target: "general_desk", label: "default", isDefaultCase: false, branchKind: "default" },
            ],
        }),
        routedBodyCase({
            name: "a map whose body is a decision that hands off on low confidence",
            kind: "map",
            body: decisionBody("pick", { billing: "billing_desk" }, "general_desk", "human"),
            pills: [
                { target: "billing_desk", label: "billing", isDefaultCase: false, branchKind: "route" },
                { target: "general_desk", label: "default", isDefaultCase: false, branchKind: "default" },
                { target: "human", label: "escalate < 80% confidence", isDefaultCase: false, branchKind: "escalate" },
            ],
        }),
    ];

    const pillsOf = (result: Layout) => result.nodes.filter(n => n.type === "condition");
    const pillSummary = (result: Layout) => pillsOf(result).map(n => ({ target: n.data.targetNodeId, label: n.data.label, caseNumber: n.data.caseNumber, isDefaultCase: n.data.isDefaultCase, branchKind: n.data.branchKind }));
    const expectedSummary = (pills: ExpectedPill[]) => pills.map(pill => ({ caseNumber: undefined, branchKind: undefined, ...pill }));

    describe.each(routedBodies)("$name", ({ nodes, container, body, pills, topLevel }) => {
        const layout = () => processWorkflowConfig(makeConfig(structuredClone(nodes)));

        test("draws one labelled pill per branch, in branch order, on the container", () => {
            const result = layout();
            expect(pillSummary(result)).toEqual(expectedSummary(pills));
            for (const pill of pillsOf(result)) {
                expect(pill.id).toBe(`__condition_${container}_${pill.data.targetNodeId}__`);
                expect(pill.data.switchNodeId).toBe(container);
            }
        });

        test("labels the pills as it does for the same router outside a container", () => {
            expect(pillSummary(layout())).toEqual(pillSummary(processWorkflowConfig(makeConfig(structuredClone(topLevel)))));
        });

        test("runs one edge from the container into each pill and one straight edge from the pill to its target", () => {
            const result = layout();
            for (const { target } of pills) {
                const pillId = `__condition_${container}_${target}__`;
                expect(result.edges.filter(e => e.target === pillId)).toEqual([expect.objectContaining({ source: container })]);
                expect(result.edges.filter(e => e.source === pillId)).toEqual([expect.objectContaining({ target, isStraight: true })]);
                expect(result.edges.filter(e => e.source === container && e.target === target)).toHaveLength(0);
            }
        });

        test("keeps every edge on a top-level node, none on the body inside the container", () => {
            const result = layout();
            const topLevelIds = new Set(result.nodes.map(n => n.id));
            for (const edge of result.edges) {
                expect(topLevelIds.has(edge.source), `${edge.id} source`).toBe(true);
                expect(topLevelIds.has(edge.target), `${edge.id} target`).toBe(true);
            }
            expect(result.edges.filter(e => e.source === body || e.target === body)).toHaveLength(0);
        });

        test("keeps the body drawn inside the container and out of the top-level nodes", () => {
            const result = layout();
            expect(nodeById(result, container).children).toHaveLength(1);
            expect(result.nodes.find(n => n.id === body)).toBeUndefined();
        });

        test("hangs the targets below the container with the room a top-level router gives its pills", () => {
            const result = layout();
            const box = nodeById(result, container);
            for (const { target } of pills) {
                const targetNode = nodeById(result, target);
                const pill = nodeById(result, `__condition_${container}_${target}__`);
                expect(targetNode.y).toBe(box.y + box.height + SPACING.VERTICAL_BRANCH);
                expect(pill.y).toBeGreaterThan(box.y + box.height);
                expect(pill.y + pill.height).toBeLessThan(targetNode.y);
                expect(pill.x + pill.width / 2).toBeCloseTo(targetNode.x + targetNode.width / 2);
            }
        });

        test("sits the targets left to right in branch order, whatever the YAML node order", () => {
            const targets = pills.map(pill => pill.target);
            expectLeftToRightOnOneRow(layout(), targets);
            expectLeftToRightOnOneRow(processWorkflowConfig(makeConfig(structuredClone(nodes).reverse())), targets);
        });

        test("still draws the pills and their edges when the container is collapsed", () => {
            const result = processWorkflowConfig(makeConfig(structuredClone(nodes)), new Set([container]));
            expect(pillSummary(result)).toEqual(expectedSummary(pills));
            expect(result.edges.filter(e => e.source === container).map(e => e.target)).toEqual(pills.map(pill => `__condition_${container}_${pill.target}__`));
        });
    });

    test("leaves a container whose body does not route unchanged: no pills, edges straight to its dependents", () => {
        const nodes = [agentNode("start"), containerNode("box", "loop", "work", ["start"]), agentNode("work"), agentNode("after", ["box"])];
        const result = processWorkflowConfig(makeConfig(nodes));
        expect(pillsOf(result)).toHaveLength(0);
        expect(result.edges.map(e => e.id)).toEqual([START_EDGE, "start-box", "box-after", "after-__end__"]);
    });

    test("sends the edges of a body nested two containers deep to the outermost container, whatever the node order", () => {
        const body = switchBody("sw", [["k == 1", "one"]], "other");
        const outer = containerNode("outer", "loop", "inner", ["start"]);
        const inner = containerNode("inner", "map", "sw", []);
        const orders = [
            [agentNode("start"), outer, inner, body, agentNode("one"), agentNode("other")],
            [agentNode("one"), agentNode("other"), body, inner, outer, agentNode("start")],
        ];
        for (const order of orders) {
            const result = processWorkflowConfig(makeConfig(order));
            expect(result.edges.filter(e => e.source === "outer").map(e => e.target)).toEqual(["__condition_outer_one__", "__condition_outer_other__"]);
            expect(result.nodes.find(n => n.id === "one")).toBeDefined();
        }
    });

    describe("a body that routes back to a node the container waits on or is", () => {
        const decisionRoutingTo = (target: string) => decisionBody("pick", { x: target }, "other");
        const cases: Array<{ name: string; build: () => WorkflowNodeConfig[]; backTarget: string }> = [
            { name: "a switch case to a node the container waits on", backTarget: "start", build: () => [agentNode("start"), containerNode("box", "loop", "sw", ["start"]), switchBody("sw", [["k == 1", "start"]], "other"), agentNode("other")] },
            { name: "a switch case to its own container", backTarget: "box", build: () => [agentNode("start"), containerNode("box", "loop", "sw", ["start"]), switchBody("sw", [["k == 1", "box"]], "other"), agentNode("other")] },
            {
                name: "a switch case to the outer container of a nested pair",
                backTarget: "outer",
                build: () => [agentNode("start"), containerNode("outer", "loop", "inner", ["start"]), containerNode("inner", "map", "sw", []), switchBody("sw", [["k == 1", "outer"]], "other"), agentNode("other")],
            },
            {
                name: "a switch case to a node two levels up",
                backTarget: "first",
                build: () => [agentNode("first"), agentNode("start", ["first"]), containerNode("box", "loop", "sw", ["start"]), switchBody("sw", [["k == 1", "first"]], "other"), agentNode("other")],
            },
            { name: "a decision route to a node the container waits on", backTarget: "start", build: () => [agentNode("start"), containerNode("box", "map", "pick", ["start"]), decisionRoutingTo("start"), agentNode("other")] },
            { name: "a decision route to its own container", backTarget: "box", build: () => [agentNode("start"), containerNode("box", "map", "pick", ["start"]), decisionRoutingTo("box"), agentNode("other")] },
        ];

        test.each(cases)("draws no pill for $name, and no pill without edges", ({ build, backTarget }) => {
            const result = processWorkflowConfig(makeConfig(build()));
            expect(pillsOf(result).map(pill => pill.data.targetNodeId)).not.toContain(backTarget);
            expect(pillsOf(result).map(pill => pill.data.targetNodeId)).toContain("other");
            for (const pill of pillsOf(result)) {
                expect(result.edges.filter(e => e.target === pill.id)).toHaveLength(1);
                expect(result.edges.filter(e => e.source === pill.id)).toHaveLength(1);
            }
        });
    });

    test("keeps case order for branch targets that also wait on another node, in either node order", () => {
        const build = () => [agentNode("start"), agentNode("other"), containerNode("box", "loop", "sw", ["start"]), switchBody("sw", [["k == 1", "ha"]], "hb"), agentNode("hb", ["other"]), agentNode("ha", ["other"])];
        expectLeftToRightOnOneRow(processWorkflowConfig(makeConfig(build())), ["ha", "hb"]);
        expectLeftToRightOnOneRow(processWorkflowConfig(makeConfig(build().reverse())), ["ha", "hb"]);
    });

    test("hands the dependents of a nested body that does not route to the outermost container, whatever the node order", () => {
        const build = () => [agentNode("start"), containerNode("outer", "map", "inner", ["start"]), containerNode("inner", "loop", "work", []), agentNode("work"), agentNode("after", ["work"])];
        for (const order of [build(), build().reverse()]) {
            const result = processWorkflowConfig(makeConfig(order));
            expect(result.edges.filter(e => e.source === "outer").map(e => e.target)).toEqual(["after"]);
            expect(nodeById(result, "after").y).toBeGreaterThan(nodeById(result, "outer").y);
        }
    });

    describe("containers that name themselves or each other as their body", () => {
        const sw = () => switchBody("s", [["k == 1", "a"]], "z");
        const entry = (kind: ContainerKind, collapsed: string[] = [], ring: ContainerKind = "loop") => ({
            collapsed,
            build: () => [agentNode("start"), containerNode("entry", kind, "a", ["start"]), containerNode("a", ring, "b", []), containerNode("b", ring, "a", [])],
        });
        const plain = ["__start__", "start", "end", "__end__"];
        const plainEdges = [START_EDGE, "start-end", "end-__end__"];
        const entryEdges = [START_EDGE, "start-entry", "entry-__end__"];
        const cases: Array<{ name: string; build: () => WorkflowNodeConfig[]; collapsed?: string[]; nodes?: string[]; edges?: string[] }> = [
            {
                name: "two maps naming each other, with a node depending on one",
                build: () => [agentNode("start"), containerNode("a", "map", "b", []), containerNode("b", "map", "a", []), agentNode("end", ["start", "a"])],
                nodes: plain,
                edges: plainEdges,
            },
            {
                name: "two loops naming each other, with a node depending on the other",
                build: () => [agentNode("start"), containerNode("a", "loop", "b", []), containerNode("b", "loop", "a", []), agentNode("end", ["start", "b"])],
                nodes: plain,
                edges: plainEdges,
            },
            { name: "a loop that is its own body", build: () => [agentNode("start"), containerNode("a", "loop", "a", []), agentNode("end", ["start", "a"])], nodes: plain, edges: plainEdges },
            { name: "a map that is its own body", build: () => [agentNode("start"), containerNode("a", "map", "a", []), agentNode("end", ["start", "a"])], nodes: plain, edges: plainEdges },
            { name: "a self-containing loop that waits on a node", build: () => [agentNode("start"), containerNode("a", "loop", "a", ["start"]), agentNode("end", ["start", "a"])], nodes: plain, edges: plainEdges },
            {
                name: "three loops in a ring",
                build: () => [agentNode("start"), containerNode("a", "loop", "b", []), containerNode("b", "loop", "c", []), containerNode("c", "loop", "a", []), agentNode("end", ["start", "a"])],
                nodes: plain,
                edges: plainEdges,
            },
            {
                name: "a switch routing to a loop ring",
                build: () => [agentNode("start", []), { ...sw(), depends_on: ["start"] } as WorkflowNodeConfig, containerNode("a", "loop", "b", []), containerNode("b", "loop", "a", []), agentNode("z"), agentNode("end", ["a", "z"])],
                nodes: ["__start__", "start", "s", "z", "end", "__end__", "__condition_s_z__"],
                edges: [START_EDGE, "start-s", "s-__condition_s_z__", "__condition_s_z__-z", "z-end", "end-__end__"],
            },
            { name: "an entry loop over a ring", ...entry("loop"), nodes: ["__start__", "start", "entry[a(0)]", "__end__"], edges: entryEdges },
            { name: "an entry map over a ring of maps", ...entry("map", [], "map"), nodes: ["__start__", "start", "entry[a(0)]", "__end__"], edges: entryEdges },
            { name: "a collapsed entry loop over a ring", ...entry("loop", ["entry"]), nodes: ["__start__", "start", "entry", "__end__"], edges: entryEdges },
            {
                name: "two loops naming each other, with a third loop over one of them that waits on both",
                build: () => [containerNode("n0", "loop", "n1", ["n0"]), containerNode("n1", "loop", "n0", ["n1"]), containerNode("n2", "loop", "n0", ["n0", "n1"])],
            },
            {
                name: "a self-containing loop, and a map over it that a loop over the map waits on",
                build: () => [containerNode("n1", "loop", "n1", []), containerNode("n2", "loop", "n3", ["n1", "n3"]), containerNode("n3", "map", "n1", [])],
            },
        ];
        const withChildren = (n: Layout["nodes"][number]) => (n.children.length > 0 ? `${n.id}[${n.children.map(child => child.id + "(" + child.children.length + ")").join(",")}]` : n.id);
        const shape = (result: Layout) => ({ nodes: result.nodes.map(withChildren), edges: result.edges.map(e => e.id) });

        test.each(cases)("terminates and draws what it can: $name", ({ build, collapsed, nodes, edges }) => {
            const drawn = shape(processWorkflowConfig(makeConfig(build()), new Set(collapsed ?? [])));
            if (nodes) expect(drawn.nodes).toEqual(nodes);
            if (edges) expect([...drawn.edges].sort((left, right) => left.localeCompare(right))).toEqual([...edges].sort((left, right) => left.localeCompare(right)));
        });
    });

    test("gives only the container that owns a shared body the room for its pills, whatever the node order", () => {
        const build = () => [agentNode("start"), containerNode("c1", "map", "sw", ["start"]), agentNode("mid", ["c1"]), containerNode("c2", "map", "sw", ["mid"]), switchBody("sw", [["k == 1", "ha"]], "hb"), agentNode("ha"), agentNode("hb")];
        const gapBelow = (result: Layout, id: string) => {
            const node = nodeById(result, id);
            const bottom = node.y + node.height;
            return Math.min(...result.nodes.filter(n => n.type !== "condition" && n.y >= bottom).map(n => n.y)) - bottom;
        };
        for (const order of [build(), build().reverse()]) {
            const result = processWorkflowConfig(makeConfig(order));
            const owner = pillsOf(result)[0].data.switchNodeId;
            expect(gapBelow(result, owner === "c1" ? "c2" : "c1")).toBe(SPACING.VERTICAL);
        }
    });

    test("orders the targets of a shared body by case when they wait on the container that does not own it, whatever the node order", () => {
        const build = () => [agentNode("start"), containerNode("c1", "map", "sw", ["start"]), containerNode("c2", "map", "sw", ["start"]), switchBody("sw", [["k == 1", "ha"]], "hb"), agentNode("hb", ["c1"]), agentNode("ha", ["c1"])];
        for (const order of [build(), build().reverse()]) {
            expectLeftToRightOnOneRow(processWorkflowConfig(makeConfig(order)), ["ha", "hb"]);
        }
    });

    describe("seeded random container graphs", () => {
        const SEEDS = 400;

        function seededRandom(seed: number): () => number {
            let state = seed;
            return () => {
                state = (state + 0x6d2b79f5) >>> 0;
                let t = state;
                t = Math.imul(t ^ (t >>> 15), t | 1);
                t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
                return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
            };
        }

        function randomGraph(seed: number): { nodes: WorkflowNodeConfig[]; plainAgents: string[] } {
            const random = seededRandom(seed);
            const agentCount = 2 + Math.floor(random() * 3);
            const routerCount = 2 + Math.floor(random() * 4);
            const routerIds = Array.from({ length: routerCount }, (_, index) => `r${index}`);
            const anyId = (): string => {
                const ids = [...routerIds, ...Array.from({ length: agentCount }, (_, index) => `a${index}`)];
                return ids[Math.floor(random() * ids.length)];
            };
            const waits = (ids: string[]) => ids.filter(() => random() < 0.3);
            const agents = Array.from({ length: agentCount }, (_, index) => agentNode(`a${index}`, waits(Array.from({ length: index }, (_, lower) => `a${lower}`))));
            const routers = routerIds.map((id): WorkflowNodeConfig => {
                const kind = random();
                const depends_on = waits(routerIds);
                if (kind < 0.35) return { ...containerNode(id, "loop", anyId(), depends_on) };
                if (kind < 0.7) return { ...containerNode(id, "map", anyId(), depends_on) };
                return {
                    ...switchBody(
                        id,
                        [
                            ["k == 1", anyId()],
                            ["k == 2", anyId()],
                        ],
                        anyId()
                    ),
                    depends_on,
                };
            });
            const referenced = new Set(routers.flatMap(router => [router.node, router.default, ...(router.cases ?? []).map(branch => branch.node)]));
            const plainAgents = agents.filter(agent => !referenced.has(agent.id) && agent.depends_on?.every(id => !referenced.has(id))).map(agent => agent.id);
            return { nodes: [...agents, ...routers], plainAgents };
        }

        test(`lays out ${SEEDS} graphs without throwing, with unique edge ids and a pill with one edge in and out, and draws the agents nothing routes to or waits behind`, () => {
            for (let seed = 1; seed <= SEEDS; seed++) {
                const { nodes, plainAgents } = randomGraph(seed);
                const result = processWorkflowConfig(makeConfig(structuredClone(nodes)));
                const drawn = new Set(result.nodes.map(n => n.id));
                const flattened = new Set(
                    result.nodes.flatMap(function ids(n: Layout["nodes"][number]): string[] {
                        return [n.id, ...n.children.flatMap(ids)];
                    })
                );
                for (const edge of result.edges) expect(flattened.has(edge.source) && flattened.has(edge.target), `seed ${seed}: ${edge.id} joins drawn nodes`).toBe(true);
                expect(new Set(result.edges.map(e => e.id)).size, `seed ${seed} duplicate edge ids`).toBe(result.edges.length);
                for (const id of plainAgents) expect(drawn.has(id), `seed ${seed}: ${id} is drawn`).toBe(true);
                for (const pill of pillsOf(result)) {
                    expect(
                        result.edges.filter(e => e.target === pill.id),
                        `seed ${seed}: ${pill.id} in`
                    ).toHaveLength(1);
                    expect(
                        result.edges.filter(e => e.source === pill.id),
                        `seed ${seed}: ${pill.id} out`
                    ).toHaveLength(1);
                }
            }
        });
    });

    describe("branches and bodies that are still being written", () => {
        const halfWrittenCases: Array<{ name: string; condition: unknown }> = [
            { name: "no condition yet", condition: undefined },
            { name: "a null condition", condition: null },
            { name: "a number as the condition", condition: 7 },
            { name: "an object as the condition", condition: {} },
        ];
        const bodySwitch = (condition: unknown) =>
            ({
                id: "sw",
                type: "switch",
                cases: [
                    { condition, node: "d" },
                    { condition: "k == 2", node: "e" },
                ],
            }) as unknown as WorkflowNodeConfig;

        test.each(halfWrittenCases)("draws a finite pill for a body switch case with $name", ({ condition }) => {
            const result = processWorkflowConfig(makeConfig([agentNode("start"), containerNode("box", "loop", "sw", ["start"]), bodySwitch(condition), agentNode("d"), agentNode("e")]));
            const pill = nodeById(result, "__condition_box_d__");
            for (const value of [pill.x, pill.y, pill.width, pill.height]) expect(Number.isFinite(value)).toBe(true);
            expect(pill.data.label).toBe("");
            expect(result.edges.every(e => [e.sourceX, e.sourceY, e.targetX, e.targetY].every(Number.isFinite))).toBe(true);
        });

        test.each(halfWrittenCases)("draws a finite pill for a top-level switch case with $name", ({ condition }) => {
            const result = processWorkflowConfig(makeConfig([agentNode("start"), { ...bodySwitch(condition), depends_on: ["start"] } as WorkflowNodeConfig, agentNode("d"), agentNode("e")]));
            const pill = nodeById(result, "__condition_sw_d__");
            for (const value of [pill.x, pill.y, pill.width]) expect(Number.isFinite(value)).toBe(true);
        });

        test("draws a map or loop whose body is missing or not named yet, with a node waiting on it", () => {
            const loopUnnamed = "loop-unnamed";
            const build = () => [
                agentNode("start"),
                containerNode("dangling", "map", "nowhere", ["start"]),
                { id: "unnamed", type: "map", depends_on: ["start"] } as unknown as WorkflowNodeConfig,
                { id: loopUnnamed, type: "loop", depends_on: ["start"] } as unknown as WorkflowNodeConfig,
                agentNode("after", ["dangling", "unnamed", loopUnnamed]),
            ];
            for (const order of [build(), build().reverse()]) {
                const result = processWorkflowConfig(makeConfig(order));
                for (const id of ["dangling", "unnamed", loopUnnamed, "after"]) expect(nodeById(result, id).children, id).toHaveLength(0);
                expect(result.edges.filter(e => e.target === "after")).toHaveLength(3);
            }
        });
    });

    test("draws a body shared by two containers under both, with one set of pills for the container that owns it, whatever the node order", () => {
        const build = () => [agentNode("start"), containerNode("first", "map", "sw", ["start"]), containerNode("second", "map", "sw", ["start"]), switchBody("sw", [["k == 1", "one"]], "other"), agentNode("one"), agentNode("other")];
        const owners = ["second", "first"];
        for (const [index, order] of [build(), build().reverse()].entries()) {
            const result = processWorkflowConfig(makeConfig(order));
            const pills = pillsOf(result);
            expect(pills.map(pill => pill.data.switchNodeId)).toEqual([owners[index], owners[index]]);
            for (const container of ["first", "second"]) expect(nodeById(result, container).children.map(child => child.id)).toEqual(["sw"]);
            expect(pills).toHaveLength(2);
            for (const pill of pills) {
                expect(result.edges.filter(e => e.target === pill.id)).toHaveLength(1);
                expect(result.edges.filter(e => e.source === pill.id)).toHaveLength(1);
            }
        }
    });
    describe("the escalation return edge of a decision inside a map or loop", () => {
        const rerouting = (escalate: unknown = "human", extra: Record<string, unknown> = {}): WorkflowNodeConfig =>
            ({ ...decisionBody("pick", { billing: "billing_desk", technical: "tech_desk" }, "general_desk"), escalate, min_confidence: 0.8, ...extra }) as unknown as WorkflowNodeConfig;
        const desks = () => ["billing_desk", "tech_desk", "general_desk", "human"].map(id => agentNode(id));
        const answerEdges = (result: Layout) => result.edges.filter(e => e.escalationAnswer);
        const pairs = (result: Layout) => answerEdges(result).map(e => [e.source, e.target]);
        const layoutOf = (nodes: WorkflowNodeConfig[], collapsed: string[] = []) => processWorkflowConfig(makeConfig(structuredClone(nodes)), new Set(collapsed));
        const topLevelPick = (): WorkflowNodeConfig => ({ ...rerouting(), depends_on: ["start"] }) as WorkflowNodeConfig;
        const loopOverPick = () => [agentNode("start"), containerNode("lp", "loop", "pick", ["start"])];
        const loopOverMapOverPick = () => [agentNode("start"), containerNode("lp", "loop", "mp", ["start"]), containerNode("mp", "map", "pick", [])];

        interface AnswerCase {
            name: string;
            build: () => WorkflowNodeConfig[];
            collapsed?: string[];
            expected: string[][];
        }

        const cases: AnswerCase[] = [
            { name: "a loop whose body is the decision", build: () => [...loopOverPick(), rerouting(), ...desks()], expected: [["human", "lp"]] },
            { name: "a map whose body is the decision", build: () => [agentNode("start"), containerNode("mp", "map", "pick", ["start"]), rerouting(), ...desks()], expected: [["human", "mp"]] },
            { name: "a map nested in a loop, the decision in the map", build: () => [...loopOverMapOverPick(), rerouting(), ...desks()], expected: [["human", "lp"]] },
            {
                name: "a loop in a map in a loop, whatever the node order",
                build: () => [...desks(), rerouting(), containerNode("in", "loop", "pick", []), containerNode("mid", "map", "in", []), containerNode("out", "loop", "mid", ["start"]), agentNode("start")],
                expected: [["human", "out"]],
            },
            { name: "a collapsed loop", build: () => [...loopOverPick(), rerouting(), ...desks()], collapsed: ["lp"], expected: [["human", "lp"]] },
            { name: "a collapsed outer loop over a map", build: () => [...loopOverMapOverPick(), rerouting(), ...desks()], collapsed: ["lp"], expected: [["human", "lp"]] },
            { name: "a collapsed map in an expanded loop", build: () => [...loopOverMapOverPick(), rerouting(), ...desks()], collapsed: ["mp"], expected: [["human", "lp"]] },
            {
                name: "an escalate target that is itself the body of another container",
                build: () => [...loopOverPick(), containerNode("review", "map", "human", ["start"]), rerouting(), ...desks()],
                expected: [["review", "lp"]],
            },
            { name: "a decision that escalates to itself inside its container", build: () => [...loopOverPick(), rerouting("pick"), ...desks()], expected: [] },
            { name: "a decision in a loop that hands off", build: () => [...loopOverPick(), rerouting("human", { escalation_mode: "handoff" }), ...desks()], expected: [] },
            { name: "a decision in a loop with an unknown escalation_mode", build: () => [...loopOverPick(), rerouting("human", { escalation_mode: "reroutee" }), ...desks()], expected: [] },
            { name: "a decision in a loop with no escalate target", build: () => [...loopOverPick(), rerouting("human", { escalate: undefined }), ...desks()], expected: [] },
            { name: "a decision in a loop that escalates to a node that does not exist", build: () => [...loopOverPick(), rerouting("nowhere"), ...desks()], expected: [] },
            {
                name: "a decision in a loop that escalates to a node that cannot answer",
                build: () => [...loopOverPick(), rerouting("gate"), switchBody("gate", [["k == 1", "billing_desk"]]), ...desks().slice(0, 3)],
                expected: [],
            },
            { name: "a decision at the top level", build: () => [agentNode("start"), topLevelPick(), ...desks()], expected: [["human", "pick"]] },
            { name: "a decision that escalates to itself at the top level", build: () => [agentNode("start"), { ...rerouting("pick"), depends_on: ["start"] } as WorkflowNodeConfig, ...desks()], expected: [["pick", "pick"]] },
            {
                name: "a decision at the top level and one in a loop that escalate to the same node",
                build: () => [
                    ...loopOverPick(),
                    rerouting(),
                    { ...rerouting(), id: "top_pick", routes: { billing: "b2", technical: "t2" }, default: "g2", depends_on: ["start"] } as WorkflowNodeConfig,
                    ...desks(),
                    agentNode("b2"),
                    agentNode("t2"),
                    agentNode("g2"),
                ],
                expected: [
                    ["human", "lp"],
                    ["human", "top_pick"],
                ],
            },
            {
                name: "two loops that share the decision as their body",
                build: () => [agentNode("start"), containerNode("c1", "loop", "pick", ["start"]), containerNode("c2", "loop", "pick", ["start"]), rerouting(), ...desks()],
                expected: [["human", "c2"]],
            },
        ];

        test.each(cases)("draws the return edges $expected for $name", ({ build, collapsed, expected }) => {
            expect(pairs(layoutOf(build(), collapsed))).toEqual(expected);
        });

        test.each(cases.filter(c => c.expected.length > 0))("attaches every return edge of $name to nodes that are on the canvas", ({ build, collapsed }) => {
            const result = layoutOf(build(), collapsed);
            const onCanvas = new Set(result.nodes.map(n => n.id));
            for (const edge of answerEdges(result)) {
                expect(onCanvas.has(edge.source), `${edge.id} source`).toBe(true);
                expect(onCanvas.has(edge.target), `${edge.id} target`).toBe(true);
                expect(edge.id).toBe(`${edge.source}-answer-${edge.target}`);
            }
        });

        test("runs the return edge from the escalate target to the loop card, on the side the target sits", () => {
            const result = layoutOf([...loopOverPick(), rerouting(), ...desks()]);
            const loop = nodeById(result, "lp");
            const human = nodeById(result, "human");
            expect(answerEdges(result)).toEqual([
                expect.objectContaining({ source: "human", target: "lp", answerSide: "right", targetX: loop.x + loop.width / 2, targetY: loop.y, sourceX: human.x + human.width / 2, sourceY: human.y + human.height }),
            ]);
        });

        test("puts the return edge on the side its source sits, whichever side that is, whatever the node order", () => {
            const build = () => [...loopOverPick(), containerNode("review", "map", "human", ["start"]), rerouting(), ...desks()];
            const sides = new Set<string | undefined>();
            for (const order of [build(), build().reverse()]) {
                const result = layoutOf(order);
                const [edge] = answerEdges(result);
                const source = nodeById(result, edge.source);
                const target = nodeById(result, edge.target);
                const expected = source.x + source.width / 2 < target.x + target.width / 2 ? "left" : "right";
                expect(edge.answerSide).toBe(expected);
                sides.add(edge.answerSide);
            }
            expect([...sides].sort((a, b) => String(a).localeCompare(String(b)))).toEqual(["left", "right"]);
        });

        test("draws the return edge of a top-level decision as it always was drawn", () => {
            const result = layoutOf([agentNode("start"), topLevelPick(), ...desks()]);
            const pick = nodeById(result, "pick");
            expect(answerEdges(result)).toEqual([expect.objectContaining({ id: "human-answer-pick", source: "human", target: "pick", targetX: pick.x + pick.width / 2, targetY: pick.y, answerSide: "right" })]);
        });

        test("leaves the pills and the route edges of the decision as a handoff draws them", () => {
            const nodes = [...loopOverPick(), rerouting(), ...desks()];
            const handedOff = nodes.map(n => (n.id === "pick" ? ({ ...n, escalation_mode: "handoff" } as WorkflowNodeConfig) : n));
            const routeEdges = (result: Layout) => result.edges.filter(e => !e.escalationAnswer && e.target !== "__end__").map(e => e.id);
            expect(routeEdges(layoutOf(nodes))).toEqual(routeEdges(layoutOf(handedOff)));
            expect(pillSummary(layoutOf(nodes))).toEqual(pillSummary(layoutOf(handedOff)));
        });

        const unfinished: Array<{ name: string; escalate: unknown }> = [
            { name: "an empty string", escalate: "" },
            { name: "null", escalate: null },
            { name: "a number", escalate: 7 },
            { name: "an object", escalate: {} },
            { name: "a list", escalate: ["human"] },
        ];
        test.each(unfinished)("draws no return edge, and no edge it cannot place, for an escalate target that is $name", ({ escalate }) => {
            const result = layoutOf([...loopOverPick(), rerouting(escalate), ...desks()]);
            expect(answerEdges(result)).toHaveLength(0);
            expect(result.edges.every(e => [e.sourceX, e.sourceY, e.targetX, e.targetY].every(Number.isFinite))).toBe(true);
        });
    });
});

describe("layouts without a switch or decision inside a map or loop", () => {
    const baseline = unchangedBaseline as Record<string, unknown>;

    test.each(unchangedLayoutFixtures)("$name keeps the layout it had before container bodies drew condition pills", ({ name, nodes, collapsed }) => {
        const result = processWorkflowConfig(makeConfig(nodes), new Set(collapsed ?? []));
        expect(layoutSnapshot(result)).toEqual(baseline[name]);
    });

    test("every fixture has a baseline and no baseline is left over", () => {
        expect(Object.keys(baseline)).toEqual(unchangedLayoutFixtures.map(fixture => fixture.name));
    });
});

describe("known workflows", () => {
    test("agent in knownWorkflows gets 'workflow' visual type", () => {
        const result = processWorkflowConfig(makeConfig([agentNode("sub-flow")]), new Set(), new Set(["sub-flow"]));
        expect(nodeById(result, "sub-flow").type).toBe("workflow");
    });

    test("explicit workflow type node gets 'workflow' visual type", () => {
        const workflowNode: WorkflowNodeConfig = {
            id: "wf1",
            type: "workflow",
            workflow_name: "my-workflow",
            depends_on: [],
        } as unknown as WorkflowNodeConfig;
        const result = processWorkflowConfig(makeConfig([workflowNode]));
        expect(nodeById(result, "wf1").type).toBe("workflow");
    });
});

describe("layout invariants", () => {
    test("all nodes have non-negative x and y", () => {
        const nodes = [agentNode("a"), agentNode("b", ["a"]), agentNode("c", ["a"])];
        const result = processWorkflowConfig(makeConfig(nodes));
        for (const node of result.nodes) {
            expect(node.x).toBeGreaterThanOrEqual(0);
            expect(node.y).toBeGreaterThanOrEqual(0);
        }
    });

    test("totalHeight >= height of all levels stacked", () => {
        const nodes = [agentNode("a"), agentNode("b", ["a"]), agentNode("c", ["b"])];
        const result = processWorkflowConfig(makeConfig(nodes));
        expect(result.totalHeight).toBeGreaterThan(NODE_HEIGHTS.AGENT * 3 + SPACING.VERTICAL * 2);
    });

    test("each edge references existing node ids", () => {
        const nodes = [agentNode("a"), agentNode("b", ["a"])];
        const result = processWorkflowConfig(makeConfig(nodes));
        const ids = new Set(result.nodes.map(n => n.id));
        for (const edge of result.edges) {
            expect(ids.has(edge.source)).toBe(true);
            expect(ids.has(edge.target)).toBe(true);
        }
    });

    test("no duplicate node ids", () => {
        const nodes = [agentNode("a"), agentNode("b", ["a"]), agentNode("c", ["b"])];
        const result = processWorkflowConfig(makeConfig(nodes));
        const ids = result.nodes.map(n => n.id);
        expect(new Set(ids).size).toBe(ids.length);
    });

    test("no duplicate edge ids", () => {
        const nodes = [agentNode("a"), agentNode("b", ["a"]), agentNode("c", ["a"])];
        const result = processWorkflowConfig(makeConfig(nodes));
        const edgeIds = result.edges.map(e => e.id);
        expect(new Set(edgeIds).size).toBe(edgeIds.length);
    });
});

describe("invalid depends_on shapes", () => {
    type Layout = ReturnType<typeof processWorkflowConfig>;
    const stepIds = (result: Layout) =>
        result.nodes
            .filter(n => !n.id.startsWith("__"))
            .map(n => n.id)
            .sort((x, y) => x.localeCompare(y, "en"));
    const edgePairs = (result: Layout) => result.edges.map(e => `${e.source}->${e.target}`);

    test("a two-node cycle with no root draws both nodes, the first listed as the root", () => {
        const result = processWorkflowConfig(makeConfig([agentNode("a", ["b"]), agentNode("b", ["a"])]));

        expect(stepIds(result)).toEqual(["a", "b"]);
        expect(edgePairs(result)).toEqual(expect.arrayContaining(["__start__->a", "a->b", "b->__end__"]));
        expect(nodeById(result, "b").y).toBeGreaterThan(nodeById(result, "a").y);
    });

    test("a three-node cycle draws every node in a single chain", () => {
        const result = processWorkflowConfig(makeConfig([agentNode("a", ["c"]), agentNode("b", ["a"]), agentNode("c", ["b"])]));

        expect(stepIds(result)).toEqual(["a", "b", "c"]);
        expect(edgePairs(result)).toEqual(expect.arrayContaining(["__start__->a", "a->b", "b->c", "c->__end__"]));
    });

    test("a cycle reachable from a root terminates and is broken below that root", () => {
        const result = processWorkflowConfig(makeConfig([agentNode("a", ["b"]), agentNode("b", ["a", "root"]), agentNode("root")]));

        expect(stepIds(result)).toEqual(["a", "b", "root"]);
        expect(edgePairs(result)).toEqual(expect.arrayContaining(["__start__->root", "root->b", "b->a", "a->__end__"]));
        expect(nodeById(result, "root").y).toBeLessThan(nodeById(result, "b").y);
        expect(nodeById(result, "b").y).toBeLessThan(nodeById(result, "a").y);
    });

    test("a self-dependency draws the node as a root", () => {
        const result = processWorkflowConfig(makeConfig([agentNode("a", ["a"])]));

        expect(stepIds(result)).toEqual(["a"]);
        expect(edgePairs(result)).toEqual(expect.arrayContaining(["__start__->a", "a->__end__"]));
    });

    test("a node that only depends on an unknown id is drawn as a root", () => {
        const result = processWorkflowConfig(makeConfig([agentNode("a", ["ghost"])]));

        expect(stepIds(result)).toEqual(["a"]);
        expect(edgePairs(result)).toEqual(expect.arrayContaining(["__start__->a", "a->__end__"]));
        expect(result.nodes.some(n => n.id === "ghost")).toBe(false);
    });

    test("a known dependency still orders a node that also lists an unknown one", () => {
        const result = processWorkflowConfig(makeConfig([agentNode("a"), agentNode("b", ["a", "ghost"])]));

        expect(stepIds(result)).toEqual(["a", "b"]);
        expect(edgePairs(result)).toContain("a->b");
        expect(nodeById(result, "b").y).toBeGreaterThan(nodeById(result, "a").y);
    });

    test("nodes downstream of a dangling dependency keep their order", () => {
        const result = processWorkflowConfig(makeConfig([agentNode("a", ["ghost"]), agentNode("b", ["a"]), agentNode("c", ["b"])]));

        expect(edgePairs(result)).toEqual(expect.arrayContaining(["__start__->a", "a->b", "b->c", "c->__end__"]));
    });

    test("valid nodes keep their levels next to a cycle and a dangling dependency", () => {
        const nodes = [agentNode("first"), agentNode("second", ["first"]), agentNode("loop_a", ["loop_b"]), agentNode("loop_b", ["loop_a"]), agentNode("dangling", ["ghost"])];
        const result = processWorkflowConfig(makeConfig(nodes));

        expect(stepIds(result)).toEqual(["dangling", "first", "loop_a", "loop_b", "second"]);
        expect(edgePairs(result)).toEqual(expect.arrayContaining(["__start__->first", "first->second", "__start__->loop_a", "loop_a->loop_b", "__start__->dangling"]));
        expect(nodeById(result, "second").y).toBeGreaterThan(nodeById(result, "first").y);
        expect(nodeById(result, "first").y).toBeCloseTo(nodeById(result, "loop_a").y);
        expect(nodeById(result, "first").y).toBeCloseTo(nodeById(result, "dangling").y);
    });

    test("the same cycle listed in a different order is entered at the node listed first", () => {
        const entered = (first: string, second: string) => {
            const result = processWorkflowConfig(makeConfig([agentNode(first, [second]), agentNode(second, [first])]));
            return edgePairs(result).filter(pair => pair.startsWith("__start__->"));
        };

        expect(entered("a", "b")).toEqual(["__start__->a"]);
        expect(entered("b", "a")).toEqual(["__start__->b"]);
    });

    test("the authored config is not modified by the layout", () => {
        const config = makeConfig([agentNode("a", ["c", "ghost"]), agentNode("b", ["a"]), agentNode("c", ["b"])]);
        const before = structuredClone(config);

        processWorkflowConfig(config);

        expect(config).toEqual(before);
    });

    test("a null entry in depends_on is skipped and the real dependency still counts", () => {
        const nodes = [agentNode("a"), { id: "b", type: "agent", agent_name: "b", depends_on: [null, "a"] } as unknown as WorkflowNodeConfig];
        const result = processWorkflowConfig(makeConfig(nodes));

        expect(edgePairs(result)).toEqual(expect.arrayContaining(["__start__->a", "a->b", "b->__end__"]));
    });

    test.each([
        ["a scalar string", "a"],
        ["null", null],
        ["a number", 3],
        ["an object", { a: 1 }],
        ["an empty string entry", [""]],
        ["a list of non-strings", [1, {}, null]],
    ])("depends_on that is %s does not throw and draws the node as a root", (_label, dependsOn) => {
        const nodes = [agentNode("a"), { id: "b", type: "agent", agent_name: "b", depends_on: dependsOn } as unknown as WorkflowNodeConfig];
        const result = processWorkflowConfig(makeConfig(nodes));

        expect(stepIds(result)).toEqual(["a", "b"]);
        expect(edgePairs(result)).toEqual(expect.arrayContaining(["__start__->a", "__start__->b"]));
    });

    test("a mixed depends_on list keeps its valid entries and a repeated entry draws one edge", () => {
        const nodes = [agentNode("a"), { id: "b", type: "agent", agent_name: "b", depends_on: ["a", "a", 7, null, "ghost"] } as unknown as WorkflowNodeConfig];
        const result = processWorkflowConfig(makeConfig(nodes));

        expect(edgePairs(result).filter(pair => pair === "a->b")).toHaveLength(1);
        expect(edgePairs(result)).not.toContain("__start__->b");
    });

    test("a long acyclic chain is laid out without exhausting the stack", () => {
        const chain = Array.from({ length: 20000 }, (_, index) => agentNode(`n${index}`, index === 0 ? [] : [`n${index - 1}`]));
        const result = processWorkflowConfig(makeConfig(chain));

        expect(stepIds(result)).toHaveLength(20000);
    });

    test("a map child that depends on its container's dependent does not pull the cycle through the child", () => {
        const nodes = [{ id: "m", type: "map", node: "c", items: "x.output", depends_on: ["x"] } as WorkflowNodeConfig, agentNode("c"), agentNode("x", ["c"])];
        const result = processWorkflowConfig(makeConfig(nodes));

        expect(edgePairs(result)).toEqual(expect.arrayContaining(["__start__->m", "m->x"]));
    });

    test("a node without an id does not stop the cycle walk from reaching the rest of the cycle", () => {
        const idless = { type: "agent", agent_name: "x", depends_on: ["a"] } as unknown as WorkflowNodeConfig;
        const result = processWorkflowConfig(makeConfig([agentNode("a", ["b"]), idless, agentNode("b", ["a"])]));

        expect(result.nodes.map(n => n.id)).toEqual(expect.arrayContaining(["a", "b"]));
    });

    test("a container child that depends on itself keeps its container's edges", () => {
        const nodes = [agentNode("n0", ["n1", "n0"]), agentNode("n1"), { id: "n2", type: "map", node: "n0", items: "x.output", depends_on: ["n0", "n1"] } as WorkflowNodeConfig];
        const result = processWorkflowConfig(makeConfig(nodes));

        expect(edgePairs(result)).toContain("n2->n0");
    });

    test("a cycle-free ladder of diamonds is walked in linear time", () => {
        const rungs = 24;
        const ladder: WorkflowNodeConfig[] = [agentNode("join0")];
        for (let rung = 0; rung < rungs; rung++) {
            ladder.push(agentNode(`left${rung}`, [`join${rung}`]), agentNode(`right${rung}`, [`join${rung}`]), agentNode(`join${rung + 1}`, [`left${rung}`, `right${rung}`]));
        }
        const started = performance.now();
        const result = processWorkflowConfig(makeConfig(ladder));

        expect(stepIds(result)).toHaveLength(ladder.length);
        expect(performance.now() - started).toBeLessThan(2000);
    });

    describe("branch targets whose edge is cut by a cycle", () => {
        const conditionPills = (result: Layout) => result.nodes.filter(n => n.type === "condition");
        const expectNoOrphanPills = (result: Layout) => {
            for (const pill of conditionPills(result)) {
                expect(
                    edgePairs(result).some(pair => pair.endsWith(`->${pill.id}`)),
                    `${pill.id} has no incoming edge`
                ).toBe(true);
                expect(
                    edgePairs(result).some(pair => pair.startsWith(`${pill.id}->`)),
                    `${pill.id} has no outgoing edge`
                ).toBe(true);
            }
        };

        test("a switch case that routes back to an ancestor draws every node and no stray pill", () => {
            const nodes = [agentNode("a", ["router"]), { id: "router", type: "switch", depends_on: ["a"], cases: [{ condition: "x == 1", node: "a" }], default: "done" } as WorkflowNodeConfig, agentNode("done")];
            const result = processWorkflowConfig(makeConfig(nodes));

            expect(stepIds(result)).toEqual(["a", "done", "router"]);
            expect(conditionPills(result).map(n => n.id)).toEqual(["__condition_router_done__"]);
            expectNoOrphanPills(result);
        });

        test("a switch case that routes to the switch itself draws no stray pill", () => {
            const nodes = [{ id: "router", type: "switch", cases: [{ condition: "x == 1", node: "router" }], default: "done" } as WorkflowNodeConfig, agentNode("done")];
            const result = processWorkflowConfig(makeConfig(nodes));

            expect(stepIds(result)).toEqual(["done", "router"]);
            expect(conditionPills(result).map(n => n.id)).toEqual(["__condition_router_done__"]);
            expectNoOrphanPills(result);
        });

        test("a decision route that is an ancestor draws no stray pill", () => {
            const nodes = [agentNode("a", ["triage"]), { id: "triage", type: "decision", questions: [{ key: "k", type: "boolean" }], route_on: "k", routes: { yes: "a" }, default: "done", depends_on: ["a"] } as WorkflowNodeConfig, agentNode("done")];
            const result = processWorkflowConfig(makeConfig(nodes));

            expect(stepIds(result)).toEqual(["a", "done", "triage"]);
            expect(conditionPills(result).map(n => n.id)).toContain("__condition_triage_done__");
            expectNoOrphanPills(result);
        });
    });
});
