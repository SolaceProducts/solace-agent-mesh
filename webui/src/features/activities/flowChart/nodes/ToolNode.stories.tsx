// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import ToolNode from "@/features/activities/flowChart/nodes/ToolNode";

import { createActivityLayoutNode } from "@/testing/helpers/layoutNodes";

const meta = {
    title: "Activities/ActivitiesVisualization/ToolNode",
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

const baseNode = createActivityLayoutNode({
    id: "tool-1",
    type: "tool",
    width: 225,
    height: 50,
    data: { label: "search_tool" },
});

export const Default: Story = {
    args: { node: baseNode },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("search_tool")).toBeInTheDocument();
        expect(canvasElement.querySelector('[data-testid="tool-node-search_tool"]')).toBeInTheDocument();
    },
};

export const WithSingleArtifact: Story = {
    args: {
        node: {
            ...baseNode,
            data: {
                ...baseNode.data,
                createdArtifacts: [{ filename: "result.json" }],
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("1 artifact")).toBeInTheDocument();
    },
};
