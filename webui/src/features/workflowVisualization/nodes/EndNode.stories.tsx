// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen } from "storybook/test";

import { createWorkflowLayoutNode } from "@/testing/helpers/layoutNodes";

import EndNode from "./EndNode";

const meta = {
    title: "Workflow/WorkflowVisualization/EndNode",
    component: EndNode,
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
} satisfies Meta<typeof EndNode>;

export default meta;
type Story = StoryObj<typeof meta>;

const endNode = createWorkflowLayoutNode({
    id: "__end__",
    type: "end",
    width: 100,
    height: 40,
    data: { label: "End" },
});

export const Default: Story = {
    args: {
        node: endNode,
    },
    play: async () => {
        const endNodeElement = screen.getByText("End");
        expect(endNodeElement).toBeInTheDocument();
    },
};
