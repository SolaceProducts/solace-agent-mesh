// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import { mockWorkflows } from "@/testing/data/agentCardInfos";

import { WorkflowList } from "./WorkflowList";

const meta = {
    title: "Workflow/WorkflowList",
    component: WorkflowList,
    parameters: {
        docs: {
            description: {
                component: "List component for displaying and managing workflows with search, filtering, and detail panel.",
            },
        },
    },
} satisfies Meta<typeof WorkflowList>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        workflows: mockWorkflows,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByRole("table", { name: "Workflows" })).toBeInTheDocument();

        expect(await canvas.findByText("Name")).toBeInTheDocument();
        expect(await canvas.findByText("Version")).toBeInTheDocument();
        expect(await canvas.findByText("Status")).toBeInTheDocument();

        expect(await canvas.findByText("Complete Order Workflow")).toBeInTheDocument();
        expect(await canvas.findByText("SimpleLoopWorkflow")).toBeInTheDocument();
    },
};

export const EmptyList: Story = {
    args: {
        workflows: [],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Workflows give enterprises production-ready, best-practice agent patterns that are predictable and reliable.")).toBeInTheDocument();
        expect(await canvas.findByText("Learn how to create workflows")).toBeInTheDocument();
    },
};

export const EmptyListWithBuildCTA: Story = {
    args: {
        workflows: [],
        onBuildWithAI: fn(),
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);

        const cta = await canvas.findByRole("button", { name: /build with ai/i });
        expect(await canvas.findByText("Learn how to create workflows")).toBeInTheDocument();

        await userEvent.click(cta);
        expect(args.onBuildWithAI).toHaveBeenCalledTimes(1);
    },
};
