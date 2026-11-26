// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen } from "storybook/test";

import { createWorkflowLayoutNode } from "@/testing/helpers/layoutNodes";

import StartNode from "./StartNode";

const meta = {
    title: "Workflow/WorkflowVisualization/StartNode",
    component: StartNode,
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
} satisfies Meta<typeof StartNode>;

export default meta;
type Story = StoryObj<typeof meta>;

const startNode = createWorkflowLayoutNode({
    id: "__start__",
    type: "start",
    width: 100,
    height: 40,
    data: { label: "Start" },
});

export const Default: Story = {
    args: {
        node: startNode,
        isHighlighted: false,
    },
    play: async () => {
        const startNodeElement = screen.getByText("Start");
        expect(startNodeElement).toBeInTheDocument();
    },
};
