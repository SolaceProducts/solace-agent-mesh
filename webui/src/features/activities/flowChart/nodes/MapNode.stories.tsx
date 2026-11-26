// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import MapNode from "@/features/activities/flowChart/nodes/MapNode";

import { createActivityLayoutNode } from "@/testing/helpers/layoutNodes";

const meta = {
    title: "Activities/ActivitiesVisualization/MapNode",
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

const childAgent = (id: string, label: string, iterationIndex?: number) =>
    createActivityLayoutNode({
        id,
        type: "agent",
        data: { label, ...(iterationIndex !== undefined ? { iterationIndex } : {}) },
    });

const compactNode = createActivityLayoutNode({
    id: "map-compact",
    type: "map",
    width: 200,
    height: 50,
});

const expandedSingleBranch = createActivityLayoutNode({
    id: "map-single-branch",
    type: "map",
    width: 320,
    height: 200,
    children: [childAgent("agent-1", "Worker", 0)],
    data: { label: "ParallelMap" },
});

const expandedTwoBranches = createActivityLayoutNode({
    id: "map-two-branches",
    type: "map",
    width: 320,
    height: 200,
    children: [childAgent("agent-1", "BranchOne", 0), childAgent("agent-2", "BranchTwo", 1)],
    data: { label: "ForkMap" },
});

export const CompactNoChildren: Story = {
    args: { node: compactNode },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Map")).toBeInTheDocument();
    },
};

export const ExpandedSingleBranch: Story = {
    args: { node: expandedSingleBranch },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("ParallelMap")).toBeInTheDocument();
        expect(canvas.getByText("Worker")).toBeInTheDocument();
    },
};

export const ExpandedTwoBranches: Story = {
    args: { node: expandedTwoBranches },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("ForkMap")).toBeInTheDocument();
        expect(canvas.getByText("BranchOne")).toBeInTheDocument();
        expect(canvas.getByText("BranchTwo")).toBeInTheDocument();
    },
};

export const InvokesOnChildClick: Story = {
    args: { node: expandedSingleBranch, onChildClick: fn() },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const childLabel = await canvas.findByText("Worker");
        await userEvent.click(childLabel);
        expect(args.onChildClick).toHaveBeenCalledTimes(1);
    },
};
