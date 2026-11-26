// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, test, vi } from "vitest";

import type { AppliedBudget } from "@/api/budgets";

import type { RBACRole, User } from "@/types";

import { UsersTable } from "./UsersTable";

const platform = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), patch: vi.fn(), put: vi.fn(), delete: vi.fn() }));
vi.mock("@/api/client", () => ({ api: { platform } }));

function renderTable(rows: User[], onSort = vi.fn(), showBudgets?: boolean, canEditBudget?: boolean) {
    render(
        <OpenFeatureTestProvider flagValueMap={{}}>
            <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
                <MemoryRouter>
                    <UsersTable users={rows} roleById={roleById} sortKey="displayName" sortDir="asc" onSort={onSort} showBudgets={showBudgets} canEditBudget={canEditBudget} />
                </MemoryRouter>
            </QueryClientProvider>
        </OpenFeatureTestProvider>
    );
    return { onSort };
}

const roleById = new Map<string, RBACRole>([["role-admin", { id: "role-admin", name: "Administrator", description: "", origin: "yaml", scopes: [], createdBy: "", updatedBy: "", createdAt: "", updatedAt: "" }]]);

const users: User[] = [
    { id: "2b000000-0000-4000-8000-000000000001", displayName: "Ada Lovelace", email: "ada@example.com", sub: "oidc|ada", roles: [{ roleId: "role-admin", source: "yaml" }] },
    { id: "2b000000-0000-4000-8000-000000000002", email: "noname@example.com", sub: "oidc|noname", roles: [] },
];

const systemUser: User = {
    id: "019fd49f-2ec6-7c50-8b65-b8e953426f90",
    displayName: "CFG Test Deployer v2",
    email: "",
    sub: "system:cfgtest-deployer",
    provisionedVia: "system",
    roles: [],
};

describe("UsersTable", () => {
    test("shows name and email for a named user, plus their role chip", () => {
        renderTable(users);
        expect(screen.getByText("Ada Lovelace")).toBeInTheDocument();
        expect(screen.getByText("ada@example.com")).toBeInTheDocument();
        expect(screen.getByText("Administrator")).toBeInTheDocument();
    });

    test("shows an em dash above the email for a user with no display name", () => {
        renderTable(users);
        const row = screen.getByText("noname@example.com").closest("tr")!;
        expect(within(row).getByText("—")).toBeInTheDocument();
        expect(within(screen.getByText("Ada Lovelace").closest("tr")!).queryByText("—")).not.toBeInTheDocument();
    });

    test("renders a row actions trigger per user", () => {
        renderTable(users);
        expect(screen.getAllByTestId("userRowMoreTrigger")).toHaveLength(users.length);
    });

    test("badges a system-provisioned user and labels the name its entrypoints run as", () => {
        renderTable([...users, systemUser]);

        const row = screen.getByText("CFG Test Deployer v2").closest("tr")!;
        expect(within(row).getByText("Service Account")).toBeInTheDocument();
        expect(within(row).getByText("Run as: cfgtest-deployer")).toBeInTheDocument();

        expect(within(screen.getByText("Ada Lovelace").closest("tr")!).queryByText("Service Account")).not.toBeInTheDocument();
    });

    test("offers no row actions for a system user, so its roles cannot be edited here", () => {
        renderTable([...users, systemUser]);

        const systemRow = screen.getByText("CFG Test Deployer v2").closest("tr")!;
        expect(within(systemRow).queryByTestId("userRowMoreTrigger")).not.toBeInTheDocument();

        const humanRow = screen.getByText("Ada Lovelace").closest("tr")!;
        expect(within(humanRow).getByTestId("userRowMoreTrigger")).toBeInTheDocument();
    });

    test("clicking the User header asks to sort by display name", async () => {
        const { onSort } = renderTable(users);

        await userEvent.click(screen.getByRole("button", { name: "User" }));

        expect(onSort).toHaveBeenCalledWith("displayName");
    });
});

describe("UsersTable budget columns", () => {
    const [ada] = users;
    const budgetCell = (name: string) => within(screen.getByText(name).closest("tr")!).getAllByRole("cell")[2];
    const withAdaOn = (budget: AppliedBudget | null) => users.map(user => (user.id === ada.id ? { ...user, budget } : user));

    test("show the budget that applies, an assigned one without an icon, its limit as hidden text", () => {
        renderTable(withAdaOn({ id: "b-eng", name: "Engineering", amount: "2000.00", source: "assigned", assignmentId: "a-1" }), vi.fn(), true);
        expect(screen.getAllByRole("columnheader").map(h => h.textContent)).toEqual(["User", "Roles", "Budget", ""]);
        const budget = within(budgetCell("Ada Lovelace"));
        expect(budget.getByText("Engineering").closest("[data-slot=badge]")!.querySelector("svg")).toBeNull();
        expect(budget.getByText("Limit: $2,000.00 (USD)")).toHaveClass("sr-only");
    });

    test("mark a budget that applies through the default, an unlimited one's limit Unlimited", async () => {
        renderTable(withAdaOn({ id: "b-std", name: "Standard", amount: null, source: "default", assignmentId: null }), vi.fn(), true);
        const chip = within(budgetCell("Ada Lovelace")).getByText("Standard").closest("[data-slot=badge]")!;
        expect(chip.querySelector("svg")).not.toBeNull();
        await userEvent.hover(chip);
        expect((await screen.findAllByText("Assigned through the default user budget. Limit: Unlimited")).length).toBeGreaterThan(0);
    });

    test("with no budget at all, or read without the expand, the budget is —", () => {
        renderTable(withAdaOn(null), vi.fn(), true);
        expect(budgetCell("Ada Lovelace")).toHaveTextContent("—");
        expect(budgetCell("noname@example.com")).toHaveTextContent("—");
    });

    const ENGINEERING_ASSIGNED: AppliedBudget = { id: "b-eng", name: "Engineering", amount: "2000.00", source: "assigned", assignmentId: "a-1" };
    const TOP_UP = { id: "t-1", amount: "100.00", expiresTime: "2026-11-01T00:00:00.000Z" };

    test("an additional amount sits beside the budget, and the limit includes it", async () => {
        renderTable(
            users.map(user => (user.id === ada.id ? { ...user, budget: ENGINEERING_ASSIGNED, budgetTopUp: TOP_UP, effectiveBudgetAmount: "2100.00" } : user)),
            vi.fn(),
            true
        );
        const budget = budgetCell("Ada Lovelace");
        const topUpChip = within(budget).getByText("+ $100.00").closest("[data-slot=badge]")!;
        await userEvent.hover(within(budget).getByText("Engineering"));
        expect((await screen.findAllByText("Limit: $2,100.00 (USD), including the additional amount")).length).toBeGreaterThan(0);
        await userEvent.hover(topUpChip);
        expect((await screen.findAllByText("Additional amount until Nov 1, 2026, 12:00 AM UTC")).length).toBeGreaterThan(0);
    });

    test("an additional amount on an unlimited budget is muted and says it has no effect", async () => {
        renderTable(
            users.map(user => (user.id === ada.id ? { ...user, budget: { ...ENGINEERING_ASSIGNED, amount: null }, budgetTopUp: TOP_UP, effectiveBudgetAmount: null } : user)),
            vi.fn(),
            true
        );
        const topUpChip = within(budgetCell("Ada Lovelace")).getByText("+ $100.00").closest("[data-slot=badge]")!;
        expect(topUpChip).toHaveClass("border-dashed");
        await userEvent.hover(topUpChip);
        expect((await screen.findAllByText("Additional amount until Nov 1, 2026, 12:00 AM UTC. It has no effect while Engineering is unlimited.")).length).toBeGreaterThan(0);
    });
});

describe("UsersTable Manage Budget", () => {
    const menuItems = async (name: string) => {
        await userEvent.click(within(screen.getByText(name).closest("tr")!).getByTestId("userRowMoreTrigger"));
        return screen.getAllByRole("menuitem").map(item => item.textContent);
    };

    test("is offered beside Edit User Roles when the viewer can assign budgets", async () => {
        renderTable(users, vi.fn(), false, true);
        expect(await menuItems("Ada Lovelace")).toEqual(["Edit User Roles", "Manage Budget"]);
    });

    test("is not offered otherwise", async () => {
        renderTable(users);
        expect(await menuItems("Ada Lovelace")).toEqual(["Edit User Roles"]);
    });

    test("is a system user's only action", async () => {
        renderTable([...users, systemUser], vi.fn(), false, true);
        expect(await menuItems("CFG Test Deployer v2")).toEqual(["Manage Budget"]);
    });
});
