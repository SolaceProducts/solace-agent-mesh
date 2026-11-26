// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import { UsagePageContainer } from "./UsagePageContainer";
import { detailPanel, recordRequests, regionOf, tableOf, usageAt, usageMsw } from "./usageStories";

const workflowLists = recordRequests("*/api/v2/platform/usage/workflows");

const meta = {
    title: "Signals & Usage/Usage/Workflows",
    component: UsagePageContainer,
    parameters: { layout: "fullscreen", ...usageAt("?type=workflows"), ...usageMsw(undefined, workflowLists.handler) },
    beforeEach: () => {
        workflowLists.clear();
    },
} satisfies Meta<typeof UsagePageContainer>;

export default meta;
type Story = StoryObj<typeof meta>;

export const ThisMonth: Story = {
    name: "This month",
    play: async ({ canvasElement, step }) => {
        const canvas = within(canvasElement);

        await step("Spender cards and the table, from two list calls", async () => {
            const table = tableOf(canvasElement);
            await table.findByText("Invoice Reconciliation");
            const top = regionOf(canvasElement, "Top Spenders");
            await waitFor(() => expect(top.getAllByRole("listitem")).toHaveLength(5));
            expect(top.queryByText("Other")).not.toBeInTheDocument();
            expect(await regionOf(canvasElement, "Lowest Spenders").findByText("Quarterly Forecast")).toBeInTheDocument();
            expect(canvas.getByRole("tabpanel", { name: "Workflows" })).toContainElement(canvas.getByRole("region", { name: "Cost Breakdown" }));
            const listCalls = () => workflowLists.urls.filter(url => url.pathname.endsWith("/usage/workflows")).map(url => url.searchParams);
            await waitFor(() =>
                expect(
                    listCalls()
                        .map(params => `${params.get("sort")}|${params.get("pageSize")}|${params.get("pageNumber")}`)
                        .sort()
                ).toEqual(["cost:asc|5|1", "cost:desc|25|1"])
            );
            expect(listCalls().every(params => params.get("period") === "this_month" && !params.has("population"))).toBe(true);
        });

        await step("The Cost Breakdown tooltip names the spend outside workflows and why the rows overlap", async () => {
            await userEvent.click(regionOf(canvasElement, "Cost Breakdown").getByRole("button", { name: "About this table" }));
            expect(await screen.findByText(/It doesn't include \$363\.18 outside workflows/)).toBeInTheDocument();
            expect(screen.getByText(/in the top-level workflow's row/)).toBeInTheDocument();
            await userEvent.keyboard("{Escape}");
            await waitFor(() => expect(screen.queryByText(/outside workflows/)).not.toBeInTheDocument());
        });

        await step("A workflow's nodes in the side panel, opened from anywhere on its row", async () => {
            const name = tableOf(canvasElement).getByRole("button", { name: "Expense Audit" });
            const row = name.closest("tr") as HTMLElement;
            await userEvent.click(within(row).getByText("$57.400"));
            expect(row).toHaveAttribute("data-state", "selected");
            const dialog = await detailPanel();
            expect(await dialog.findByRole("heading", { name: "Expense Audit" })).toBeInTheDocument();
            expect(await dialog.findByText("4.6% of total cost")).toBeInTheDocument();
            const scrolled = (el: Element | null): number[] => (el ? [el.scrollLeft, ...scrolled(el.parentElement)] : []);
            expect(scrolled(name).every(offset => offset === 0)).toBe(true);
            expect(dialog.getByRole("region", { name: "Cost Over Time" })).toBeInTheDocument();
            expect(dialog.getByRole("region", { name: "Activities" })).toBeInTheDocument();
            expect(
                within(dialog.getByRole("region", { name: "Cost Over Time" }))
                    .getAllByRole("row")
                    .slice(1)
            ).toHaveLength(16);
            const nodes = within(dialog.getByRole("region", { name: "Cost by Node" }));
            expect(nodes.getAllByRole("listitem")).toHaveLength(5);
            expect(nodes.getByText("New")).toBeInTheDocument();
            expect(nodes.getByText("Down")).toBeInTheDocument();
            await userEvent.click(nodes.getByRole("button", { name: "View 2 more nodes" }));
            expect(nodes.getAllByRole("listitem")).toHaveLength(7);
            expect(nodes.getByText("Removed node")).toBeInTheDocument();
            await userEvent.keyboard("{Escape}");
            await waitFor(() => expect(screen.queryByTestId("usageDetailPanel")).not.toBeInTheDocument());
            expect(name.closest("[inert]")).toBeNull();
            await waitFor(() => expect(name).toHaveFocus());
            expect(row).not.toHaveAttribute("data-state");
        });
    },
};
