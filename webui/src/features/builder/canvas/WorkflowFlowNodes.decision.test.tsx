// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ComponentProps } from "react";
import { render, screen } from "@testing-library/react";
import { ReactFlowProvider } from "@xyflow/react";
import { describe, expect, test } from "vitest";

import type { LayoutNode } from "@/features/workflowVisualization/utils/types";

import { makeNodeProps } from "@/testing/helpers/nodeProps";

import { WfDecisionFlowNode } from "./WorkflowFlowNodes";

const decisionLayoutNode: LayoutNode = {
    id: "triage",
    type: "decision",
    data: {
        label: "triage",
        questions: [
            { key: "department", type: "choice", instructions: "Which team owns it?", options: ["billing", "tech"] },
            { key: "urgent", type: "boolean", instructions: "Is it urgent?", options: [] },
        ],
        routeOn: "department",
    },
    x: 0,
    y: 0,
    width: 280,
    height: 160,
    children: [],
};

describe("WfDecisionFlowNode", () => {
    test("renders the decision card: type label, typed questions with the routed one first and marked, and no node id", () => {
        const props = makeNodeProps({ layoutNode: decisionLayoutNode }) as ComponentProps<typeof WfDecisionFlowNode>;
        render(
            <ReactFlowProvider>
                <WfDecisionFlowNode {...props} />
            </ReactFlowProvider>
        );

        expect(screen.getByTestId("decision-node")).toBeInTheDocument();
        expect(screen.getByText("Decision")).toBeInTheDocument();
        expect(screen.queryByText("triage")).toBeNull();
        expect(screen.getByTitle("Which team owns it?")).toHaveTextContent("department");
        expect(screen.getByText("Choice")).toBeInTheDocument();
        expect(screen.getByText("urgent")).toBeInTheDocument();
        expect(screen.getByText("Boolean")).toBeInTheDocument();
        expect(screen.getAllByRole("img", { name: "Routes on this question" })).toHaveLength(1);
        expect(screen.getByTestId("decision-node")).toHaveTextContent("departmentChoiceurgentBoolean");
        expect(screen.queryByText(/escalates below/)).toBeNull();
    });
});
