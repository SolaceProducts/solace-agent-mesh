// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import type { LayoutNode } from "../utils/types";

import WorkflowGroup from "./WorkflowGroup";

let idCounter = 0;

function makeNode(type: LayoutNode["type"], data: LayoutNode["data"], children: LayoutNode[] = []): LayoutNode {
    return {
        id: `${type}_${idCounter++}`,
        type,
        data,
        x: 0,
        y: 0,
        width: 0,
        height: 0,
        children,
    };
}

function buildDependencyModeGroup(): LayoutNode {
    idCounter = 0;
    const start = makeNode("agent", { label: "Start", variant: "pill" });
    const toolA = makeNode("tool", { label: "a", nodeId: "a", toolName: "query_alpha", dependsOn: [], iterationIndex: 0 });
    const toolB = makeNode("tool", { label: "b", nodeId: "b", toolName: "query_beta", dependsOn: [], iterationIndex: 1 });
    const toolC = makeNode("tool", { label: "c", nodeId: "c", toolName: "render_gamma", dependsOn: ["a", "b"], iterationIndex: 0 });
    const level0 = makeNode("parallelBlock", { label: "Parallel" }, [toolA, toolB]);
    const level1 = makeNode("parallelBlock", { label: "Parallel" }, [toolC]);
    const finish = makeNode("agent", { label: "Finish", variant: "pill" });
    return makeNode("group", { label: "ReportWorkflow" }, [start, level0, level1, finish]);
}

describe("WorkflowGroup dependency mode", () => {
    test("renders tool children with tool-name subtitles and one bezier per DAG edge", () => {
        const group = buildDependencyModeGroup();
        const { container } = render(<WorkflowGroup node={group} />);

        for (const label of ["a", "b", "c"]) {
            expect(screen.getByTestId(`tool-node-${label}`)).toBeInTheDocument();
        }
        expect(screen.getByText("query_alpha")).toBeInTheDocument();
        expect(screen.getByText("render_gamma")).toBeInTheDocument();

        expect(container.querySelectorAll("svg.pointer-events-none path")).toHaveLength(5);
    });

    test("collapsed dependency-mode group draws no edges", () => {
        const group = buildDependencyModeGroup();
        group.data.isCollapsed = true;
        const { container } = render(<WorkflowGroup node={group} />);

        expect(container.querySelectorAll("svg.pointer-events-none path")).toHaveLength(0);
        expect(screen.getByText("ReportWorkflow")).toBeInTheDocument();
    });
});
