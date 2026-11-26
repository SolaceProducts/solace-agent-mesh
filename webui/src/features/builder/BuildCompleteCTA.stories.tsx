// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import { BuildCompleteCTA } from "@/features/builder/BuildCompleteCTA";
import type { BuildManifest } from "@/features/builder/hooks/useBuilderManifest";

const meta = {
    title: "Builder/BuildCompleteCTA",
    component: BuildCompleteCTA,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Inline CTA pinned to the last build-chat AI message once every session-origin component is deployed. Returns null until the build is done — callers render unconditionally inside their addon callback.",
            },
        },
    },
} satisfies Meta<typeof BuildCompleteCTA>;

export default meta;
type Story = StoryObj<typeof meta>;

const deployedManifest: BuildManifest = {
    name: "OrderFlow",
    components: [
        { filename: "wf.yaml", type: "workflow", name: "OrderProcessingWorkflow", status: "deployed", origin: "session", deployed_version: 0 },
        { filename: "validator.yaml", type: "agent", name: "Validator", status: "deployed", origin: "session", deployed_version: 0 },
    ],
};

const deployedEdits = {
    versions: new Map([
        ["wf.yaml", 0],
        ["validator.yaml", 0],
    ]),
    serverApplyEnabled: false,
};

const partialManifest: BuildManifest = {
    name: "InProgressBuild",
    components: [
        { filename: "wf.yaml", type: "workflow", name: "OrderProcessingWorkflow", status: "validated", origin: "session" },
        { filename: "validator.yaml", type: "agent", name: "Validator", status: "deployed", origin: "session", deployed_version: 0 },
    ],
};

export const AllDeployed: Story = {
    args: { manifest: deployedManifest, deployEdits: deployedEdits, onSwitchToTest: fn(), onAddToAgent: fn() },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        await canvas.findByText(/is created and deployed/);
        await userEvent.click(canvas.getByRole("button", { name: /Switch to Test Mode/ }));
        expect(args.onSwitchToTest).toHaveBeenCalled();
    },
};

export const NotAllDeployed_RendersNull: Story = {
    args: { manifest: partialManifest, deployEdits: deployedEdits, onSwitchToTest: fn(), onAddToAgent: fn() },
    play: async ({ canvasElement }) => {
        expect(within(canvasElement).queryByText(/is created and deployed/)).toBeNull();
        expect(within(canvasElement).queryByRole("button", { name: /Switch to Test Mode/ })).toBeNull();
    },
};
