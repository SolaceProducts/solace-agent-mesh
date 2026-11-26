// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { wfEdgeStyle } from "./wfEdgeStyle";
import { WfAgentFlowNode, WfConditionPillFlowNode, WfDecisionFlowNode, WfEndFlowNode, WfLoopFlowNode, WfMapFlowNode, wfNodePropsEq, WfStartFlowNode, WfSwitchFlowNode, WfToolFlowNode, WfWorkflowRefFlowNode } from "./WorkflowFlowNodes";

describe("wfNodePropsEq", () => {
    const ln = (id: string, extra: Record<string, unknown> = {}) => ({ id, type: "agent", x: 0, y: 0, width: 100, height: 40, data: { agentName: id, ...extra } });
    const props = (over: { canvasData?: unknown; layoutNode?: unknown; status?: string; selected?: boolean; highlighted?: string[] } = {}) =>
        ({
            selected: over.selected ?? false,
            data: {
                layoutNode: over.layoutNode ?? ln("A"),
                agentCanvasData: over.canvasData ?? null,
                agentManifestStatus: over.status,
                agentComponentName: "A",
                hasDepWaitingForInput: false,
                onNodeClick: () => {},
                onExpand: () => {},
                knownNodeIds: new Set<string>(),
                highlightedNodeIds: over.highlighted ? new Set(over.highlighted) : undefined,
            },
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
        }) as any;

    const stableCanvasData = { name: "A" };

    test("skips re-render for an unchanged node even though data is a fresh object each flush", () => {
        expect(wfNodePropsEq(props({ canvasData: stableCanvasData }), props({ canvasData: stableCanvasData }))).toBe(true);
    });

    test("re-renders when agentCanvasData reference changes (this node is streaming)", () => {
        expect(wfNodePropsEq(props({ canvasData: { name: "A" } }), props({ canvasData: { name: "A" } }))).toBe(false);
    });

    test("re-renders on a real layoutNode content change (e.g. a connector icon resolves)", () => {
        expect(wfNodePropsEq(props({ canvasData: stableCanvasData, layoutNode: ln("A") }), props({ canvasData: stableCanvasData, layoutNode: ln("A", { icons: [{ name: "db", url: "x" }] }) }))).toBe(false);
    });

    test("re-renders on status, selection, or own-highlight change", () => {
        expect(wfNodePropsEq(props({ canvasData: stableCanvasData, status: "draft" }), props({ canvasData: stableCanvasData, status: "validated" }))).toBe(false);
        expect(wfNodePropsEq(props({ canvasData: stableCanvasData, selected: false }), props({ canvasData: stableCanvasData, selected: true }))).toBe(false);
        expect(wfNodePropsEq(props({ canvasData: stableCanvasData }), props({ canvasData: stableCanvasData, highlighted: ["A"] }))).toBe(false);
    });

    test("ignores highlight changes to OTHER nodes (only this node's membership matters)", () => {
        expect(wfNodePropsEq(props({ canvasData: stableCanvasData }), props({ canvasData: stableCanvasData, highlighted: ["B"] }))).toBe(true);
    });

    test.each([
        ["hasDepWaitingForInput", (d: Record<string, unknown>) => (d.hasDepWaitingForInput = true)],
        ["allChildrenDone", (d: Record<string, unknown>) => (d.allChildrenDone = false)],
        ["agentComponentName", (d: Record<string, unknown>) => (d.agentComponentName = "B")],
        ["agentManifestId", (d: Record<string, unknown>) => (d.agentManifestId = "x")],
        ["workflowComponent", (d: Record<string, unknown>) => (d.workflowComponent = { name: "WF" })],
        ["workflowCanvasData", (d: Record<string, unknown>) => (d.workflowCanvasData = { name: "WF" })],
    ])("re-renders when %s changes", (_field, mutate) => {
        const a = props({ canvasData: stableCanvasData });
        const b = props({ canvasData: stableCanvasData });
        mutate(b.data);
        expect(wfNodePropsEq(a, b)).toBe(false);
    });
});

describe("workflow flow-node memo wiring", () => {
    test("leaf nodes use wfNodePropsEq; map/loop containers stay bare memo", () => {
        for (const node of [WfStartFlowNode, WfEndFlowNode, WfAgentFlowNode, WfWorkflowRefFlowNode, WfToolFlowNode, WfSwitchFlowNode, WfDecisionFlowNode, WfConditionPillFlowNode]) {
            expect((node as unknown as { compare: unknown }).compare).toBe(wfNodePropsEq);
        }
        for (const node of [WfMapFlowNode, WfLoopFlowNode]) {
            expect((node as unknown as { compare: unknown }).compare).toBeNull();
        }
    });
});

describe("wfEdgeStyle", () => {
    test("a highlighted edge wins over its branch kind", () => {
        expect(wfEdgeStyle({ highlighted: true, branchKind: "escalate" })).toEqual({ strokeWidth: 2.5, stroke: "var(--success-wMain)" });
    });

    test("an escalate edge takes the warning tone", () => {
        expect(wfEdgeStyle({ branchKind: "escalate" })).toEqual({ strokeWidth: 2, stroke: "var(--warning-wMain)" });
    });

    test("route, default and plain edges stay neutral", () => {
        for (const data of [{ branchKind: "route" }, { branchKind: "default" }, undefined]) {
            expect(wfEdgeStyle(data)).toEqual({ strokeWidth: 2, stroke: "var(--stateLayer-w20)" });
        }
    });
});
