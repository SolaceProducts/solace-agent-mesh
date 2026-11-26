// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import type { Budget } from "@/api/budgets";

import { budgetUsageRoute } from "@/features/systemSettings/routes";

import { USAGE_BUDGETS } from "@/testing/data/usage";
import { budgetHandler, budgetsListHandler } from "@/testing/msw/budgetHandlers";
import { rbacHandlers } from "@/testing/msw/rbacHandlers";

import { BudgetUsagePageContainer } from "./BudgetUsagePageContainer";
import { engineeringDataset, recordRequests, usageMsw } from "./usageStories";

const engineering: Budget = {
    id: USAGE_BUDGETS.engineering.id,
    type: "budget",
    name: "Engineering",
    subjectType: "user",
    amount: "150.00",
    warningPercentage: 80,
    isDefault: false,
    assignedCount: 6,
    hiddenAssignedCount: 0,
    pendingCount: 0,
    createdBy: "amy@example.com",
    createdTime: "2026-09-01T00:00:00.000Z",
    updatedBy: "amy@example.com",
    updatedTime: "2026-09-01T00:00:00.000Z",
};

const userLists = recordRequests("*/api/v2/platform/usage/users");
const names = (canvasElement: HTMLElement) =>
    within(canvasElement)
        .getAllByRole("row")
        .slice(1)
        .map(row => within(row).getAllByRole("cell")[0].textContent);

const meta = {
    title: "Signals & Usage/Usage/Budget",
    component: BudgetUsagePageContainer,
    parameters: {
        layout: "fullscreen",
        routerValues: { initialPath: budgetUsageRoute(engineering.id), routePath: "/budgets/:budgetId/usage" },
        ...usageMsw(engineeringDataset, userLists.handler, budgetHandler([engineering]), budgetsListHandler([engineering]), ...rbacHandlers),
        configContext: { configUseAuthorization: true },
        featureContext: { rbac: true },
    },
    beforeEach: () => {
        userLists.clear();
    },
} satisfies Meta<typeof BudgetUsagePageContainer>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Engineering: Story = {
    play: async ({ canvasElement, step }) => {
        const canvas = within(canvasElement);

        await step("This month's spend by the users on the budget, highest first", async () => {
            expect(await canvas.findByRole("heading", { name: "Engineering" })).toBeInTheDocument();
            for (const [label, value] of [
                ["Scope", "User"],
                ["Period", "Calendar month"],
                ["Limit (USD)", "$150.00"],
                ["Alert Threshold", "80%"],
            ]) {
                expect(canvas.getByText(label).parentElement).toHaveTextContent(value);
            }
            expect(canvas.getByRole("heading", { name: "User Usage" })).toBeInTheDocument();
            expect(canvas.queryByText("Includes users with no budget assigned.")).not.toBeInTheDocument();
            await waitFor(() => expect(names(canvasElement)).toEqual(["Avery PlatformOver budget", "Taylor Mobile", "Quinn DataApproaching budget", "Dana Finance", "Build BotService Account", "Lee Newhire"]));
            expect(userLists.urls[0].search).toBe(`?period=this_month&pageNumber=1&pageSize=25&sort=cost%3Adesc&budgetId=${engineering.id}`);
            const taylor = within(canvas.getByText("Taylor Mobile").closest("tr") as HTMLElement);
            expect(taylor.getByText("+$50")).toBeInTheDocument();
            expect(taylor.getByText("70.6%")).toBeInTheDocument();
            expect(within(canvas.getByText("Dana Finance").closest("tr") as HTMLElement).getByText("—")).toBeInTheDocument();
        });

        await step("Sort by usage and filter by name", async () => {
            await userEvent.click(canvas.getByRole("button", { name: /^Budget Usage/ }));
            await waitFor(() => expect(names(canvasElement)[0]).toBe("Lee Newhire"));
            await userEvent.type(canvas.getByPlaceholderText("Filter by name"), "quinn");
            await waitFor(() => expect(names(canvasElement)).toEqual(["Quinn DataApproaching budget"]));
            expect(userLists.urls.at(-1)?.searchParams.get("search")).toBe("quinn");
        });

        await step("A user's menu views their usage or manages their budget", async () => {
            await userEvent.click(canvas.getByTestId("budgetUserMoreTrigger"));
            expect((await screen.findAllByRole("menuitem")).map(item => item.textContent)).toEqual(["View Usage", "Manage Budget"]);
            await userEvent.click(screen.getByRole("menuitem", { name: "Manage Budget" }));
            const manage = await screen.findByRole("dialog", { name: "Manage Budget" });
            await userEvent.click(within(manage).getByRole("button", { name: "Cancel" }));
            await waitFor(() => expect(screen.queryByRole("dialog", { name: "Manage Budget" })).not.toBeInTheDocument());
        });
    },
};
