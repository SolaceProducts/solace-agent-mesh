// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { createWorkflowLayoutNode } from "@/testing/helpers/layoutNodes";

import ToolNode from "./ToolNode";

const meta = {
    title: "Workflow/WorkflowVisualization/ToolNode",
    component: ToolNode,
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
} satisfies Meta<typeof ToolNode>;

export default meta;
type Story = StoryObj<typeof meta>;

const toolNode = createWorkflowLayoutNode({
    id: "create_chart",
    type: "tool",
    data: { toolName: "create_pptx", label: "create_chart" },
});

export const Default: Story = {
    args: {
        node: toolNode,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("create_pptx")).toBeInTheDocument();
        expect(canvas.getByText("Tool")).toBeInTheDocument();
    },
};
