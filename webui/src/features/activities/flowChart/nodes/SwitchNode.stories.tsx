// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import SwitchNode from "@/features/activities/flowChart/nodes/SwitchNode";

import { createActivityLayoutNode } from "@/testing/helpers/layoutNodes";

const meta = {
    title: "Activities/ActivitiesVisualization/SwitchNode",
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

const baseNode = createActivityLayoutNode({
    id: "switch-1",
    type: "switch",
    height: 80,
    data: { label: "OriginalLabel" },
});

export const NoCases: Story = {
    args: { node: baseNode },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Switch")).toBeInTheDocument();
        expect(canvas.queryByText(/cases$/)).not.toBeInTheDocument();
    },
};

export const WithCasesCount: Story = {
    args: {
        node: {
            ...baseNode,
            data: {
                ...baseNode.data,
                cases: [{ condition: "x == 1", node: "n1" }],
                defaultBranch: "n-default",
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("2 cases")).toBeInTheDocument();
    },
};

export const WithSelectedBranch: Story = {
    args: {
        node: {
            ...baseNode,
            data: {
                ...baseNode.data,
                cases: [{ condition: "x == 1", node: "branch-a" }],
                selectedBranch: "branch-a",
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("branch-a")).toBeInTheDocument();
    },
};

export const WithDefaultSelectedBranch: Story = {
    args: {
        node: {
            ...baseNode,
            data: {
                ...baseNode.data,
                defaultBranch: "n-default",
                selectedBranch: "default",
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("default")).toBeInTheDocument();
    },
};
