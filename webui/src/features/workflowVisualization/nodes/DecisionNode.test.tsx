// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { createWorkflowLayoutNode } from "@/testing/helpers/layoutNodes";

import type { LayoutNode, WorkflowFlowNodeData } from "../utils/types";
import WorkflowNodeRenderer from "../WorkflowNodeRenderer";

import { renderWorkflowChild } from "./renderWorkflowChild";

const decisionNode: LayoutNode = createWorkflowLayoutNode({
    id: "triage_ticket",
    type: "decision",
    data: {
        label: "triage_ticket",
        questions: [{ key: "category", type: "choice", instructions: "", options: ["billing", "technical"] }],
        routeOn: "category",
    },
});

describe("decision node renderers", () => {
    test("WorkflowNodeRenderer renders a decision node", () => {
        const nodeRefs = { current: new Map<string, HTMLDivElement>() };
        render(<WorkflowNodeRenderer nodes={[decisionNode]} nodeRefs={nodeRefs} />);
        expect(screen.getByRole("img", { name: "Routes on this question" })).toBeInTheDocument();
    });

    test("renderWorkflowChild renders a decision node inside a container", () => {
        const data: WorkflowFlowNodeData = { layoutNode: decisionNode, selectedNodeId: "triage_ticket" };
        render(<>{renderWorkflowChild(decisionNode, data)}</>);
        expect(screen.getByTestId("decision-node")).toHaveClass("!border-(--accent-n2-wMain)");
    });
});
