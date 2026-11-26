// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import LoopNode from "@/features/activities/flowChart/nodes/LoopNode";

import { createActivityLayoutNode } from "@/testing/helpers/layoutNodes";

const meta = {
    title: "Activities/ActivitiesVisualization/LoopNode",
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

const agentChild = (id: string, label: string) => createActivityLayoutNode({ id, type: "agent", data: { label } });

const compactNode = createActivityLayoutNode({
    id: "loop-1",
    type: "loop",
    width: 200,
    height: 50,
    data: { label: "OriginalLabel", maxIterations: 100 },
});

const expandedNode = createActivityLayoutNode({
    id: "loop-2",
    type: "loop",
    height: 200,
    children: [agentChild("iter-1", "IterationAgent")],
    data: { label: "ProcessingLoop", maxIterations: 5 },
});

export const FallbackLabelBehavior: Story = {
    args: {
        node: compactNode,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Loop")).toBeInTheDocument();
        expect(canvas.queryByText("OriginalLabel")).not.toBeInTheDocument();
    },
};

export const ExpandedWithChildren: Story = {
    args: {
        node: expandedNode,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("ProcessingLoop")).toBeInTheDocument();
        expect(canvas.getByText("max: 5")).toBeInTheDocument();
        expect(canvas.getByText("IterationAgent")).toBeInTheDocument();
    },
};

export const ExpandedWithShortCondition: Story = {
    args: {
        node: {
            ...expandedNode,
            data: { ...expandedNode.data, condition: "x < 5" },
        },
    },
    play: async ({ canvasElement }) => {
        expect(await within(canvasElement).findByText("while: x < 5")).toBeInTheDocument();
    },
};

export const ExpandedWithLongCondition: Story = {
    args: {
        node: {
            ...expandedNode,
            data: {
                ...expandedNode.data,
                condition: "this_is_a_very_long_loop_condition_that_should_be_truncated",
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const longCondition = await canvas.findByText(/^while:/);
        expect(longCondition.textContent).toMatch(/\.\.\.$/);
        expect(longCondition.textContent?.length).toBeLessThanOrEqual("while: ".length + 30 + 3);
    },
};

export const IterationCounterVisible: Story = {
    args: {
        node: {
            ...expandedNode,
            data: { ...expandedNode.data, status: "in-progress", currentIteration: 3 },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Processing iteration 3...")).toBeInTheDocument();
    },
};
