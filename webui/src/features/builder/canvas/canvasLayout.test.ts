// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { escalationAnswerHandles, LAYOUT_CONSTANTS } from "@/features/workflowVisualization/utils/types";

import type { ComponentCanvasData } from "../hooks/useBuilderCanvasData";
import type { BuildManifest } from "../hooks/useBuilderManifest";
import { isBuildStopped } from "../workflowChildStatus";

import { computeCanvasLayout, startCardWidth } from "./canvasLayout";
import { ESCALATION_ANSWER_DASH, wfEdgeStyle } from "./wfEdgeStyle";

function data(overrides: Partial<ComponentCanvasData> = {}): ComponentCanvasData {
    return {
        name: "x",
        filename: "x.yaml",
        type: "agent",
        yamlText: null,
        isLoading: false,
        isGenerating: false,
        isQueued: false,
        isWaitingForInput: false,
        isBuildingDeps: false,
        isBuilderResponding: false,
        dependencyProgress: [],
        ...overrides,
    };
}

type Comp = NonNullable<BuildManifest["components"]>[number];

function manifest(components: Comp[], overrides: Partial<BuildManifest> = {}): BuildManifest {
    return { name: "build", components, ...overrides };
}

describe("computeCanvasLayout — empty + trivial inputs", () => {
    test("returns empty layout for null manifest", () => {
        expect(computeCanvasLayout(null, new Map())).toEqual({ nodes: [], edges: [] });
    });

    test("returns empty layout when the manifest has no components", () => {
        const { nodes, edges } = computeCanvasLayout(manifest([]), new Map());
        expect(nodes).toEqual([]);
        expect(edges).toEqual([]);
    });
});

describe("computeCanvasLayout — column grouping", () => {
    test("places a single agent in the agent column", () => {
        const comps: Comp[] = [{ filename: "a.yaml", type: "agent", name: "A" }];
        const cd = new Map([["A", data({ name: "A", type: "agent" })]]);

        const { nodes, edges } = computeCanvasLayout(manifest(comps), cd);

        expect(edges).toEqual([]);
        expect(nodes.some(n => n.id === "A")).toBe(true);
        expect(nodes.some(n => n.type === "column-header")).toBe(false);
    });

    test("gateway + agent produce two columns with gateway left of agent", () => {
        const comps: Comp[] = [
            { filename: "g.yaml", type: "gateway", name: "G" },
            { filename: "a.yaml", type: "agent", name: "A" },
        ];
        const cd = new Map([
            ["G", data({ name: "G", type: "gateway" })],
            ["A", data({ name: "A", type: "agent" })],
        ]);

        const { nodes } = computeCanvasLayout(manifest(comps), cd);

        const gwNode = nodes.find(n => n.id === "G");
        const agNode = nodes.find(n => n.id === "A");

        expect(gwNode).toBeDefined();
        expect(agNode).toBeDefined();
        expect(gwNode!.position.x).toBeLessThan(agNode!.position.x);
    });

    test("multiple agents stack vertically in the same column", () => {
        const comps: Comp[] = [
            { filename: "a.yaml", type: "agent", name: "A1" },
            { filename: "b.yaml", type: "agent", name: "A2" },
            { filename: "c.yaml", type: "agent", name: "A3" },
        ];
        const cd = new Map(comps.map(c => [c.name, data({ name: c.name, type: "agent" })]));

        const { nodes } = computeCanvasLayout(manifest(comps), cd);

        const agentCards = nodes.filter(n => n.id === "A1" || n.id === "A2" || n.id === "A3");
        expect(agentCards).toHaveLength(3);
        const xs = new Set(agentCards.map(n => n.position.x));
        expect(xs.size).toBe(1);
        const ys = agentCards.map(n => n.position.y).sort((a, b) => a - b);
        expect(new Set(ys).size).toBe(3);
    });
});

describe("computeCanvasLayout — skill and connector card placement", () => {
    test("an agent-referenced skill renders as its own node with a dependency edge", () => {
        const comps: Comp[] = [
            { filename: "a.yaml", type: "agent", name: "A", depends_on: ["S"] },
            { filename: "s.yaml", type: "skill", name: "S" },
        ];
        const cd = new Map([
            ["A", data({ name: "A", type: "agent" })],
            ["S", data({ name: "S", type: "skill" })],
        ]);

        const { nodes, edges } = computeCanvasLayout(manifest(comps), cd);

        expect(nodes.some(n => n.id === "A")).toBe(true);
        expect(nodes.some(n => n.id === "S")).toBe(true);
        expect(edges.some(e => e.source === "A" && e.target === "S")).toBe(true);
    });

    test("standalone skills (not referenced by any agent) DO render as their own card", () => {
        const comps: Comp[] = [{ filename: "s.yaml", type: "skill", name: "S" }];
        const cd = new Map([["S", data({ name: "S", type: "skill" })]]);

        const { nodes } = computeCanvasLayout(manifest(comps), cd);

        expect(nodes.some(n => n.id === "S")).toBe(true);
    });

    test("a skill node grows taller when it has references/tools to show", () => {
        const comps: Comp[] = [{ filename: "s.yaml", type: "skill", name: "S" }];
        const bare = computeCanvasLayout(manifest(comps), new Map([["S", data({ name: "S", type: "skill" })]]));
        const rich = computeCanvasLayout(manifest(comps), new Map([["S", data({ name: "S", type: "skill", skillConfig: { references: ["a.md", "b.md"], tools: ["web_request"] } })]]));
        const h = (layout: { nodes: { id: string; height?: number }[] }) => layout.nodes.find(n => n.id === "S")?.height ?? 0;
        expect(h(rich)).toBeGreaterThan(h(bare));
    });

    test("connectors owned by an agent are rendered inline, not as standalone cards", () => {
        const comps: Comp[] = [
            { filename: "a.yaml", type: "agent", name: "A", connectors: ["C"] },
            { filename: "c.json", type: "connector", name: "C" },
        ];
        const cd = new Map([
            ["A", data({ name: "A", type: "agent" })],
            ["C", data({ name: "C", type: "connector" })],
        ]);

        const { nodes } = computeCanvasLayout(manifest(comps), cd);

        expect(nodes.some(n => n.id === "A")).toBe(true);
        expect(nodes.some(n => n.id === "C")).toBe(false);
    });

    test("standalone connectors (not referenced by any agent/workflow) DO render as their own card", () => {
        const comps: Comp[] = [
            { filename: "a.yaml", type: "agent", name: "A" },
            { filename: "c.json", type: "connector", name: "C" },
        ];
        const cd = new Map([
            ["A", data({ name: "A", type: "agent" })],
            ["C", data({ name: "C", type: "connector" })],
        ]);

        const { nodes } = computeCanvasLayout(manifest(comps), cd);

        expect(nodes.some(n => n.id === "A")).toBe(true);
        expect(nodes.some(n => n.id === "C")).toBe(true);
    });
});

describe("computeCanvasLayout — loading state", () => {
    test("renders a loading card when ComponentCanvasData.isLoading is true", () => {
        const comps: Comp[] = [{ filename: "a.yaml", type: "agent", name: "A" }];
        const cd = new Map([["A", data({ name: "A", type: "agent", isLoading: true })]]);

        const { nodes } = computeCanvasLayout(manifest(comps), cd);
        const agentNode = nodes.find(n => n.id === "A");
        expect(agentNode).toBeDefined();
        expect(agentNode!.type).toBe("loading");
    });

    test("renders a non-loading card type when data is present", () => {
        const comps: Comp[] = [{ filename: "a.yaml", type: "agent", name: "A" }];
        const cd = new Map([["A", data({ name: "A", type: "agent", yamlText: "apps: []" })]]);

        const { nodes } = computeCanvasLayout(manifest(comps), cd);
        const agentNode = nodes.find(n => n.id === "A");
        expect(agentNode!.type).toBe("agent");
    });
});

describe("computeCanvasLayout — dependency edges", () => {
    test("creates a dependency edge between two agents in the same column", () => {
        const comps: Comp[] = [
            { filename: "a.yaml", type: "agent", name: "A" },
            { filename: "b.yaml", type: "agent", name: "B", depends_on: ["A"] },
        ];
        const cd = new Map([
            ["A", data({ name: "A", type: "agent" })],
            ["B", data({ name: "B", type: "agent" })],
        ]);

        const { edges } = computeCanvasLayout(manifest(comps), cd);
        const depEdge = edges.find(e => e.id === "dep:B->A");
        expect(depEdge).toBeDefined();
        expect(depEdge!.source).toBe("B");
        expect(depEdge!.target).toBe("A");
    });

    test("does not create a dependency edge when the target is absent from the canvas", () => {
        const comps: Comp[] = [{ filename: "a.yaml", type: "agent", name: "A", depends_on: ["ghost"] }];
        const cd = new Map([["A", data({ name: "A", type: "agent" })]]);

        const { edges } = computeCanvasLayout(manifest(comps), cd);
        expect(edges.filter(e => e.id.startsWith("dep:"))).toEqual([]);
    });
});

describe("computeCanvasLayout — platform-origin components", () => {
    test("platform-origin components still render on the canvas", () => {
        const comps: Comp[] = [
            { filename: "a.yaml", type: "agent", name: "Session", origin: "session" },
            { filename: "p.yaml", type: "agent", name: "Preexisting", origin: "platform" },
        ];
        const cd = new Map([
            ["Session", data({ name: "Session", type: "agent" })],
            ["Preexisting", data({ name: "Preexisting", type: "agent", origin: "platform" })],
        ]);

        const { nodes } = computeCanvasLayout(manifest(comps), cd);
        expect(nodes.some(n => n.id === "Session")).toBe(true);
        expect(nodes.some(n => n.id === "Preexisting")).toBe(true);
    });
});

describe("computeCanvasLayout — workflow expansion", () => {
    test("expands a workflow into prefixed sub-nodes (wf:<name>:*)", () => {
        const comps: Comp[] = [
            {
                filename: "wf.yaml",
                type: "workflow",
                name: "Pipeline",
                nodes: [{ id: "step1", type: "agent", agent_name: "Worker" }],
            },
            { filename: "worker.yaml", type: "agent", name: "Worker" },
        ];
        const cd = new Map<string, ComponentCanvasData>([
            [
                "Pipeline",
                data({
                    name: "Pipeline",
                    type: "workflow",
                    yamlText: "ok",
                    workflowConfig: {
                        nodes: [{ id: "step1", type: "agent", agent_name: "Worker" }],
                    } as ComponentCanvasData["workflowConfig"],
                }),
            ],
            ["Worker", data({ name: "Worker", type: "agent" })],
        ]);

        const { nodes } = computeCanvasLayout(manifest(comps), cd);

        expect(nodes.some(n => n.id === "Worker")).toBe(false);

        expect(nodes.some(n => typeof n.id === "string" && n.id.startsWith("wf:Pipeline:"))).toBe(true);
    });

    test("hands a workflow's inner agent node the id the session recorded for it, so its pill is judged by its own record", () => {
        const comps: Comp[] = [
            { filename: "wf.yaml", type: "workflow", name: "Pipeline", id: "wf-id", nodes: [{ id: "step1", type: "agent", agent_name: "Worker" }] },
            { filename: "worker.yaml", type: "agent", name: "Worker", id: "worker-id", status: "validated" },
        ];
        const cd = new Map<string, ComponentCanvasData>([
            ["Pipeline", data({ name: "Pipeline", type: "workflow", yamlText: "ok", workflowConfig: { nodes: [{ id: "step1", type: "agent", agent_name: "Worker" }] } as ComponentCanvasData["workflowConfig"] })],
            ["Worker", data({ name: "Worker", type: "agent" })],
        ]);

        const { nodes } = computeCanvasLayout(manifest(comps), cd);

        const inner = nodes.find(n => n.id === "wf:Pipeline:step1");
        expect((inner?.data as { agentManifestId?: string }).agentManifestId).toBe("worker-id");
        const start = nodes.find(n => n.id === "wf:Pipeline:__start__");
        expect((start?.data as { workflowComponent?: { id?: string } }).workflowComponent?.id).toBe("wf-id");
    });

    test("does not emit a loop body node as its own wf-* node (rendered inline)", () => {
        const loopNodes = [
            { id: "count_down", type: "loop" as const, condition: "{{ ctx.output.done }}", max_iterations: 20, node: "decrement" },
            { id: "decrement", type: "agent" as const, agent_name: "LoopCounterAgent" },
        ];
        const comps: Comp[] = [
            { filename: "wf.yaml", type: "workflow", name: "LoopDemo", nodes: loopNodes },
            { filename: "counter.yaml", type: "agent", name: "LoopCounterAgent" },
        ];
        const cd = new Map<string, ComponentCanvasData>([
            ["LoopDemo", data({ name: "LoopDemo", type: "workflow", yamlText: "ok", workflowConfig: { nodes: loopNodes } as ComponentCanvasData["workflowConfig"] })],
            ["LoopCounterAgent", data({ name: "LoopCounterAgent", type: "agent" })],
        ]);

        const { nodes } = computeCanvasLayout(manifest(comps), cd);

        expect(nodes.filter(n => n.type === "wf-loop")).toHaveLength(1);
        expect(nodes.some(n => n.id === "wf:LoopDemo:decrement")).toBe(false);
        expect(nodes.some(n => n.type === "wf-agent")).toBe(false);
    });

    test("does not emit a map body node as its own wf-* node (rendered inline)", () => {
        const mapNodes = [
            { id: "fan_out", type: "map" as const, items: "{{ ctx.output.rows }}", node: "process_row" },
            { id: "process_row", type: "agent" as const, agent_name: "RowProcessor" },
        ];
        const comps: Comp[] = [
            { filename: "wf.yaml", type: "workflow", name: "MapDemo", nodes: mapNodes },
            { filename: "proc.yaml", type: "agent", name: "RowProcessor" },
        ];
        const cd = new Map<string, ComponentCanvasData>([
            ["MapDemo", data({ name: "MapDemo", type: "workflow", yamlText: "ok", workflowConfig: { nodes: mapNodes } as ComponentCanvasData["workflowConfig"] })],
            ["RowProcessor", data({ name: "RowProcessor", type: "agent" })],
        ]);

        const { nodes } = computeCanvasLayout(manifest(comps), cd);

        expect(nodes.filter(n => n.type === "wf-map")).toHaveLength(1);
        expect(nodes.some(n => n.id === "wf:MapDemo:process_row")).toBe(false);
        expect(nodes.some(n => n.type === "wf-agent")).toBe(false);
    });

    test("nested container body (loop-of-map) emits only the outer container node", () => {
        const nestedNodes = [
            { id: "outer_loop", type: "loop" as const, max_iterations: 5, node: "inner_map" },
            { id: "inner_map", type: "map" as const, items: "{{ ctx.output.rows }}", node: "worker" },
            { id: "worker", type: "agent" as const, agent_name: "Worker" },
        ];
        const comps: Comp[] = [
            { filename: "wf.yaml", type: "workflow", name: "NestedDemo", nodes: nestedNodes },
            { filename: "w.yaml", type: "agent", name: "Worker" },
        ];
        const cd = new Map<string, ComponentCanvasData>([
            ["NestedDemo", data({ name: "NestedDemo", type: "workflow", yamlText: "ok", workflowConfig: { nodes: nestedNodes } as ComponentCanvasData["workflowConfig"] })],
            ["Worker", data({ name: "Worker", type: "agent" })],
        ]);

        const { nodes } = computeCanvasLayout(manifest(comps), cd);

        expect(nodes.filter(n => n.type === "wf-loop")).toHaveLength(1);
        expect(nodes.some(n => n.type === "wf-map")).toBe(false);
        expect(nodes.some(n => n.type === "wf-agent")).toBe(false);
        expect(nodes.some(n => typeof n.id === "string" && (n.id.endsWith(":inner_map") || n.id.endsWith(":worker")))).toBe(false);
    });

    test("emits a top-level tool node as a wf-tool node", () => {
        const wfNodes = [{ id: "fetch_weather", type: "tool" as const, tool_name: "WeatherToolset__get_current_weather" }];
        const comps: Comp[] = [{ filename: "wf.yaml", type: "workflow", name: "WeatherWorkflow", nodes: wfNodes }];
        const cd = new Map<string, ComponentCanvasData>([["WeatherWorkflow", data({ name: "WeatherWorkflow", type: "workflow", yamlText: "ok", workflowConfig: { nodes: wfNodes } as ComponentCanvasData["workflowConfig"] })]]);

        const { nodes } = computeCanvasLayout(manifest(comps), cd);

        expect(nodes.filter(n => n.type === "wf-tool")).toHaveLength(1);
    });

    test("does not render a toolset component as a standalone canvas card", () => {
        const wfNodes = [{ id: "fetch_weather", type: "tool" as const, tool_name: "WeatherToolset__get_current_weather" }];
        const comps: Comp[] = [
            { filename: "wf.yaml", type: "workflow", name: "WeatherWorkflow", nodes: wfNodes },
            { filename: "", type: "toolset", name: "WeatherToolset", origin: "platform", status: "deployed" },
        ];
        const cd = new Map<string, ComponentCanvasData>([["WeatherWorkflow", data({ name: "WeatherWorkflow", type: "workflow", yamlText: "ok", workflowConfig: { nodes: wfNodes } as ComponentCanvasData["workflowConfig"] })]]);

        const { nodes } = computeCanvasLayout(manifest(comps), cd);

        expect(nodes.some(n => n.id === "WeatherToolset")).toBe(false);
    });

    test("a deployed agent-less tool workflow is treated as children-done (no perpetual spinner)", () => {
        const wfNodes = [{ id: "fetch_weather", type: "tool" as const, tool_name: "WeatherToolset__get_current_weather" }];
        const comps: Comp[] = [{ filename: "wf.yaml", type: "workflow", name: "WeatherWorkflow", nodes: wfNodes }];
        const cd = new Map<string, ComponentCanvasData>([["WeatherWorkflow", data({ name: "WeatherWorkflow", type: "workflow", status: "deployed", yamlText: "ok", workflowConfig: { nodes: wfNodes } as ComponentCanvasData["workflowConfig"] })]]);

        const { nodes } = computeCanvasLayout(manifest(comps), cd);

        const start = nodes.find(n => typeof n.id === "string" && n.id.endsWith(":__start__"));
        expect(start).toBeDefined();
        expect((start!.data as { allChildrenDone?: boolean }).allChildrenDone).toBe(true);
    });

    test("a deployed workflow delegating to a platform agent is treated as children-done", () => {
        const wfNodes = [
            { id: "fetch_image", type: "tool" as const, tool_name: "web_request" },
            { id: "detect_objects", type: "tool" as const, tool_name: "detect_objects" },
            { id: "draw_boxes", type: "tool" as const, tool_name: "image-tools__draw_boxes" },
            { id: "normalize_detections", type: "agent" as const, agent_name: "Orchestrator" },
        ];
        const comps: Comp[] = [{ filename: "wf.yaml", type: "workflow", name: "VehiclePedestrianDetection", status: "deployed", nodes: wfNodes }];
        const cd = new Map<string, ComponentCanvasData>([
            ["VehiclePedestrianDetection", data({ name: "VehiclePedestrianDetection", type: "workflow", status: "deployed", yamlText: "ok", workflowConfig: { nodes: wfNodes } as ComponentCanvasData["workflowConfig"] })],
        ]);

        const { nodes } = computeCanvasLayout(manifest(comps), cd);

        const start = nodes.find(n => typeof n.id === "string" && n.id.endsWith(":__start__"));
        expect(start).toBeDefined();
        expect((start!.data as { allChildrenDone?: boolean }).allChildrenDone).toBe(true);
    });

    test("a workflow whose owned agent is still draft is NOT children-done", () => {
        const wfNodes = [{ id: "summarize", type: "agent" as const, agent_name: "Summarizer" }];
        const comps: Comp[] = [
            { filename: "wf.yaml", type: "workflow", name: "DigestWorkflow", status: "deployed", nodes: wfNodes },
            { filename: "sum.yaml", type: "agent", name: "Summarizer", status: "draft" },
        ];
        const cd = new Map<string, ComponentCanvasData>([
            ["DigestWorkflow", data({ name: "DigestWorkflow", type: "workflow", status: "deployed", yamlText: "ok", workflowConfig: { nodes: wfNodes } as ComponentCanvasData["workflowConfig"] })],
            ["Summarizer", data({ name: "Summarizer", status: "draft" })],
        ]);

        const { nodes } = computeCanvasLayout(manifest(comps), cd);

        const start = nodes.find(n => typeof n.id === "string" && n.id.endsWith(":__start__"));
        expect(start).toBeDefined();
        expect((start!.data as { allChildrenDone?: boolean }).allChildrenDone).toBe(false);
    });
});

describe("computeCanvasLayout — decision nodes", () => {
    const decisionNodes = [
        { id: "intake", type: "agent" as const, agent_name: "Intake" },
        {
            id: "triage",
            type: "decision" as const,
            depends_on: ["intake"],
            questions: { department: { type: "choice" as const, instructions: "Which team owns it?", criteria: { billing: "Payments", tech: "Bugs" } } },
            route_on: "department",
            routes: { billing: "billing_desk", tech: "tech_desk" },
            default: "tech_desk",
            min_confidence: 0.8,
            escalate: "human_review",
        },
        { id: "billing_desk", type: "agent" as const, agent_name: "Billing", depends_on: ["triage"] },
        { id: "tech_desk", type: "agent" as const, agent_name: "Tech", depends_on: ["triage"] },
        { id: "human_review", type: "agent" as const, agent_name: "Reviewer", depends_on: ["triage"] },
    ];

    function layoutDecisionWorkflow() {
        const comps: Comp[] = [{ filename: "wf.yaml", type: "workflow", name: "Triage", nodes: decisionNodes }];
        const cd = new Map<string, ComponentCanvasData>([["Triage", data({ name: "Triage", type: "workflow", yamlText: "ok" })]]);
        return computeCanvasLayout(manifest(comps), cd);
    }

    test("emits the decision node from the manifest nodes as a wf-decision node", () => {
        const { nodes } = layoutDecisionWorkflow();

        const decision = nodes.filter(n => n.type === "wf-decision");
        expect(decision.map(n => n.id)).toEqual(["wf:Triage:triage"]);
        expect((decision[0].data as { layoutNode: { data: { routeOn?: string } } }).layoutNode.data).toMatchObject({ routeOn: "department" });
        expect(decision[0].width).toBeGreaterThan(0);
        expect(decision[0].height).toBeGreaterThan(0);
    });

    test("gives each branch target one labelled pill, naming a default that repeats a route target alongside the route", () => {
        const { nodes } = layoutDecisionWorkflow();

        const pills = nodes.filter(n => n.type === "wf-condition");
        const pillData = pills.map(p => (p.data as { layoutNode: { data: { label: string; targetNodeId?: string } } }).layoutNode.data);
        expect(pillData.map(d => [d.targetNodeId, d.label])).toEqual([
            ["billing_desk", "billing"],
            ["tech_desk", "tech / default"],
            ["human_review", "escalate < 80% confidence"],
        ]);
    });

    test("routes each branch through its pill as condition edges, toning only the escalate branch", () => {
        const { edges } = layoutDecisionWorkflow();

        for (const target of ["billing_desk", "tech_desk", "human_review"]) {
            const pillId = `wf:Triage:__condition_triage_${target}__`;
            const intoPill = edges.filter(e => e.source === "wf:Triage:triage" && e.target === pillId);
            const outOfPill = edges.filter(e => e.source === pillId && e.target === `wf:Triage:${target}`);
            expect(intoPill).toHaveLength(1);
            expect(outOfPill).toHaveLength(1);
            expect(intoPill[0].data).toMatchObject({ isConditionEdge: true });
            expect(intoPill[0].markerEnd).toBeUndefined();

            const expectedKind = target === "human_review" ? "escalate" : "route";
            expect(intoPill[0].data?.branchKind).toBe(expectedKind);
            expect(outOfPill[0].data?.branchKind).toBe(expectedKind);
        }
        expect(edges.some(e => e.source === "wf:Triage:triage" && !String(e.target).includes("__condition_"))).toBe(false);
    });

    test("draws a rerouting escalation's answer as one dashed return edge to the decision", () => {
        const reroutingNodes = [
            {
                id: "triage",
                type: "decision" as const,
                questions: { department: { type: "choice" as const, instructions: "Which team owns it?", criteria: { billing: "Payments", tech: "Bugs" } } },
                route_on: "department",
                routes: { billing: "billing_desk", tech: "tech_desk" },
                min_confidence: 0.8,
                escalate: "reclassify",
            },
            { id: "reclassify", type: "agent" as const, agent_name: "Classifier", depends_on: ["triage"] },
            { id: "billing_desk", type: "agent" as const, agent_name: "Billing", depends_on: ["triage"] },
            { id: "tech_desk", type: "agent" as const, agent_name: "Tech", depends_on: ["triage"] },
        ];
        const comps: Comp[] = [{ filename: "wf.yaml", type: "workflow", name: "Triage", nodes: reroutingNodes }];
        const { edges } = computeCanvasLayout(manifest(comps), new Map([["Triage", data({ name: "Triage", type: "workflow", yamlText: "ok" })]]));

        const answerEdges = edges.filter(e => e.data?.escalationAnswer === true);
        const { sourceHandle, targetHandle } = escalationAnswerHandles("right");
        expect(answerEdges.map(e => [e.source, e.target, e.sourceHandle, e.targetHandle])).toEqual([["wf:Triage:reclassify", "wf:Triage:triage", sourceHandle, targetHandle]]);
        expect(wfEdgeStyle(answerEdges[0].data).strokeDasharray).toBe(ESCALATION_ANSWER_DASH);
        expect(edges.some(e => e.source === "wf:Triage:reclassify" && String(e.target).includes("desk"))).toBe(false);
        const otherEdges = edges.filter(e => e.type === "workflow-edge" && e.data?.escalationAnswer !== true);
        expect(otherEdges.some(e => wfEdgeStyle(e.data).strokeDasharray !== undefined)).toBe(false);
        expect(otherEdges.every(e => e.sourceHandle === "bottom" && e.targetHandle === "top")).toBe(true);
    });

    describe.each(["loop", "map"] as const)("a rerouting decision that is the body of a %s", kind => {
        const container =
            kind === "loop" ? { id: "box", type: "loop" as const, node: "triage", condition: "{{ triage.output.done }} == false", max_iterations: 3 } : { id: "box", type: "map" as const, node: "triage", items: "{{ start.output.items }}" };
        const reroutingBody = {
            id: "triage",
            type: "decision" as const,
            questions: { department: { type: "choice" as const, instructions: "Which team owns it?", criteria: { billing: "Payments", tech: "Bugs" } } },
            route_on: "department",
            routes: { billing: "billing_desk", tech: "tech_desk" },
            min_confidence: 0.8,
            escalate: "reclassify",
        };
        const nodes = [
            { id: "start", type: "agent" as const, agent_name: "Starter" },
            { ...container, depends_on: ["start"] },
            reroutingBody,
            ...["reclassify", "billing_desk", "tech_desk"].map(id => ({ id, type: "agent" as const, agent_name: id })),
        ];

        test("draws one dashed return edge between the escalate target and the container, both on the canvas", () => {
            const comps: Comp[] = [{ filename: "wf.yaml", type: "workflow", name: "Triage", nodes }];
            const { nodes: canvasNodes, edges } = computeCanvasLayout(manifest(comps), new Map([["Triage", data({ name: "Triage", type: "workflow", yamlText: "ok" })]]));

            const answerEdges = edges.filter(e => e.data?.escalationAnswer === true);
            const { sourceHandle, targetHandle } = escalationAnswerHandles("right");
            expect(answerEdges.map(e => [e.source, e.target, e.sourceHandle, e.targetHandle])).toEqual([["wf:Triage:reclassify", "wf:Triage:box", sourceHandle, targetHandle]]);
            const onCanvas = new Set(canvasNodes.map(n => n.id));
            expect(onCanvas.has(answerEdges[0].source) && onCanvas.has(answerEdges[0].target)).toBe(true);
            expect(wfEdgeStyle(answerEdges[0].data).strokeDasharray).toBe(ESCALATION_ANSWER_DASH);
            expect(edges.some(e => e.source === "wf:Triage:triage" || e.target === "wf:Triage:triage")).toBe(false);
        });
    });

    test("leaves branchKind off switch branch edges", () => {
        const switchNodes = [
            { id: "route", type: "switch" as const, cases: [{ condition: "{{ x }}", node: "a" }] },
            { id: "a", type: "agent" as const, agent_name: "A", depends_on: ["route"] },
        ];
        const comps: Comp[] = [{ filename: "wf.yaml", type: "workflow", name: "Switchy", nodes: switchNodes }];
        const { edges } = computeCanvasLayout(manifest(comps), new Map([["Switchy", data({ name: "Switchy", type: "workflow", yamlText: "ok" })]]));

        const branchEdges = edges.filter(e => String(e.source).includes("__condition_") || String(e.target).includes("__condition_"));
        expect(branchEdges.length).toBeGreaterThan(0);
        expect(branchEdges.every(e => e.data?.branchKind === undefined)).toBe(true);
    });
});

describe("workflow start card width", () => {
    const PILL_WIDTH = LAYOUT_CONSTANTS.NODE_WIDTHS.START;
    const BG_PAD = 20;
    const stepNodes = [{ id: "step1", type: "agent" as const, agent_name: "Worker" }];

    function workflowNodes(name: string) {
        const comps: Comp[] = [{ filename: "wf.yaml", type: "workflow", name, nodes: stepNodes }];
        const { nodes } = computeCanvasLayout(manifest(comps), new Map([[name, data({ name, type: "workflow", yamlText: "ok" })]]));
        const byId = (suffix: string) => nodes.find(n => n.id === `wf:${name}:${suffix}`)!;
        return { start: byId("__start__"), step: byId("step1"), bg: byId("__bg__") };
    }
    const centerX = (n: { position: { x: number }; width?: number }) => n.position.x + n.width! / 2;

    test("grows with the workflow name instead of stopping at the pill width", () => {
        expect(startCardWidth("TriTicketFlow", PILL_WIDTH)).toBeGreaterThan(startCardWidth("Tri", PILL_WIDTH));
    });

    test("never goes below the pill width", () => {
        const widePill = 700;
        expect(startCardWidth("Tri", widePill)).toBe(widePill);
    });

    test("measures the displayed name, which humanizes separators", () => {
        expect(startCardWidth("ticket--triage", PILL_WIDTH)).toBe(startCardWidth("Ticket Triage", PILL_WIDTH));
    });

    test("caps a very long name so it ellipsizes instead of stretching the card", () => {
        const capped = startCardWidth("AVeryLongWorkflowNameThatKeepsOnGoing".repeat(3), PILL_WIDTH);
        expect(capped).toBe(560);
        expect(startCardWidth("AVeryLongWorkflowNameThatKeepsOnGoing".repeat(6), PILL_WIDTH)).toBe(capped);
    });

    test("gives the canvas start node the computed width", () => {
        const { start } = workflowNodes("TriTicketFlow");
        expect(start.width).toBe(startCardWidth("TriTicketFlow", PILL_WIDTH));
        expect(start.width).toBeGreaterThanOrEqual(290);
    });

    test("keeps the widened start card centred over the diagram", () => {
        const { start, step } = workflowNodes("TriTicketFlow");
        expect(centerX(start)).toBeCloseTo(centerX(step));
    });

    test("widens the workflow background panel to enclose a wide start card", () => {
        const { start, bg } = workflowNodes("customer-support-ticket-triage");
        expect(bg.width).toBeGreaterThanOrEqual(start.width! + BG_PAD * 2);
        expect(centerX(bg)).toBeCloseTo(centerX(start));
    });
});

describe("computeCanvasLayout — workflow start card children pending, and the stopped-build reading of the same data", () => {
    const wfNodes = [
        { id: "n0", type: "agent" as const, agent_name: "Done1" },
        { id: "n1", type: "agent" as const, agent_name: "Todo1" },
    ];
    const comps: Comp[] = [
        { filename: "wf.yaml", type: "workflow", name: "StoppedFlow", status: "validated", nodes: wfNodes },
        { filename: "d.yaml", type: "agent", name: "Done1", status: "validated" },
        { filename: "t.yaml", type: "agent", name: "Todo1", status: "draft" },
    ];

    function startData(todo: Partial<ComponentCanvasData>, siblings: { wf?: Partial<ComponentCanvasData>; done?: Partial<ComponentCanvasData> } = {}, everyEntry: Partial<ComponentCanvasData> = {}) {
        const cd = new Map<string, ComponentCanvasData>([
            ["StoppedFlow", data({ name: "StoppedFlow", type: "workflow", status: "validated", yamlText: "ok", workflowConfig: { nodes: wfNodes } as ComponentCanvasData["workflowConfig"], ...siblings.wf, ...everyEntry })],
            ["Done1", data({ name: "Done1", status: "validated", yamlText: "ok", ...siblings.done, ...everyEntry })],
            ["Todo1", data({ name: "Todo1", status: "draft", ...todo, ...everyEntry })],
        ]);
        const { nodes } = computeCanvasLayout(manifest(comps), cd);
        const start = nodes.find(n => n.id === "wf:StoppedFlow:__start__");
        return { ...(start!.data as { allChildrenDone?: boolean }), buildInterrupted: isBuildStopped(cd.values()) };
    }

    test("the layout carries no interruption verdict: the canvas view context is the only source the cards read", () => {
        const cd = new Map<string, ComponentCanvasData>([
            ["StoppedFlow", data({ name: "StoppedFlow", type: "workflow", status: "validated", yamlText: "ok" })],
            ["Done1", data({ name: "Done1", status: "validated", yamlText: "ok" })],
            ["Todo1", data({ name: "Todo1", status: "draft" })],
        ]);
        const { nodes } = computeCanvasLayout(manifest(comps), cd);
        for (const n of nodes) expect(n.data).not.toHaveProperty("buildInterrupted");
    });

    test("a stopped build (one agent done, one never generated, nothing running) is interrupted with children pending", () => {
        expect(startData({})).toMatchObject({ buildInterrupted: true, allChildrenDone: false });
    });

    test.each([
        ["generating", { isGenerating: true }],
        ["queued", { isQueued: true }],
        ["waiting for input", { isWaitingForInput: true }],
    ])("a build with the unfinished agent %s is not interrupted", (_label, todo) => {
        expect(startData(todo)).toMatchObject({ buildInterrupted: false, allChildrenDone: false });
    });

    test("a build with nothing finished yet is not interrupted", () => {
        expect(startData({}, { wf: { status: "draft" }, done: { status: "draft" } })).toMatchObject({ buildInterrupted: false });
    });

    test("a platform-origin component that is still draft does not interrupt a build with nothing else unfinished", () => {
        expect(startData({ status: "validated", yamlText: "ok" }, { done: { origin: "platform", status: "draft", yamlText: null } })).toMatchObject({ buildInterrupted: false });
    });

    test("a deployed agent counts as finished work", () => {
        expect(startData({}, { wf: { status: "draft", yamlText: "ok" }, done: { status: "deployed" } })).toMatchObject({ buildInterrupted: true });
    });

    test("a deployed agent without YAML is not an unfinished component", () => {
        expect(startData({ status: "validated", yamlText: "ok" }, { done: { status: "deployed", yamlText: null } })).toMatchObject({ buildInterrupted: false });
    });

    test("an unfinished agent that already has YAML still leaves the workflow's children pending", () => {
        expect(startData({ yamlText: "generated but never validated" })).toMatchObject({ allChildrenDone: false });
    });

    const UNFINISHED_CHILDREN: ReadonlyArray<[string, Partial<ComponentCanvasData>]> = [
        ["a draft without YAML", { status: "draft", yamlText: null }],
        ["a draft with YAML", { status: "draft", yamlText: "generated but never validated" }],
        ["an error with YAML", { status: "error", yamlText: "failed validation" }],
        ["an error without YAML", { status: "error", yamlText: null }],
        ["a component with no status and YAML", { status: undefined, yamlText: "written, status never stamped" }],
        ["a component with no status and no YAML", { status: undefined, yamlText: null }],
    ];

    test.each(UNFINISHED_CHILDREN)("a stopped build whose only unfinished child is %s is interrupted with children pending", (_label, todo) => {
        expect(startData(todo)).toMatchObject({ buildInterrupted: true, allChildrenDone: false });
    });

    test.each(UNFINISHED_CHILDREN)("a live turn whose only unfinished child is %s is not interrupted, however idle that child looks", (_label, todo) => {
        expect(startData(todo, {}, { isBuilderResponding: true })).toMatchObject({ buildInterrupted: false, allChildrenDone: false });
    });

    test.each([
        ["validated", "validated"],
        ["deployed", "deployed"],
    ])("a build whose last child is %s is not interrupted and has no children pending", (_label, status) => {
        expect(startData({ status, yamlText: "ok" })).toMatchObject({ buildInterrupted: false, allChildrenDone: true });
    });

    test("a platform-origin component that is already live is not finished work of this build", () => {
        expect(startData({}, { wf: { status: "draft" }, done: { origin: "platform", status: "deployed" } })).toMatchObject({ buildInterrupted: false });
    });

    test.each([
        ["generating", { isGenerating: true }],
        ["waiting for input", { isWaitingForInput: true }],
        ["queued", { isQueued: true }],
    ])("a sibling that is %s keeps the whole build from reading as interrupted, even beside an idle unfinished child", (_label, active) => {
        expect(startData({ status: "draft", yamlText: null }, { done: { status: "draft", yamlText: null, ...active }, wf: { status: "validated" } })).toMatchObject({ buildInterrupted: false });
    });

    test("a finished component that is also generating keeps the build from reading as interrupted beside an idle draft", () => {
        expect(startData({ status: "draft", yamlText: null }, { done: { status: "validated", isGenerating: true } })).toMatchObject({ buildInterrupted: false });
    });

    test("a platform-origin sibling that is queued or generating does not keep the build from reading as interrupted", () => {
        expect(startData({ status: "draft", yamlText: null }, { done: { origin: "platform", isQueued: true, isGenerating: true } })).toMatchObject({ buildInterrupted: true });
    });

    test("an errored platform-origin component is not an unfinished component", () => {
        expect(startData({ status: "validated", yamlText: "ok" }, { done: { origin: "platform", status: "error", yamlText: "failed validation" } })).toMatchObject({ buildInterrupted: false });
    });

    test("an errored component beside only unfinished siblings is not interrupted while nothing has finished", () => {
        expect(startData({ status: "error", yamlText: "failed validation" }, { wf: { status: "draft" }, done: { status: "draft" } })).toMatchObject({ buildInterrupted: false });
    });
});
