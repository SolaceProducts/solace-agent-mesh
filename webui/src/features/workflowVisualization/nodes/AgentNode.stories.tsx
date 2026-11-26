// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { createWorkflowLayoutNode } from "@/testing/helpers/layoutNodes";

import AgentNode from "./AgentNode";

const meta = {
    title: "Workflow/WorkflowVisualization/AgentNode",
    component: AgentNode,
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
} satisfies Meta<typeof AgentNode>;

export default meta;
type Story = StoryObj<typeof meta>;

const agentNode = createWorkflowLayoutNode({
    id: "validate_order",
    type: "agent",
    data: { label: "OrderValidator" },
});

export const Default: Story = {
    args: {
        node: agentNode,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("OrderValidator")).toBeInTheDocument();
        expect(canvas.getByText("Agent")).toBeInTheDocument();
    },
};
