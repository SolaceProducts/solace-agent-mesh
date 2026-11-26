// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { createWorkflowLayoutNode } from "@/testing/helpers/layoutNodes";

import LoopNode from "./LoopNode";

const meta = {
    title: "Workflow/WorkflowVisualization/LoopNode",
    component: LoopNode,
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
} satisfies Meta<typeof LoopNode>;

export default meta;
type Story = StoryObj<typeof meta>;

const loopNode = createWorkflowLayoutNode({
    id: "retry_loop",
    type: "loop",
    height: 120,
    data: {
        label: "Retry Loop",
        condition: "{{check_status.output.ready}} == false",
        maxIterations: 5,
        delay: "2s",
    },
});

const loopNodeWithChildren = createWorkflowLayoutNode({
    id: "retry_loop",
    type: "loop",
    width: 328,
    height: 176,
    children: [
        createWorkflowLayoutNode({
            id: "check_status",
            type: "agent",
            data: { label: "StatusChecker" },
        }),
    ],
    data: {
        label: "Retry Loop",
        condition: "{{check_status.output.ready}} == false",
        maxIterations: 5,
        delay: "2s",
    },
});

export const Default: Story = {
    args: {
        node: loopNode,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Loop")).toBeInTheDocument();
        expect(canvas.getByText("max: 5")).toBeInTheDocument();
    },
};

export const WithChildren: Story = {
    args: {
        node: loopNodeWithChildren,
        renderChildren: children => children.map(child => <div key={child.id}>{child.data.label}</div>),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Loop")).toBeInTheDocument();
        expect(await canvas.findByText("StatusChecker")).toBeInTheDocument();
    },
};
