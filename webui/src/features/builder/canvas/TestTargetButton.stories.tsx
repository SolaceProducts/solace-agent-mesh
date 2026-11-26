// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, userEvent, within } from "storybook/test";

import { CanvasViewProvider } from "@/features/builder/canvas/canvasViewContext";
import { TestTargetButton } from "@/features/builder/canvas/TestTargetButton";

const meta = {
    title: "Builder/TestTargetButton",
    component: TestTargetButton,
    parameters: {
        layout: "centered",
        docs: {
            description: {
                component: "Test-tube button overlay rendered on canvas cards in Test mode. Built on CanvasViewContext — outside the provider it renders nothing.",
            },
        },
    },
    decorators: [
        (Story, ctx) => {
            const { builderActiveTab = "test", testTargetName = null } = ctx.parameters.canvasView as { builderActiveTab?: "build" | "test"; testTargetName?: string | null };
            return (
                <CanvasViewProvider builderActiveTab={builderActiveTab} testTargetName={testTargetName} onSelectTestTarget={() => {}} isApplyingChanges={false}>
                    <div className="p-6">
                        <Story />
                    </div>
                </CanvasViewProvider>
            );
        },
    ],
} satisfies Meta<typeof TestTargetButton>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Selectable: Story = {
    args: { componentName: "AnotherAgent", eligible: true },
    parameters: { canvasView: { builderActiveTab: "test", testTargetName: "Translator" } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const button = canvas.getByRole("button", { name: "Test this" });
        await userEvent.click(button);
    },
};

export const AlreadySelected: Story = {
    args: { componentName: "Translator", eligible: true, disabledWhenSelected: true },
    parameters: { canvasView: { builderActiveTab: "test", testTargetName: "Translator" } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const button = canvas.getByRole("button", { name: "This node is already selected" });
        expect(button).toBeDisabled();
    },
};

export const NotEligible_RendersNothing: Story = {
    args: { componentName: "SomeConnector", eligible: false },
    parameters: { canvasView: { builderActiveTab: "test", testTargetName: "Translator" } },
    play: async ({ canvasElement }) => {
        expect(within(canvasElement).queryByRole("button")).toBeNull();
    },
};

export const BuildMode_RendersNothing: Story = {
    args: { componentName: "Translator", eligible: true },
    parameters: { canvasView: { builderActiveTab: "build", testTargetName: null } },
    play: async ({ canvasElement }) => {
        expect(within(canvasElement).queryByRole("button")).toBeNull();
    },
};
