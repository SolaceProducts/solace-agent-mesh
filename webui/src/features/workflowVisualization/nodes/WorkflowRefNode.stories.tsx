// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { createWorkflowLayoutNode } from "@/testing/helpers/layoutNodes";

import WorkflowRefNode from "./WorkflowRefNode";

const meta = {
    title: "Workflow/WorkflowVisualization/WorkflowRefNode",
    component: WorkflowRefNode,
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
} satisfies Meta<typeof WorkflowRefNode>;

export default meta;
type Story = StoryObj<typeof meta>;

const workflowRefNode = createWorkflowLayoutNode({
    id: "nested_workflow",
    type: "workflow",
    data: { workflowName: "NestedWorkflow", label: "Process Suborder" },
});

export const Default: Story = {
    args: {
        node: workflowRefNode,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("NestedWorkflow")).toBeInTheDocument();
        expect(canvas.getByText("Workflow")).toBeInTheDocument();
    },
};
