// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import { mockVisualizerSteps } from "@/testing/data/visualizerSteps";

import FlowChartPanel from "./FlowChartPanel";

const meta = {
    title: "Activities/ActivitiesVisualization/FlowChartPanel",
    component: FlowChartPanel,
    parameters: {
        chatContext: {
            agentNameDisplayNameMap: {
                Orchestrator: "Orchestrator",
                ValidatorAgent: "Validator",
            },
            agentsRefetch: async () => {},
        },
        taskContext: {
            highlightedStepId: null,
            setHighlightedStepId: () => {},
        },
    },
    decorators: [
        Story => (
            <div className="h-full w-full bg-(--background-w10)">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof FlowChartPanel>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        processedSteps: mockVisualizerSteps,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findAllByText("User")).toHaveLength(2);
        expect(canvas.getByText("Orchestrator")).toBeInTheDocument();
        expect(canvas.getByText("Validator")).toBeInTheDocument();
        expect(canvas.getByText("Detail Mode")).toBeInTheDocument();
    },
};

export const Empty: Story = {
    args: {
        processedSteps: [],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("No events to display.")).toBeInTheDocument();
    },
};

export const Collapse: Story = {
    args: {
        processedSteps: mockVisualizerSteps,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const user = userEvent.setup();

        expect(await canvas.findAllByText("LLM")).toHaveLength(2);

        await user.click(canvas.getByRole("switch"));
        await waitFor(() => expect(canvas.getAllByText("LLM")).toHaveLength(1));
    },
};

export const AgentNodeDialog: Story = {
    args: {
        processedSteps: mockVisualizerSteps,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const user = userEvent.setup();

        const orchestratorNode = await canvas.findByText("Orchestrator");
        await user.click(orchestratorNode);

        const dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        expect(await within(dialog).findByRole("heading", { name: "Orchestrator" })).toBeInTheDocument();
        expect(within(dialog).getByRole("button", { name: /close/i })).toBeInTheDocument();
    },
};

export const UserNodeDialog: Story = {
    args: {
        processedSteps: mockVisualizerSteps,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const user = userEvent.setup();

        const userNodes = await canvas.findAllByText("User");
        await user.click(userNodes[0]);

        let dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        expect(await within(dialog).findByText("REQUEST")).toBeInTheDocument();

        await user.click(within(dialog).getByRole("button", { name: /close/i }));
        await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

        await user.click(userNodes[1]);
        dialog = await screen.findByRole("dialog");
        expect(dialog).toBeInTheDocument();
        expect(await within(dialog).findByText("Final Output")).toBeInTheDocument();
    },
};
