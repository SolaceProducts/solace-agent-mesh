// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import { daysLeftInPeriod } from "@/common/usage";

import { usageBudget, usageUserRows } from "@/testing/data/usage";
import { defaultUsageDataset } from "@/testing/msw/usageHandlers";
import { formatUsageMoney } from "@/utils";

import { UsagePageContainer } from "./UsagePageContainer";
import { barOpacities, chartRows, detailPanel, lastAxisDay, recordRequests, regionOf, tableOf, usageAt, usageMsw } from "./usageStories";

const userLists = recordRequests("*/api/v2/platform/usage/users");
const summaries = recordRequests("*/api/v2/platform/usage/summary");
const savingsReads = recordRequests("*/api/v2/platform/usage/savings");
const sortsRequested = () => userLists.urls.map(url => url.searchParams.get("sort")).sort();

const meta = {
    title: "Signals & Usage/Usage/Users",
    component: UsagePageContainer,
    parameters: { layout: "fullscreen", ...usageAt("?type=users"), ...usageMsw(undefined, userLists.handler, summaries.handler) },
    beforeEach: () => {
        userLists.clear();
        summaries.clear();
        savingsReads.clear();
    },
} satisfies Meta<typeof UsagePageContainer>;

export default meta;
type Story = StoryObj<typeof meta>;

async function openUser(canvasElement: HTMLElement, name: string) {
    await userEvent.click(await tableOf(canvasElement).findByRole("button", { name }));
    return detailPanel();
}

async function expectPeriodSpendPanel(dialog: Awaited<ReturnType<typeof detailPanel>>, period: string) {
    const figures = within(await dialog.findByLabelText("Cost and Budget"));
    expect(dialog.getByTestId("usageDetailPeriod")).toHaveTextContent(period);
    expect(figures.getByText("Cost This Period")).toBeInTheDocument();
    expect(figures.getByText("Current Monthly Budget")).toBeInTheDocument();
    expect(dialog.queryByText(/current budget this month/)).not.toBeInTheDocument();
    expect(dialog.queryByRole("alert")).not.toBeInTheDocument();
}

export const ThisMonth: Story = {
    name: "This month",
    parameters: { configContext: { configUseAuthorization: true }, featureContext: { rbac: true } },
    play: async ({ canvasElement, step }) => {
        const canvas = within(canvasElement);

        await step("Overview", async () => {
            const total = within(await canvas.findByTestId("usageTotalSpend"));
            expect(total.getByText("$1,240.58")).toBeInTheDocument();
            expect(total.getByText("same point last month")).toBeInTheDocument();
            expect(canvas.getByRole("region", { name: "Daily Token Cost" }).querySelectorAll("[tabindex]:not([tabindex='-1'])")).toHaveLength(0);
            expect(chartRows(canvasElement, "Daily Token Cost")).toHaveLength(16);
            await waitFor(() => expect(lastAxisDay(canvasElement, "Daily Token Cost")).toHaveAttribute("fill", "var(--secondary-text-w50)"));
            expect(chartRows(canvasElement, "Daily Token Cost")[0]).toHaveTextContent(/[\d.]+[KM] tokens \(Input [\d.]+[KM], Output [\d.]+[KM], Cache read [\d.]+[KM], Cache write [\d.]+[KM]\)/);
            expect(canvas.queryByRole("region", { name: "Cost by Month" })).not.toBeInTheDocument();
            expect(canvas.getByTestId("usageFreshness")).toHaveTextContent(/^Updated [^.]*$/);
        });

        await step("Users tab, sorted by budget usage", async () => {
            expect(canvas.getByRole("tab", { name: "Users" })).toHaveAttribute("aria-selected", "true");
            const table = tableOf(canvasElement);
            await table.findByText("Parminder Procurement");
            expect(table.getByRole("button", { name: /Current Budget/ })).toBeInTheDocument();
            expect(table.getByRole("button", { name: /Budget Usage/ })).toBeInTheDocument();
            expect(table.getByRole("button", { name: /^Cost(?! \/)/ })).toBeInTheDocument();
            expect(table.getAllByRole("row")[1]).toHaveTextContent("Parminder Procurement");
            expect(table.getAllByRole("row")[1].firstElementChild).toHaveTextContent("Approaching budget");
            expect(within(table.getByText("Olive Operations").closest("tr") as HTMLElement).getByText(/^\(\$200 \+ \$50 top-up until .+\)$/)).toHaveClass("sr-only");
            expect(table.getByText("No limit")).toBeInTheDocument();
            expect(table.getAllByText("Service Account").map(badge => badge.closest("tr"))).toEqual([table.getByText("Build Bot").closest("tr")]);
            const unknown = table.getAllByText("Unknown user");
            expect(unknown).toHaveLength(2);
            for (const name of unknown) {
                expect(name).toBeVisible();
                expect(name).not.toHaveClass("sr-only");
                expect(within(name.closest("tr") as HTMLElement).getByRole("button", { name: "Help" })).toBeInTheDocument();
            }
            const bare = unknown.find(name => name.tagName !== "BUTTON") as HTMLElement;
            expect(bare.closest("tr")).toHaveTextContent("$100");
            expect(table.getAllByRole("button", { name: "Unknown user" })).toHaveLength(1);
            expect(table.queryByText(/google-oauth2/)).not.toBeInTheDocument();
            expect(await regionOf(canvasElement, "Top Spenders").findByText("Approaching budget")).toBeInTheDocument();
            await waitFor(() => expect(sortsRequested()).toEqual(["budgetUsage:desc", "cost:asc", "cost:desc"]));
            expect(summaries.urls.map(url => url.search)).toEqual(["?period=this_month"]);
        });

        await step("Sort and filter the table", async () => {
            const table = tableOf(canvasElement);
            await userEvent.click(table.getByRole("button", { name: /Activities/ }));
            await waitFor(() => expect(table.getAllByRole("row")[1]).toHaveTextContent("Unknown user"));
            expect(userLists.urls.some(url => url.searchParams.get("sort") === "activities:asc" && url.searchParams.get("pageSize") === "25")).toBe(true);
            expect(regionOf(canvasElement, "Top Spenders").getAllByRole("listitem")[0]).toHaveTextContent("Parminder Procurement");

            const filter = table.getByPlaceholderText("Filter by name");
            await userEvent.type(filter, "olive");
            await waitFor(() => expect(table.getAllByRole("row")).toHaveLength(2));
            expect(table.getByText("Olive Operations")).toBeInTheDocument();
            await userEvent.clear(filter);
            expect(await table.findByText("Parminder Procurement")).toBeInTheDocument();
        });

        await step("A user near their budget in the side panel", async () => {
            const dialog = await openUser(canvasElement, "Parminder Procurement");
            const warning = await dialog.findByRole("alert");
            expect(warning).toHaveTextContent("Parminder Procurement is at 91.6% of their $450 current budget");
            expect(within(warning).getByRole("button", { name: "Go to Users" })).toBeInTheDocument();
            const parminder = usageUserRows.find(row => row.name === "Parminder Procurement");
            const daysLeft = daysLeftInPeriod(parminder?.budget?.periodEnd ?? "", new Date());
            const spent = formatUsageMoney("412.3000000000");
            expect(dialog.getByText(daysLeft === 0 ? `Cost in a budget period that has ended: ${spent}.` : `Cost this month: ${spent}, with ${daysLeft} ${daysLeft === 1 ? "day" : "days"} left.`)).toBeInTheDocument();
            expect(dialog.getByRole("region", { name: "Cost Versus Current Budget" })).toHaveTextContent("91.6% of $450 current budget this month");
            expect(dialog.queryByRole("region", { name: "Cost by Month" })).not.toBeInTheDocument();
            const roots = within(dialog.getByRole("region", { name: "Top Agents & Workflows" }));
            expect(roots.getByText("Other")).toBeInTheDocument();
            expect(roots.getByText("nightly-report")).toBeInTheDocument();
            expect(roots.queryByRole("button")).not.toBeInTheDocument();
            await userEvent.keyboard("{Escape}");
            await waitFor(() => expect(screen.queryByTestId("usageDetailPanel")).not.toBeInTheDocument());
        });
    },
};

export const LastMonth: Story = {
    name: "Last month",
    parameters: usageAt("?type=users&period=last_month"),
    play: async ({ canvasElement, step }) => {
        const canvas = within(canvasElement);

        await step("Overview", async () => {
            expect(await canvas.findByText("the month before")).toBeInTheDocument();
            expect(chartRows(canvasElement, "Daily Token Cost")).toHaveLength(30);
            await waitFor(() => expect(lastAxisDay(canvasElement, "Daily Token Cost")).toHaveAttribute("fill", "var(--secondary-text-wMain)"));
        });

        await step("Users table without budget standing", async () => {
            const table = tableOf(canvasElement);
            await table.findByText("Parminder Procurement");
            expect(table.getByRole("button", { name: /Current Budget/ })).toBeInTheDocument();
            expect(table.queryByRole("button", { name: /Budget Usage/ })).not.toBeInTheDocument();
            expect(table.queryByText("No limit")).not.toBeInTheDocument();
            expect(canvas.queryByText("Approaching budget")).not.toBeInTheDocument();
            expect(canvas.queryByText("Over budget")).not.toBeInTheDocument();
            await waitFor(() => expect(sortsRequested()).toEqual(["cost:asc", "cost:desc"]));
        });
    },
};

const meshNearBudget = { ...defaultUsageDataset, summary: { ...defaultUsageDataset.summary, meshBudget: usageBudget({ amount: "2000", spend: "1900" }) } };

export const Last7Days: Story = {
    name: "Last 7 days",
    parameters: { ...usageMsw(meshNearBudget, userLists.handler, summaries.handler, savingsReads.handler) },
    play: async ({ canvasElement, step }) => {
        const canvas = within(canvasElement);
        const meshWarning = "Agent Mesh is at 95.0% of its monthly budget";

        await step("Choose the period", async () => {
            await canvas.findByTestId("usageTotalSpend");
            const warning = canvas.getByText(meshWarning).closest("[role=alert]") as HTMLElement;
            expect(within(warning).getByRole("button", { name: "Go to Budgets" })).toBeInTheDocument();
            await userEvent.click(canvas.getByRole("button", { name: /^Period/ }));
            await userEvent.click(await screen.findByRole("button", { name: "Last 7 days" }));
            expect(await canvas.findByText("the previous 7 days")).toBeInTheDocument();
            expect(canvas.getByRole("button", { name: /^Period/ })).toHaveTextContent("Last 7 days");
            expect(summaries.urls.map(url => url.search)).toEqual(["?period=this_month", "?period=last_7_days"]);
            expect(canvas.queryByText(meshWarning)).not.toBeInTheDocument();
        });

        await step("Smart Context savings follow the period", async () => {
            await waitFor(() => expect(savingsReads.urls.map(url => url.search)).toEqual(["?period=this_month", "?period=last_7_days"]));
            expect(await regionOf(canvasElement, "Smart Context Savings").findByText(/^Without Smart Context, the cost would have been/)).toBeInTheDocument();
        });

        await step("One bar for each day selected", async () => {
            await waitFor(() => expect(chartRows(canvasElement, "Daily Token Cost")).toHaveLength(7));
            expect(chartRows(canvasElement, "Daily Token Cost")[0]).toHaveTextContent(/Oct 10/);
        });

        await step("A user's spend this period in the side panel", async () => {
            await waitFor(() => expect(userLists.urls.some(url => url.searchParams.get("period") === "last_7_days")).toBe(true));
            await expectPeriodSpendPanel(await openUser(canvasElement, "Olive Operations"), "Last 7 days");
        });
    },
};

export const Last90Days: Story = {
    name: "Last 90 days",
    parameters: usageAt("?type=users&period=last_90_days"),
    play: async ({ canvasElement, step }) => {
        const canvas = within(canvasElement);

        await step("Weekly token cost, part weeks lighter", async () => {
            await canvas.findByRole("region", { name: "Weekly Token Cost" });
            const weeks = chartRows(canvasElement, "Weekly Token Cost");
            expect(weeks).toHaveLength(14);
            expect(within(weeks[0]).getByRole("rowheader")).toHaveTextContent("Week of Jul 19 (Partial week · 1 of 7 days)");
            expect(within(weeks[1]).getByRole("rowheader")).toHaveTextContent("Week of Jul 20 – 26");
            expect(within(weeks[13]).getByRole("rowheader")).toHaveTextContent("Week of Oct 12 – 16 (Partial week · 5 of 7 days)");
            const chart = canvas.getByRole("region", { name: "Weekly Token Cost" });
            await waitFor(() => {
                const opacities = barOpacities(chart);
                expect([opacities[0], opacities[1], opacities[12], opacities[13]]).toEqual(["0.35", "1", "1", "0.35"]);
            });
        });

        await step("Spend by month, the part month lighter", async () => {
            const months = within(canvasElement).getByRole("region", { name: "Cost by Month" });
            expect(
                within(months)
                    .getAllByRole("rowheader")
                    .map(header => header.textContent)
            ).toEqual([expect.stringMatching(/^Jul.*Partial month · 13 of 31 days/), expect.stringMatching(/^Aug/), expect.stringMatching(/^Sep/), expect.stringMatching(/^Oct/)]);
            await waitFor(() => expect(barOpacities(months).slice(0, 3)).toEqual(["0.35", "1", "1"]));
        });

        await step("A user's spend by month in the side panel", async () => {
            const dialog = await openUser(canvasElement, "Olive Operations");
            await expectPeriodSpendPanel(dialog, "Last 90 days");
            const months = dialog.getByRole("region", { name: "Cost by Month" });
            const rows = within(months).getAllByRole("row").slice(1);
            expect(rows).toHaveLength(4);
            expect(within(rows[0]).getByRole("rowheader")).toHaveTextContent(/Partial month/);
            expect(within(rows[0]).getAllByRole("cell")[1]).toHaveTextContent("Not recorded");
        });
    },
};
