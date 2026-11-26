// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import type { BuildManifest } from "@/features/builder/hooks/useBuilderManifest";
import { InlinePlanCard } from "@/features/builder/InlinePlanCard";

const SAMPLE_MANIFEST: BuildManifest = {
    name: "OrderFlow",
    display_name: "Order Processing Workflow",
    description: "Validates and routes incoming customer orders",
    components: [
        { filename: "wf.yaml", type: "workflow", name: "OrderProcessingWorkflow", status: "validated" },
        { filename: "validator.yaml", type: "agent", name: "Validator", status: "validated" },
        { filename: "stripe.yaml", type: "connector", name: "Stripe", status: "validated" },
    ],
};

const meta = {
    title: "Builder/InlinePlanCard",
    component: InlinePlanCard,
    parameters: { layout: "padded" },
} satisfies Meta<typeof InlinePlanCard>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Pending: Story = {
    args: {
        manifest: SAMPLE_MANIFEST,
        onApprove: fn(),
        onRequestChanges: fn(),
        isApproved: false,
        isResponding: false,
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Order Processing Workflow");
        await canvas.findByText("OrderProcessingWorkflow");
        await canvas.findByText("Validator");
        await canvas.findByText("Stripe");

        await userEvent.click(canvas.getByRole("button", { name: /Keep Editing/ }));
        expect(args.onRequestChanges).toHaveBeenCalledWith(expect.stringMatching(/iterate on this plan/));
    },
};

export const ApprovedExpanded: Story = {
    args: {
        manifest: SAMPLE_MANIFEST,
        onApprove: fn(),
        onRequestChanges: fn(),
        isApproved: true,
        isCollapsed: false,
        isResponding: false,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Order Processing Workflow");
        expect(canvas.queryByRole("button", { name: /Keep Editing/ })).toBeNull();
        expect(canvas.queryByRole("button", { name: /Build & Activate/ })).toBeNull();
        await canvas.findByText("Approved");
        expect(canvas.getByLabelText("Approved")).toBeTruthy();
    },
};

export const HiddenWhileResponding: Story = {
    args: {
        manifest: SAMPLE_MANIFEST,
        onApprove: fn(),
        onRequestChanges: fn(),
        isApproved: false,
        isResponding: true,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Order Processing Workflow");
        expect(canvas.queryByRole("button", { name: /Build & Activate/ })).toBeNull();
        expect(canvas.queryByRole("button", { name: /Keep Editing/ })).toBeNull();
    },
};
