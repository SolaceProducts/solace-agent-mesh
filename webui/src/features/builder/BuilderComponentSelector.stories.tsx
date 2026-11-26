// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import { BuilderComponentSelector } from "@/features/builder/BuilderComponentSelector";
import type { BuildManifest } from "@/features/builder/hooks/useBuilderManifest";

const meta = {
    title: "Builder/BuilderComponentSelector",
    component: BuilderComponentSelector,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component:
                    "Dropdown selector that lets the user switch which manifest component is focused in the canvas. Groups items by kind (Workflows / Agents / Skills / Connectors) with a separator between groups. Returns null when the manifest has no components — callers render unconditionally.",
            },
        },
    },
    decorators: [
        Story => (
            <div className="flex min-h-90 justify-end">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof BuilderComponentSelector>;

export default meta;
type Story = StoryObj<typeof meta>;

const fullManifest: BuildManifest = {
    name: "OrderFlow",
    components: [
        { filename: "wf.yaml", type: "workflow", name: "OrderProcessingWorkflow", status: "validated", origin: "session" },
        { filename: "validator.yaml", type: "agent", name: "Validator", status: "deployed", origin: "session" },
        { filename: "translator.yaml", type: "agent", name: "Translator", status: "validated", origin: "session" },
        { filename: "platform-router.yaml", type: "agent", name: "PlatformRouter", status: "validated", origin: "platform" },
        { filename: "jira.yaml", type: "connector", name: "JiraConnector", status: "validated", origin: "session", icon: "jira" },
    ],
};

const errorStateManifest: BuildManifest = {
    name: "BrokenBuild",
    components: [
        { filename: "wf.yaml", type: "workflow", name: "OrderProcessingWorkflow", status: "error", origin: "session" },
        { filename: "validator.yaml", type: "agent", name: "Validator", status: "draft", origin: "session" },
    ],
};

async function openSelector(canvasElement: HTMLElement, triggerLabel: string) {
    await userEvent.click(within(canvasElement).getByRole("button", { name: triggerLabel }));
}

async function expectBadge(componentName: string, status: string) {
    const item = await screen.findByRole("menuitem", { name: new RegExp(componentName) });
    await waitFor(() => expect(within(item).getByText(status)).toBeVisible());
}

export const Default: Story = {
    name: "Mixed manifest with active selection",
    args: { manifest: fullManifest, activeComponentName: "Validator", onComponentSelect: async () => {} },
    play: async ({ canvasElement }) => {
        await openSelector(canvasElement, "Validator");
        await expectBadge("OrderProcessingWorkflow", "validated");
        await expectBadge("PlatformRouter", "deployed");
        await expectBadge("JiraConnector", "validated");
    },
};

export const ErrorAndDraftStatuses: Story = {
    name: "Mixed error + draft statuses",
    args: { manifest: errorStateManifest, activeComponentName: "OrderProcessingWorkflow", onComponentSelect: async () => {} },
    play: async ({ canvasElement }) => {
        await openSelector(canvasElement, "OrderProcessingWorkflow");
        await expectBadge("OrderProcessingWorkflow", "error");
        await expectBadge("Validator", "draft");
    },
};
