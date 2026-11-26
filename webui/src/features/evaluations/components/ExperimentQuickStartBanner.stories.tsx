// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, screen, userEvent } from "storybook/test";

import { ExperimentQuickStartBanner } from "./ExperimentQuickStartBanner";

const meta = {
    title: "Evaluations/ExperimentQuickStartBanner",
    component: ExperimentQuickStartBanner,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Two-column banner used in both the Reports tab (CTA: 'Run Sample Experiment') and the Experiments tab (CTA: 'Create Experiment'). Shares a single component so the visual layout stays identical across both contexts.",
            },
        },
    },
    args: {
        onCreateExperiment: fn(),
        onDismiss: fn(),
    },
} satisfies Meta<typeof ExperimentQuickStartBanner>;

export default meta;
type Story = StoryObj<typeof meta>;

export const CreateExperimentVariant: Story = {
    play: async ({ args }) => {
        const button = screen.getByRole("button", { name: /Create Experiment/i });
        await expect(button).toBeEnabled();
        await userEvent.click(button);
        await expect(args.onCreateExperiment).toHaveBeenCalledTimes(1);
    },
};

export const RunSampleVariant: Story = {
    args: {
        quickStartTitle: "See a completed report",
        quickStartDescription: "We'll run a sample Experiment so you can see a finished report in one click.",
        ctaLabel: "Run Sample Experiment",
        ctaTestId: "runSampleExperiment",
    },
    play: async ({ canvasElement }) => {
        const cta = canvasElement.querySelector('[data-testid="runSampleExperiment"]');
        await expect(cta).toBeInTheDocument();
        await expect(cta).toHaveTextContent("Run Sample Experiment");
    },
};

export const RunSamplePending: Story = {
    args: {
        ctaLabel: "Run Sample Experiment",
        ctaTestId: "runSampleExperiment",
        ctaDisabled: true,
    },
    play: async ({ canvasElement }) => {
        const cta = canvasElement.querySelector('[data-testid="runSampleExperiment"]') as HTMLButtonElement;
        await expect(cta).toBeDisabled();
    },
};

export const WithDismissButton: Story = {
    args: {
        onDismiss: fn(),
    },
    play: async ({ args, canvasElement }) => {
        const dismiss = canvasElement.querySelector('[data-testid="experimentQuickStartBannerDismiss"]') as HTMLButtonElement;
        await expect(dismiss).toBeInTheDocument();
        await expect(dismiss).toHaveAttribute("aria-label", "Dismiss");
        await userEvent.click(dismiss);
        await expect(args.onDismiss).toHaveBeenCalledTimes(1);
    },
};

export const WithoutDismissButtonRendersNoX: Story = {
    args: {
        onDismiss: undefined,
    },
    play: async ({ canvasElement }) => {
        const dismiss = canvasElement.querySelector('[data-testid="experimentQuickStartBannerDismiss"]');
        await expect(dismiss).not.toBeInTheDocument();
    },
};
