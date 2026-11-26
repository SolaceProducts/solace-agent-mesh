// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { useLocation } from "react-router-dom";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import type { UsageMoney } from "@/api/usage";

import { usageModelRow, usageModelRows, usageRowDaily, usageSummary } from "@/testing/data/usage";
import { defaultUsageDataset } from "@/testing/msw/usageHandlers";
import { formatUsageMoney, NO_TOKENS, remainderUsageMoney } from "@/utils";

import { OBSERVABILITY_ROUTES } from "../routes";

import { UsagePageContainer } from "./UsagePageContainer";
import { chartRows, detailPanel, recordRequests, tableOf, usageAt, usageMsw } from "./usageStories";

const modelRequests = recordRequests("*/api/v2/platform/usage/models*");
const modelLists = () => modelRequests.urls.filter(url => url.pathname.endsWith("/usage/models")).map(url => url.searchParams);
const modelDetails = () => modelRequests.urls.filter(url => !url.pathname.endsWith("/usage/models"));

function LocationProbe() {
    return (
        <output data-testid="locationPath" hidden>
            {useLocation().pathname}
        </output>
    );
}

const routing = usageModelRow({ name: "Routing", cost: "0.0041837", activities: 1200 }, [{ name: "gpt-4.1-nano", provider: "openai", share: 100 }]);
const modelRows = [...usageModelRows, routing];

const meta = {
    title: "Signals & Usage/Usage/Models",
    component: UsagePageContainer,
    parameters: { layout: "fullscreen", ...usageAt("?type=models"), ...usageMsw({ ...defaultUsageDataset, rows: { ...defaultUsageDataset.rows, models: modelRows } }, modelRequests.handler) },
    beforeEach: () => {
        modelRequests.clear();
    },
} satisfies Meta<typeof UsagePageContainer>;

export default meta;
type Story = StoryObj<typeof meta>;

const firstDayOf = (row: (typeof modelRows)[number]) => usageRowDaily(row)[0].cost;
const rowOf = (table: ReturnType<typeof tableOf>, name: string) => within(table.getByRole("button", { name }).closest("tr") as HTMLElement);

export const ThisMonth: Story = {
    name: "This month",
    parameters: { routerValues: { initialPath: `${OBSERVABILITY_ROUTES.usage}?type=models`, routePath: "*" } },
    render: () => (
        <>
            <UsagePageContainer />
            <LocationProbe />
        </>
    ),
    play: async ({ canvasElement, step }) => {
        const canvas = within(canvasElement);

        await step("Daily cost by model, from one top-five list request that carries each row's days", async () => {
            const chart = within(await canvas.findByRole("region", { name: "Daily Cost by Model" }));
            const legend = within(await chart.findByRole("list", { name: "Models shown" }));
            expect(legend.getAllByRole("listitem").map(item => item.textContent)).toEqual([...modelRows.map(row => row.name), "Other"]);
            expect(canvas.queryByRole("region", { name: "Top Spenders" })).not.toBeInTheDocument();
            const firstDay = within((await chart.findAllByRole("row"))[1]);
            expect(firstDay.getByRole("rowheader")).toHaveTextContent("Oct 1");
            const expected = [...modelRows.map(firstDayOf), remainderUsageMoney(usageSummary.daily[0].cost, modelRows.map(firstDayOf))].map(cost => formatUsageMoney(cost));
            expect(firstDay.getAllByRole("cell").map(cell => cell.textContent)).toEqual(expected);
            await waitFor(() =>
                expect(
                    modelLists()
                        .map(params => `${params.get("sort")}|${params.get("pageSize")}|${params.get("expand")}`)
                        .sort()
                ).toEqual(["cost:desc|25|null", "cost:desc|5|daily"])
            );
            expect(modelDetails()).toHaveLength(0);
        });

        await step("Provider models in the table", async () => {
            const table = tableOf(canvasElement);
            expect(table.getByText("gpt-image-1")).toBeInTheDocument();
            const toggle = rowOf(table, "General").getByRole("button", { name: "2 models" });
            await userEvent.click(toggle);
            expect(toggle).toHaveAttribute("aria-expanded", "true");
            for (const model of usageModelRows.find(row => row.name === "General")!.models) {
                expect(within(table.getByText(model.name ?? "").closest("tr") as HTMLElement).getByText(formatUsageMoney(model.cost, 3))).toBeInTheDocument();
            }
            await userEvent.click(toggle);
            expect(table.queryByText("claude-sonnet-4-5")).not.toBeInTheDocument();
        });

        await step("Costs to three decimals, so a small model's show and the decimal points line up; one too small reads as under $0.001", async () => {
            const table = tableOf(canvasElement);
            const [, , cost, , perActivity, , input, output, cacheRead, cacheWrite] = rowOf(table, "Routing")
                .getAllByRole("cell")
                .map(cell => cell.textContent);
            expect({ cost, perActivity, input, output, cacheRead, cacheWrite }).toEqual({ cost: "$0.004", perActivity: "<$0.001", input: "$0.002", output: "$0.002", cacheRead: "<$0.001", cacheWrite: "<$0.001" });
            expect(rowOf(table, "General").getByText("$452.180")).toBeInTheDocument();
        });

        await step("Sort by model: by each row's costliest provider model with a recorded name", async () => {
            const table = tableOf(canvasElement);
            await userEvent.click(table.getByRole("button", { name: /^Model/ }));
            await waitFor(() => expect(modelLists().some(params => params.get("sort") === "model:asc" && params.get("pageSize") === "25")).toBe(true));
            await waitFor(() =>
                expect(
                    table
                        .getAllByRole("row")
                        .slice(1)
                        .map(row => within(row).getAllByRole("cell")[0].textContent)
                ).toEqual(["General", "gpt-4.1-mini", "Routing", "Image Generation", "Planning"])
            );
        });

        await step("A model from the legend, then one with no configuration by its booked name", async () => {
            await userEvent.click(within(canvas.getByRole("list", { name: "Models shown" })).getByRole("button", { name: "General" }));
            expect(await (await detailPanel()).findByRole("heading", { name: "General" })).toBeInTheDocument();
            await userEvent.keyboard("{Escape}");
            await waitFor(() => expect(canvas.queryByTestId("usageDetailPanel")).not.toBeInTheDocument());

            await userEvent.click(tableOf(canvasElement).getByRole("button", { name: "gpt-4.1-mini" }));
            const dialog = await detailPanel();
            expect(within(await dialog.findByRole("region", { name: "Provider Models" })).getByText("$41.60")).toBeInTheDocument();
            expect(dialog.getByRole("region", { name: "Cost Over Time" })).toBeInTheDocument();
            expect(modelDetails().some(url => url.pathname.endsWith("/usage/models/byName") && url.searchParams.get("name") === "gpt-4.1-mini")).toBe(true);
            await userEvent.keyboard("{Escape}");
            await waitFor(() => expect(screen.queryByTestId("usageDetailPanel")).not.toBeInTheDocument());
        });

        await step("The unpriced-calls warning leads to the Models page", async () => {
            await userEvent.click(within(canvas.getByRole("alert")).getByRole("button", { name: "Go to Models" }));
            await waitFor(() => expect(canvas.getByTestId("locationPath")).toHaveTextContent(/^\/models$/));
        });
    },
};

const NINETY_DAYS_FROM = Date.UTC(2026, 6, 19);
const NINETY_DAYS_WINDOW = { startTime: "2026-07-19T00:00:00.000Z", endTime: usageSummary.window.endTime } as const;
const ninetyDays = (cost: UsageMoney) => Array.from({ length: 90 }, (_, index) => ({ date: new Date(NINETY_DAYS_FROM + index * 86_400_000).toISOString().slice(0, 10), cost, tokens: NO_TOKENS, activities: 1 }));

export const Last90Days: Story = {
    name: "Last 90 days",
    parameters: {
        ...usageAt("?type=models&period=last_90_days"),
        ...usageMsw({
            summary: { ...usageSummary, window: NINETY_DAYS_WINDOW, daily: ninetyDays("10.0000000000") },
            rows: { ...defaultUsageDataset.rows, models: usageModelRows.map(row => ({ ...row, daily: ninetyDays("1.0000000000") })) },
        }),
    },
    play: async ({ canvasElement, step }) => {
        await step("Weekly cost by model", async () => {
            await within(canvasElement).findByRole("region", { name: "Weekly Cost by Model" });
            await waitFor(() => expect(chartRows(canvasElement, "Weekly Cost by Model")).toHaveLength(14));
            const weeks = chartRows(canvasElement, "Weekly Cost by Model");
            expect(within(weeks[0]).getByRole("rowheader")).toHaveTextContent("Week of Jul 19 (Partial week · 1 of 7 days)");
            expect(
                within(weeks[1])
                    .getAllByRole("cell")
                    .map(cell => cell.textContent)
            ).toEqual(["$7.00", "$7.00", "$7.00", "$7.00", "$42.00"]);
        });

        await step("A model's weekly trends in the side panel", async () => {
            await userEvent.click(within(within(canvasElement).getByRole("list", { name: "Models shown" })).getByRole("button", { name: "General" }));
            const cost = await (await detailPanel()).findByRole("region", { name: "Cost Over Time" });
            expect(within(cost).getAllByRole("row").slice(1)).toHaveLength(14);
        });
    },
};
