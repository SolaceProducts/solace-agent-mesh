// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { nodeTypes } from "./BuilderCanvas";
import { computeCanvasLayout } from "./canvas";
import { isKeyboardOperableNodeType } from "./canvas/keyboardOperableNodes";
import type { ComponentCanvasData } from "./hooks/useBuilderCanvasData";
import type { BuildManifest } from "./hooks/useBuilderManifest";

describe("BuilderCanvas nodeTypes registration", () => {
    test("every node type computeCanvasLayout emits is registered in nodeTypes", () => {
        const wfNodes = [
            { id: "classify", type: "agent" as const, agent_name: "Classifier" },
            { id: "fetch_weather", type: "tool" as const, tool_name: "WeatherToolset__get_current_weather", depends_on: ["classify"] },
            { id: "route", type: "switch" as const, depends_on: ["fetch_weather"], cases: [{ condition: "{{ x }}", node: "sub" }], default: "sub" },
            { id: "sub", type: "workflow" as const, workflow_name: "SubFlow", depends_on: ["route"] },
            {
                id: "triage",
                type: "decision" as const,
                depends_on: ["sub"],
                questions: { department: { type: "choice" as const, instructions: "Which team owns it?", criteria: { billing: "Payments", tech: "Bugs" } } },
                route_on: "department",
                routes: { billing: "bill" },
                default: "bill",
            },
            { id: "bill", type: "agent" as const, agent_name: "Classifier", depends_on: ["triage"] },
        ];
        const manifest: BuildManifest = {
            name: "build",
            components: [
                { filename: "wf.yaml", type: "workflow", name: "Everything", nodes: wfNodes },
                { filename: "c.yaml", type: "agent", name: "Classifier" },
            ],
        };
        const cd = new Map<string, ComponentCanvasData>([
            [
                "Everything",
                {
                    name: "Everything",
                    filename: "wf.yaml",
                    type: "workflow",
                    yamlText: "ok",
                    isLoading: false,
                    isGenerating: false,
                    isQueued: false,
                    isWaitingForInput: false,
                    isBuildingDeps: false,
                    isBuilderResponding: false,
                    dependencyProgress: [],
                    workflowConfig: { nodes: wfNodes } as ComponentCanvasData["workflowConfig"],
                },
            ],
            [
                "Classifier",
                { name: "Classifier", filename: "c.yaml", type: "agent", yamlText: null, isLoading: false, isGenerating: false, isQueued: false, isWaitingForInput: false, isBuildingDeps: false, isBuilderResponding: false, dependencyProgress: [] },
            ],
        ]);

        const { nodes } = computeCanvasLayout(manifest, cd);
        const registered = new Set(Object.keys(nodeTypes));
        const emitted = [...new Set(nodes.map(n => n.type).filter((t): t is string => typeof t === "string"))];

        expect(emitted).toContain("wf-tool");
        expect(emitted).toContain("wf-decision");
        const unregistered = emitted.filter(t => !registered.has(t));
        expect(unregistered).toEqual([]);
    });
});

describe("BuilderCanvas keyboard-operable node types", () => {
    test("exactly the registered cards that open a detail dialog are keyboard-operable", () => {
        const operable = Object.keys(nodeTypes)
            .filter(isKeyboardOperableNodeType)
            .sort((a, b) => a.localeCompare(b));

        expect(operable).toEqual(["a2a_agent", "agent", "connector", "gateway", "skill", "wf-agent", "wf-start", "workflow"]);
    });
});
