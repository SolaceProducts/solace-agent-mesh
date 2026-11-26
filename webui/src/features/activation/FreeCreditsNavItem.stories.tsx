// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { mockFreeCredits, mockFreeModelRowV2, mockOnFreeModel } from "@/testing/data";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

import { FreeCreditsNavItem } from "./FreeCreditsNavItem";

function onFreeCredits(remainingPercent: number) {
    return withDefaults(
        http.get("*/api/v1/activation", () => HttpResponse.json(mockOnFreeModel)),
        http.get("*/api/v1/freeModel", () => HttpResponse.json(mockFreeCredits(remainingPercent))),
        http.get("*/api/v2/platform/models", () => HttpResponse.json(paginatedResponse([mockFreeModelRowV2("general"), mockFreeModelRowV2("planning")])))
    );
}

const meta = {
    title: "Activation/FreeCreditsNavItem",
    component: FreeCreditsNavItem,
    args: { collapsed: false },
    parameters: {
        layout: "padded",
        configContext: { activationGateEnabled: true },
        chatContext: { hasModelConfigWrite: true },
        msw: { handlers: onFreeCredits(72) },
        docs: { description: { component: "The free credits rail in the navigation: how much of the free credits is used, and a way onto the person's own model once none are left." } },
    },
    decorators: [
        Story => (
            <div className="w-64 bg-(--darkSurface-bg) pt-3">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof FreeCreditsNavItem>;

export default meta;
type Story = StoryObj<typeof meta>;

export const InUse: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(await canvas.findByRole("link", { name: /28% of free credits used/ })).toBeInTheDocument();
    },
};

export const NoCreditsLeft: Story = {
    parameters: { msw: { handlers: onFreeCredits(0) } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(await canvas.findByRole("link", { name: /No free credits left/ })).toBeInTheDocument();
        await expect(canvas.getByRole("button", { name: "Replace With My Own Model" })).toBeInTheDocument();
    },
};
