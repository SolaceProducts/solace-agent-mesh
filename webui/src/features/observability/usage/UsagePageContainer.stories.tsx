// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, userEvent, within } from "storybook/test";

import type { UsageMoney } from "@/api/usage";

import { usageGoldens, usageModelRows } from "@/testing/data/usage";
import { defaultUsageDataset } from "@/testing/msw/usageHandlers";

import { UsagePageContainer } from "./UsagePageContainer";
import { regionOf, usageAt, usageMsw } from "./usageStories";

const meta = {
    title: "Signals & Usage/Usage/Default",
    component: UsagePageContainer,
    parameters: { layout: "fullscreen", ...usageAt("?type=users") },
} satisfies Meta<typeof UsagePageContainer>;

export default meta;
type Story = StoryObj<typeof meta>;

const ZERO: UsageMoney = "0.0000000000";

export const NoData: Story = {
    name: "No data",
    parameters: usageMsw({
        summary: usageGoldens.summaryBuilding,
        rows: { ...defaultUsageDataset.rows, users: [], workflows: [], models: usageModelRows.map(row => ({ ...row, cost: ZERO, activities: 0 })) },
        savings: usageGoldens.savingsBuilding,
    }),
    play: async ({ canvasElement, step }) => {
        const canvas = within(canvasElement);

        await step("Overview", async () => {
            const total = within(await canvas.findByTestId("usageTotalSpend"));
            expect(total.getByText("$0.00")).toBeInTheDocument();
            expect(total.queryByText(/current budget|budget limit/)).not.toBeInTheDocument();
            expect(canvas.getByTestId("usageFreshness")).toHaveTextContent(/^Usage is still being calculated, so totals may be low\.$/);
            expect(canvas.getByText("No cost in this period yet")).toBeInTheDocument();
            expect(canvas.queryByText(/used a model with no price set/)).not.toBeInTheDocument();
        });

        await step("The spender cards and the Models chart say so instead of drawing empty figures", async () => {
            expect(await regionOf(canvasElement, "Top Spenders").findByText("No usage in this period")).toBeInTheDocument();
            await userEvent.click(canvas.getByRole("tab", { name: "Models" }));
            expect(await within(await canvas.findByRole("region", { name: "Daily Cost by Model" })).findByText("No cost in this period yet")).toBeInTheDocument();
        });
    },
};

const failure = () => HttpResponse.json({ message: "Service unavailable", errorId: "mock" }, { status: 500 });

export const LoadErrors: Story = {
    name: "Error",
    parameters: usageMsw(
        undefined,
        http.get("*/api/v2/platform/usage/summary", () => HttpResponse.json({ message: "Usage figures took too long to read; retry shortly, or choose a shorter date range", errorId: "mock-503" }, { status: 503, headers: { "Retry-After": "30" } })),
        http.get("*/api/v2/platform/usage/users", failure)
    ),
    play: async ({ canvasElement, step }) => {
        const canvas = within(canvasElement);

        await step("A slow read offers a way out instead of the server's words", async () => {
            expect(await canvas.findByText("Couldn't load the usage overview. The figures took too long to read. Select Refresh in a moment, or choose a shorter period.")).toBeInTheDocument();
            expect(canvas.queryByText(/retry shortly/)).not.toBeInTheDocument();
        });

        await step("The users lists fail on their own", async () => {
            expect(await regionOf(canvasElement, "Top Spenders").findByText(/Couldn't load spenders/)).toBeInTheDocument();
        });
    },
};
