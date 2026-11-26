// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import { usageAgentRows, usageRow } from "@/testing/data/usage";
import { defaultUsageDataset } from "@/testing/msw/usageHandlers";

import { UsagePageContainer } from "./UsagePageContainer";
import { detailPanel, recordRequests, regionOf, tableOf, usageAt, usageMsw } from "./usageStories";

const agentLists = recordRequests("*/api/v2/platform/usage/agents");
const summaries = recordRequests("*/api/v2/platform/usage/summary");

const manyAgents = [...usageAgentRows, ...Array.from({ length: 23 }, (_, i) => usageRow({ name: `Helper ${String(i + 1).padStart(2, "0")}`, cost: String(10 - i * 0.25), activities: 50 + i }))];

const summaryFailsAfterFirst = http.get("*/api/v2/platform/usage/summary", () => (summaries.urls.length > 1 ? HttpResponse.json({ message: "Service unavailable", errorId: "mock" }, { status: 500 }) : undefined));

const meta = {
    title: "Signals & Usage/Usage/Agents",
    component: UsagePageContainer,
    parameters: { layout: "fullscreen", ...usageAt() },
    beforeEach: () => {
        agentLists.clear();
        summaries.clear();
    },
} satisfies Meta<typeof UsagePageContainer>;

export default meta;
type Story = StoryObj<typeof meta>;

async function openBreakdownTooltip(canvasElement: HTMLElement) {
    await userEvent.click(await regionOf(canvasElement, "Cost Breakdown").findByRole("button", { name: "About this table" }));
}

export const ThisMonth: Story = {
    name: "This month",
    parameters: usageMsw({ ...defaultUsageDataset, rows: { ...defaultUsageDataset.rows, agents: manyAgents } }, agentLists.handler, summaries.handler, summaryFailsAfterFirst),
    play: async ({ canvasElement, step }) => {
        const canvas = within(canvasElement);

        await step("The dashboard opens on its first tab, Agents", async () => {
            expect((await canvas.findAllByRole("tab")).map(tab => tab.textContent)).toEqual(["Agents", "Workflows", "Users", "Models"]);
            expect(canvas.getByRole("tab", { name: "Agents" })).toHaveAttribute("aria-selected", "true");
        });

        await step("An agent from Top Spenders: totals and trends, no breakdown", async () => {
            await userEvent.click(await regionOf(canvasElement, "Top Spenders").findByRole("button", { name: /Code Assistant/ }));
            const dialog = await detailPanel();
            expect(await dialog.findByText("$188.40")).toBeInTheDocument();
            expect(dialog.getByRole("region", { name: "Cost Over Time" })).toBeInTheDocument();
            expect(dialog.getByRole("region", { name: "Activities" })).toBeInTheDocument();
            expect(dialog.queryByRole("region", { name: "Cost by Node" })).not.toBeInTheDocument();
            await userEvent.keyboard("{Escape}");
            await waitFor(() => expect(screen.queryByTestId("usageDetailPanel")).not.toBeInTheDocument());
        });

        await step("The Cost Breakdown tooltip names the spend no agent made", async () => {
            await openBreakdownTooltip(canvasElement);
            expect(await screen.findByText(/It doesn't include \$473\.24 of AI usage no agent made/)).toBeInTheDocument();
            await userEvent.keyboard("{Escape}");
            await waitFor(() => expect(screen.queryByText(/AI usage no agent made/)).not.toBeInTheDocument());
        });

        await step("Page through the table", async () => {
            const table = tableOf(canvasElement);
            expect(await table.findByText("Showing 1-25 of 30")).toBeInTheDocument();
            await userEvent.click(table.getByLabelText("Go to next page"));
            expect(await table.findByText("Showing 26-30 of 30")).toBeInTheDocument();
            await waitFor(() => expect(agentLists.urls.some(url => url.searchParams.get("pageNumber") === "2" && url.searchParams.get("pageSize") === "25")).toBe(true));
        });

        await step("Refresh keeps the tab and the name filter, starts the table over and keeps the figures when the refetch fails, saying so in a dialog", async () => {
            await userEvent.type(tableOf(canvasElement).getByPlaceholderText("Filter by name"), "qa");
            await waitFor(() => expect(tableOf(canvasElement).getAllByRole("row")).toHaveLength(2));
            const listsBefore = agentLists.urls.length;
            await userEvent.click(canvas.getByRole("button", { name: /Refresh/ }));
            const dialog = await screen.findByRole("dialog", { name: "Couldn't Refresh Usage" });
            expect(within(dialog).getByText("Some figures may be out of date. Service unavailable")).toBeInTheDocument();
            await userEvent.click(within(dialog).getByRole("button", { name: "Close" }));
            await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
            expect(summaries.urls.length).toBeGreaterThan(1);
            expect(agentLists.urls.length).toBeGreaterThan(listsBefore);
            expect(canvas.getByRole("tab", { name: "Agents" })).toHaveAttribute("aria-selected", "true");
            const table = tableOf(canvasElement);
            expect(table.getByPlaceholderText("Filter by name")).toHaveValue("qa");
            await waitFor(() => expect(table.getAllByRole("row")).toHaveLength(2));
            expect(await canvas.findByText("$1,240.58")).toBeInTheDocument();
            expect(canvas.queryByText(/Couldn't load the usage overview/)).not.toBeInTheDocument();
        });
    },
};
