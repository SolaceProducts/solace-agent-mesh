// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { mockFreeCredits, mockFreeModelRowV2, mockOnFreeModel } from "@/testing/data";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

import { FreeCreditsBanner } from "./FreeCreditsBanner";

function onFreeCredits(remainingPercent: number) {
    return withDefaults(
        http.get("*/api/v1/activation", () => HttpResponse.json(mockOnFreeModel)),
        http.get("*/api/v1/freeModel", () => HttpResponse.json(mockFreeCredits(remainingPercent))),
        http.get("*/api/v2/platform/models", () => HttpResponse.json(paginatedResponse([mockFreeModelRowV2("general"), mockFreeModelRowV2("planning")])))
    );
}

const meta = {
    title: "Activation/FreeCreditsBanner",
    component: FreeCreditsBanner,
    parameters: {
        layout: "padded",
        configContext: { activationGateEnabled: true },
        chatContext: { hasModelConfigWrite: true },
        msw: { handlers: onFreeCredits(72) },
        docs: { description: { component: "The Models page banner for the free model: how much of the free credits is used, a warning near the end, and what to do once none are left." } },
    },
    decorators: [
        Story => (
            <div className="max-w-2xl">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof FreeCreditsBanner>;

export default meta;
type Story = StoryObj<typeof meta>;

export const InUse: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(await canvas.findByRole("heading", { name: "28% of free credits used" })).toBeInTheDocument();
    },
};

export const RunningLow: Story = {
    parameters: { msw: { handlers: onFreeCredits(15) } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(await canvas.findByRole("heading", { name: "85% of free credits used" })).toBeInTheDocument();
        await expect(canvas.getByText(/Add a model before the credits run out/)).toBeInTheDocument();
    },
};

export const NoCreditsLeft: Story = {
    parameters: { msw: { handlers: onFreeCredits(0) } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(await canvas.findByRole("heading", { name: "No free credits left" })).toBeInTheDocument();
        await expect(canvas.getByRole("link", { name: "Contact Solace" })).toBeInTheDocument();
        await expect(canvas.getByRole("button", { name: "Replace With My Own Model" })).toBeInTheDocument();
    },
};
