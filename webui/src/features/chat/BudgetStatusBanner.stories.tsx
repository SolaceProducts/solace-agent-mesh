// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import type { BudgetStatus } from "@/api/budget-status";

import { withDefaults } from "@/testing/helpers";

import { BudgetStatusBanner } from "./BudgetStatusBanner";
import { budgetWarnDismissalKey } from "./budgetWarnDismissalKey";

const PERIOD_END = "2099-11-01T00:00:00.000Z";

const statusParameters = (status: Partial<BudgetStatus>) => ({
    userContext: { username: "story-user" },
    msw: {
        handlers: withDefaults(http.get("*/api/v1/budget/status", () => HttpResponse.json({ ownLevel: "none", meshLimitReached: false, periodEnd: PERIOD_END, capKey: "budget-1", ...status }))),
    },
});

const meta = {
    title: "Chat/BudgetStatusBanner",
    component: BudgetStatusBanner,
    decorators: [
        Story => {
            localStorage.removeItem(budgetWarnDismissalKey("story-user"));
            return (
                <div className="max-w-3xl">
                    <Story />
                </div>
            );
        },
    ],
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Chat-page banner for the caller's budget: a dismissible warning near their own limit, and a blocking notice once their own or the deployment's budget is used up. The deployment limit wins over the caller's own level.",
            },
        },
    },
} satisfies Meta<typeof BudgetStatusBanner>;

export default meta;
type Story = StoryObj<typeof meta>;

export const OwnWarning: Story = {
    parameters: statusParameters({ ownLevel: "warn" }),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText(/close to your usage budget/)).toBeInTheDocument();
    },
};

export const OwnLimit: Story = {
    parameters: statusParameters({ ownLevel: "limit" }),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText(/You've used up your usage budget/)).toBeInTheDocument();
    },
};

export const MeshLimit: Story = {
    parameters: statusParameters({ ownLevel: "warn", meshLimitReached: true }),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText(/This deployment's usage budget is used up/)).toBeInTheDocument();
    },
};
