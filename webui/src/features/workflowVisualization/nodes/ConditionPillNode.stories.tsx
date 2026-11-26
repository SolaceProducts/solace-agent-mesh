// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { createWorkflowLayoutNode } from "@/testing/helpers/layoutNodes";

import ConditionPillNode from "./ConditionPillNode";

const meta = {
    title: "Workflow/WorkflowVisualization/ConditionPillNode",
    component: ConditionPillNode,
    parameters: {
        layout: "padded",
    },
    decorators: [
        Story => (
            <div className="flex">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof ConditionPillNode>;

export default meta;
type Story = StoryObj<typeof meta>;

const conditionNode = createWorkflowLayoutNode({
    id: "condition_1",
    type: "condition",
    width: 200,
    height: 28,
    data: { label: "{{priority}} == 'high'", caseNumber: 1, isDefaultCase: false },
});

const defaultConditionNode = createWorkflowLayoutNode({
    id: "condition_default",
    type: "condition",
    width: 120,
    height: 28,
    data: { label: "Default", isDefaultCase: true },
});

export const Default: Story = {
    args: {
        node: conditionNode,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("1")).toBeInTheDocument();
    },
};

export const DefaultCase: Story = {
    args: {
        node: defaultConditionNode,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Default")).toBeInTheDocument();
    },
};

const decisionRouteNode = createWorkflowLayoutNode({
    id: "condition_decision_refund",
    type: "condition",
    width: 80,
    height: 28,
    data: { label: "refund", branchKind: "route", isDefaultCase: false },
});

const decisionEscalateNode = createWorkflowLayoutNode({
    id: "condition_decision_escalate",
    type: "condition",
    width: 80,
    height: 28,
    data: { label: "escalate", branchKind: "escalate", isDefaultCase: false },
});

export const DecisionRoute: Story = {
    args: {
        node: decisionRouteNode,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const label = await canvas.findByText("refund");
        expect(label.parentElement).toHaveAttribute("title", "refund");
        expect(label).toHaveClass("text-(--secondary-text-wMain)");
    },
};

export const DecisionEscalate: Story = {
    args: {
        node: decisionEscalateNode,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const label = await canvas.findByText("escalate");
        expect(label).toHaveClass("text-(--warning-wMain)");
        expect(label.parentElement).toHaveClass("border-(--warning-wMain)", "bg-(--warning-w10)");
    },
};
