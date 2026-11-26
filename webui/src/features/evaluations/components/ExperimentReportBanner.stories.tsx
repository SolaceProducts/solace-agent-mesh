// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, screen, userEvent, within } from "storybook/test";

import { ExperimentReportBanner } from "./ExperimentReportBanner";

const meta = {
    title: "Evaluations/ExperimentReportBanner",
    component: ExperimentReportBanner,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component:
                    "Status banner shown above the report on /#/evaluations/runGroups/{id}. Branches by run status — running shows a progress bar (and a Cancel button when an `onCancel` handler is provided), completed shows a dismiss-able success card, failed/cancelled offers a 'Run New Experiment' CTA.",
            },
        },
    },
    args: {
        progressCurrent: 0,
        progressTotal: 0,
        onRunNew: fn(),
    },
} satisfies Meta<typeof ExperimentReportBanner>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Pending: Story = {
    args: { status: "pending", progressCurrent: 0, progressTotal: 4 },
    play: async () => {
        await expect(screen.getByText(/Experiment starting:/)).toBeInTheDocument();
    },
};

export const Running: Story = {
    args: { status: "running", progressCurrent: 2, progressTotal: 4 },
    play: async () => {
        await expect(screen.getByText(/Experiment in Progress:/)).toBeInTheDocument();
        await expect(screen.getByText(/2\/4 examples completed/)).toBeInTheDocument();
    },
};

export const Completed: Story = {
    args: { status: "completed", progressCurrent: 4, progressTotal: 4 },
    play: async () => {
        await expect(screen.getByText("Experiment complete")).toBeInTheDocument();
        await expect(screen.getByText("All examples were evaluated successfully.")).toBeInTheDocument();
    },
};

export const CompletedWithWarnings: Story = {
    args: { status: "completed_with_warnings", progressCurrent: 4, progressTotal: 4 },
    play: async () => {
        await expect(screen.getByText("Experiment complete with warnings")).toBeInTheDocument();
    },
};

export const Failed: Story = {
    args: { status: "failed", progressCurrent: 2, progressTotal: 4 },
    play: async () => {
        await expect(screen.getByText("Experiment partially completed with failures")).toBeInTheDocument();
        await expect(screen.getByRole("button", { name: /Run New Experiment/i })).toBeInTheDocument();
    },
};

export const Cancelled: Story = {
    args: { status: "cancelled", progressCurrent: 2, progressTotal: 4 },
    play: async () => {
        await expect(screen.getByText("Experiment was cancelled")).toBeInTheDocument();
        await expect(screen.getByRole("button", { name: /Run New Experiment/i })).toBeInTheDocument();
    },
};

export const RunningWithCancelButton: Story = {
    args: {
        status: "running",
        progressCurrent: 12,
        progressTotal: 50,
        onCancel: fn(),
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const button = await canvas.findByRole("button", { name: "Cancel" });
        await userEvent.click(button);
        await expect(args.onCancel).toHaveBeenCalledTimes(1);
    },
};

export const RunningWithoutCancelHandler: Story = {
    args: {
        status: "running",
        progressCurrent: 7,
        progressTotal: 25,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.queryByRole("button", { name: "Cancel" })).toBeNull();
    },
};

export const RunningWhileCancelling: Story = {
    args: {
        status: "running",
        progressCurrent: 12,
        progressTotal: 50,
        onCancel: fn(),
        cancelling: true,
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const button = await canvas.findByRole("button", { name: "Cancelling…" });
        expect(button).toBeDisabled();
    },
};

export const CancelledStateHidesCancelButton: Story = {
    args: {
        status: "cancelled",
        progressCurrent: 30,
        progressTotal: 50,
        onCancel: fn(),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.queryByRole("button", { name: "Cancel" })).toBeNull();
    },
};
