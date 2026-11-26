// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { createWorkflowLayoutNode } from "@/testing/helpers/layoutNodes";

import MapNode from "./MapNode";

const meta = {
    title: "Workflow/WorkflowVisualization/MapNode",
    component: MapNode,
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
} satisfies Meta<typeof MapNode>;

export default meta;
type Story = StoryObj<typeof meta>;

const mapNode = createWorkflowLayoutNode({
    id: "process_items",
    type: "map",
    data: { label: "Process Items", items: "{{workflow.input.order_items}}" },
});

const mapNodeWithChildren = createWorkflowLayoutNode({
    id: "process_items",
    type: "map",
    width: 328,
    height: 148,
    children: [
        createWorkflowLayoutNode({
            id: "item_processor",
            type: "agent",
            data: { label: "ItemProcessor" },
        }),
    ],
    data: { label: "Process Items", items: "{{workflow.input.order_items}}" },
});

export const Default: Story = {
    args: {
        node: mapNode,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Map")).toBeInTheDocument();
    },
};

export const WithChildren: Story = {
    args: {
        node: mapNodeWithChildren,
        renderChildren: children => children.map(child => <div key={child.id}>{child.data.label}</div>),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Map")).toBeInTheDocument();
        expect(await canvas.findByText("ItemProcessor")).toBeInTheDocument();
    },
};
