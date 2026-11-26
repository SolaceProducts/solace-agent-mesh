// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import { TestModeCanvasHeader } from "@/features/builder/TestModeCanvasHeader";

const meta = {
    title: "Builder/TestModeCanvasHeader",
    component: TestModeCanvasHeader,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Sticky header rendered above the canvas while in Test mode. Stop Test enables only when running.",
            },
        },
    },
} satisfies Meta<typeof TestModeCanvasHeader>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Idle: Story = {
    args: {
        testTargetName: "OrderProcessingWorkflow",
        testTargetKind: "workflow",
        running: false,
        onStop: fn(),
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("OrderProcessingWorkflow");
        const stopButton = canvas.getByRole("button", { name: /Stop Test/ });
        expect(stopButton).toBeDisabled();
        await userEvent.click(stopButton);
        expect(args.onStop).not.toHaveBeenCalled();
    },
};

export const Running: Story = {
    args: {
        testTargetName: "OrderProcessingWorkflow",
        testTargetKind: "workflow",
        running: true,
        onStop: fn(),
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const stopButton = canvas.getByRole("button", { name: /Stop Test/ });
        expect(stopButton).toBeEnabled();
        await userEvent.click(stopButton);
        expect(args.onStop).toHaveBeenCalled();
    },
};
