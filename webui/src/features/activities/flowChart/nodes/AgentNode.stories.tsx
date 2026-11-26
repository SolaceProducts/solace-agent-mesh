// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import AgentNode from "@/features/activities/flowChart/nodes/AgentNode";

import { createActivityLayoutNode } from "@/testing/helpers/layoutNodes";

const meta = {
    title: "Activities/ActivitiesVisualization/AgentNode",
    component: AgentNode,
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
} satisfies Meta<typeof AgentNode>;

export default meta;
type Story = StoryObj<typeof meta>;

const leafNode = createActivityLayoutNode({ id: "agent-leaf", type: "agent", data: { label: "MyAgent" } });

const toolChild = createActivityLayoutNode({ id: "tool-1", type: "tool", height: 50, data: { label: "search_tool" } });

const agentWithChildren = createActivityLayoutNode({
    id: "agent-with-children",
    type: "agent",
    height: 200,
    children: [toolChild],
    data: { label: "ParentAgent" },
});

const pillNode = createActivityLayoutNode({
    id: "pill-node",
    type: "agent",
    width: 120,
    height: 40,
    data: { label: "Start", variant: "pill" },
});

const branchChild1 = createActivityLayoutNode({ id: "branch-child-1", type: "agent", data: { label: "BranchAgent1" } });

const branchChild2 = createActivityLayoutNode({ id: "branch-child-2", type: "agent", data: { label: "BranchAgent2" } });

export const Default: Story = {
    args: { node: leafNode },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("MyAgent")).toBeInTheDocument();
        expect(canvas.queryByRole("button", { name: /expand/i })).not.toBeInTheDocument();
    },
};

export const Processing: Story = {
    args: { node: { ...leafNode, data: { ...leafNode.data, status: "in-progress" } } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("MyAgent")).toBeInTheDocument();
        expect(canvasElement.querySelector(".processing-halo")).toBeInTheDocument();
    },
};

export const Collapsed: Story = {
    args: {
        node: { ...agentWithChildren, data: { ...agentWithChildren.data, isCollapsed: true } },
        onExpand: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("ParentAgent")).toBeInTheDocument();
        expect(canvas.getByRole("button", { name: /expand/i })).toBeInTheDocument();
        expect(canvas.queryByText("search_tool")).not.toBeInTheDocument();
    },
};

export const WithChildren: Story = {
    args: { node: agentWithChildren },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("ParentAgent")).toBeInTheDocument();
        expect(canvas.getByText("search_tool")).toBeInTheDocument();
    },
};

export const WithParallelBranches: Story = {
    args: {
        node: {
            ...leafNode,
            id: "agent-parallel",
            parallelBranches: [[branchChild1], [branchChild2]],
            data: { label: "ParallelAgent" },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("ParallelAgent")).toBeInTheDocument();
        expect(canvas.getByText("BranchAgent1")).toBeInTheDocument();
        expect(canvas.getByText("BranchAgent2")).toBeInTheDocument();
    },
};

export const WithWideParallelBlock: Story = {
    args: {
        node: createActivityLayoutNode({
            id: "agent-wide-parallel",
            type: "agent",
            height: 300,
            data: { label: "FanOutAgent" },
            children: [
                createActivityLayoutNode({
                    id: "parallel-block-wide",
                    type: "parallelBlock",
                    data: { label: "Parallel Tools" },
                    children: Array.from({ length: 10 }, (_, i) => createActivityLayoutNode({ id: `wide-tool-${i}`, type: "tool", height: 50, data: { label: `get_commit_${i}` } })),
                }),
            ],
        }),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("FanOutAgent")).toBeInTheDocument();
        expect(canvas.getByText("get_commit_0")).toBeInTheDocument();
        expect(canvas.getByText("get_commit_9")).toBeInTheDocument();
        const rows = Array.from(canvasElement.querySelectorAll<HTMLElement>("div.flex-row.items-start"));
        expect(rows.map(row => within(row).getAllByText(/get_commit_/).length)).toEqual([4, 4, 2]);
        const rendered = Array.from(canvasElement.querySelectorAll<HTMLElement>('[data-testid^="tool-node-get_commit_"]')).map(el => el.dataset.testid);
        expect(rendered).toEqual(Array.from({ length: 10 }, (_, i) => `tool-node-get_commit_${i}`));
    },
};

export const ExactlyFourParallel: Story = {
    args: {
        node: createActivityLayoutNode({
            id: "agent-exact-four",
            type: "agent",
            height: 200,
            data: { label: "ExactFourAgent" },
            children: [
                createActivityLayoutNode({
                    id: "parallel-block-four",
                    type: "parallelBlock",
                    data: { label: "Parallel Tools" },
                    children: Array.from({ length: 4 }, (_, i) => createActivityLayoutNode({ id: `four-tool-${i}`, type: "tool", height: 50, data: { label: `tool_${i}` } })),
                }),
            ],
        }),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("ExactFourAgent")).toBeInTheDocument();
        expect(canvasElement.querySelectorAll("div.flex-row.items-start")).toHaveLength(1);
    },
};

export const PillSimple: Story = {
    args: { node: pillNode },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Start")).toBeInTheDocument();
    },
};

export const PillError: Story = {
    args: { node: { ...pillNode, data: { ...pillNode.data, label: "Failed", status: "error" } } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Failed")).toBeInTheDocument();
    },
};

export const PillWithParallelBranches: Story = {
    args: {
        node: {
            ...pillNode,
            id: "pill-parallel",
            parallelBranches: [[branchChild1], [branchChild2]],
            data: { ...pillNode.data, label: "Map" },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Map")).toBeInTheDocument();
        expect(canvas.getByText("BranchAgent1")).toBeInTheDocument();
        expect(canvas.getByText("BranchAgent2")).toBeInTheDocument();
    },
};

export const ExpandInteraction: Story = {
    args: {
        node: { ...agentWithChildren, data: { ...agentWithChildren.data, isCollapsed: true } },
        onExpand: fn(),
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const user = userEvent.setup();
        await user.click(await canvas.findByRole("button", { name: /expand/i }));
        expect(args.onExpand).toHaveBeenCalledOnce();
        expect(args.onExpand).toHaveBeenCalledWith(agentWithChildren.id);
    },
};

export const CollapseInteraction: Story = {
    args: {
        node: { ...agentWithChildren, data: { ...agentWithChildren.data, isExpanded: true } },
        onCollapse: fn(),
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const user = userEvent.setup();
        await user.click(await canvas.findByRole("button", { name: /collapse/i }));
        expect(args.onCollapse).toHaveBeenCalledOnce();
        expect(args.onCollapse).toHaveBeenCalledWith(agentWithChildren.id);
    },
};
