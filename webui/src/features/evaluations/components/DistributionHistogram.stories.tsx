// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen } from "storybook/test";

import { DistributionHistogram } from "./DistributionHistogram";

const meta = {
    title: "Evaluations/DistributionHistogram",
    component: DistributionHistogram,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Bar chart bucketing per-example scores into 5 bands (0-19% / 20-39% / 40-59% / 60-79% / 80-100%). Renders an empty-state pill until at least one model has at least one non-null score.",
            },
        },
    },
} satisfies Meta<typeof DistributionHistogram>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Empty: Story = {
    args: {
        scoresByModel: {},
        allModels: [],
    },
    play: async () => {
        await expect(screen.getByText(/Come back to see the distribution/i)).toBeInTheDocument();
    },
};

export const SingleModelAllPassing: Story = {
    args: {
        scoresByModel: {
            general: [1.0, 1.0, 0.95, 0.85, 0.92],
        },
        allModels: ["general"],
    },
};

export const TwoModelsMixedDistribution: Story = {
    args: {
        scoresByModel: {
            "gpt-4o": [0.95, 1.0, 0.88, 0.72, 0.45, 0.85, 1.0],
            "claude-sonnet": [0.65, 0.72, 0.85, 0.9, 0.55, 0.3, 0.8],
        },
        allModels: ["gpt-4o", "claude-sonnet"],
    },
};

export const SingleBucketOutlier: Story = {
    args: {
        scoresByModel: {
            general: [0.0, 0.05, 0.1, 0.15],
        },
        allModels: ["general"],
    },
};
