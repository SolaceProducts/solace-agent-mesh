// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import { type ApplyComplete, EditCompleteCard } from "@/features/builder/EditCompleteCard";

const meta = {
    title: "Builder/EditCompleteCard",
    component: EditCompleteCard,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Inline summary card emitted by the build agent after an Apply Changes flow. Surfaces the agent's natural-language summary, the per-component change list, and a CTA to switch to Test mode.",
            },
        },
    },
} satisfies Meta<typeof EditCompleteCard>;

export default meta;
type Story = StoryObj<typeof meta>;

const APPLY_RESULT: ApplyComplete = {
    summary: "Updated the OrderProcessingWorkflow to use a fresh inventory lookup.",
    changesApplied: [
        { component: "OrderProcessingWorkflow", area: "workflow_logic", change: "Inserted a cache-bust step before the inventory_check node." },
        { component: "Translator", area: "model_choice", change: "Switched to the high-capability tier." },
    ],
};

export const WithChanges: Story = {
    args: { applyComplete: APPLY_RESULT, onSwitchToTest: fn() },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Changes are completed! Here is the summary:");
        await canvas.findByText(APPLY_RESULT.summary);
        await canvas.findByText("OrderProcessingWorkflow");
        await canvas.findByText("Translator");

        await userEvent.click(canvas.getByRole("button", { name: /Switch to Test Mode/ }));
        expect(args.onSwitchToTest).toHaveBeenCalled();
    },
};

export const NoChanges: Story = {
    args: {
        applyComplete: { summary: "Reviewed the recommendations; existing configuration already meets the test outcome.", changesApplied: [] },
        onSwitchToTest: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Changes are completed! Here is the summary:");
        await canvas.findByText(/existing configuration already meets/);
    },
};
