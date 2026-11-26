// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { createWorkflowLayoutNode } from "@/testing/helpers/layoutNodes";

import SwitchNode from "./SwitchNode";

const meta = {
    title: "Workflow/WorkflowVisualization/SwitchNode",
    component: SwitchNode,
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
} satisfies Meta<typeof SwitchNode>;

export default meta;
type Story = StoryObj<typeof meta>;

const switchNode = createWorkflowLayoutNode({
    id: "check_priority",
    type: "switch",
    height: 120,
    data: {
        label: "Check Priority",
        cases: [
            { condition: "{{priority}} == 'high'", node: "fast_path" },
            { condition: "{{priority}} == 'low'", node: "slow_path" },
        ],
    },
});

export const Default: Story = {
    args: {
        node: switchNode,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Switch")).toBeInTheDocument();
        expect(canvas.getByText("2 cases")).toBeInTheDocument();
    },
};
