// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import { createWorkflowLayoutNode } from "@/testing/helpers/layoutNodes";

import DecisionNode from "./DecisionNode";

const meta = {
    title: "Workflow/WorkflowVisualization/DecisionNode",
    component: DecisionNode,
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
} satisfies Meta<typeof DecisionNode>;

export default meta;
type Story = StoryObj<typeof meta>;

const decisionNode = createWorkflowLayoutNode({
    id: "triage_ticket",
    type: "decision",
    height: 118,
    data: {
        label: "triage_ticket",
        questions: [
            { key: "category", type: "choice", instructions: "Which team owns this ticket?", options: ["billing", "technical", "other"] },
            { key: "urgent", type: "boolean", instructions: "Does the customer need an answer today?", options: [] },
        ],
        routeOn: "category",
    },
});

export const Default: Story = {
    args: {
        node: decisionNode,
        onClick: fn(),
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const card = await canvas.findByTestId("decision-node");
        expect(within(card).getByText("Decision")).toBeInTheDocument();
        expect(card).not.toHaveTextContent("triage_ticket");
        expect(within(card).getByTitle("Which team owns this ticket?")).toHaveTextContent("category");
        expect(within(card).getByText("Choice")).toBeInTheDocument();
        expect(within(card).getByText("urgent")).toBeInTheDocument();
        expect(within(card).getByText("Boolean")).toBeInTheDocument();
        expect(within(card).getAllByRole("img", { name: "Routes on this question" })).toHaveLength(1);
        expect(card).not.toHaveTextContent("escalates below");

        await userEvent.click(card);
        expect(args.onClick).toHaveBeenCalledWith(decisionNode);
    },
};

export const QuestionsOnly: Story = {
    args: {
        node: createWorkflowLayoutNode({
            id: "score_reply",
            type: "decision",
            height: 84,
            data: {
                label: "score_reply",
                questions: [{ key: "tone", type: "score", instructions: "", options: ["1", "2", "3"] }],
            },
        }),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const card = await canvas.findByTestId("decision-node");
        expect(within(card).getByText("Score")).toBeInTheDocument();
        expect(within(card).queryByRole("img", { name: "Routes on this question" })).not.toBeInTheDocument();
    },
};
