// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import { mockApiUserBudgets } from "@/testing/data";
import { BUDGET_RBAC_SEED, FULL_RBAC_SEED, MANY_USERS_SEED, seedRbacForStory } from "@/testing/data/rbac";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";
import { budgetsListErrorHandler, budgetsListHandler } from "@/testing/msw/budgetHandlers";
import { rbacHandlers } from "@/testing/msw/rbacHandlers";

import { UsersPage } from "./UsersPage";

const server = setupServer(budgetsListHandler(mockApiUserBudgets), ...rbacHandlers);
const requestedUrls: URL[] = [];

beforeAll(() => {
    server.listen({ onUnhandledFrame: "bypass" });
    server.events.on("request:start", ({ request }) => requestedUrls.push(new URL(request.url)));
});
beforeEach(() => seedRbacForStory(FULL_RBAC_SEED));
afterEach(() => {
    server.resetHandlers();
    requestedUrls.length = 0;
});
afterAll(() => server.close());

function renderPage({ readBudgets }: { readBudgets: boolean }) {
    renderWithProviders(
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
            <UsersPage />
        </QueryClientProvider>,
        {
            userContextValues: { userHasCapability: (capability: string) => readBudgets || capability !== CAPABILITIES.readBudgets },
        }
    );
}

function usersRequests(): URL[] {
    return requestedUrls.filter(u => u.pathname.endsWith("/platform/users"));
}

describe("UsersPage budgets", () => {
    test("with budgets enabled, asks for each user's budget and shows the columns", async () => {
        renderPage({ readBudgets: true });
        await screen.findByText("Alice Anderson");
        expect(usersRequests().length).toBeGreaterThan(0);
        expect(usersRequests().every(u => u.searchParams.get("expand") === "budget")).toBe(true);
        expect(screen.getByRole("columnheader", { name: "Budget" })).toBeInTheDocument();
    });

    test("without budget read, sends no expand and shows no budget columns", async () => {
        renderPage({ readBudgets: false });
        await screen.findByText("Alice Anderson");
        expect(usersRequests().length).toBeGreaterThan(0);
        expect(usersRequests().some(u => u.searchParams.has("expand"))).toBe(false);
        expect(screen.queryByRole("columnheader", { name: "Budget" })).not.toBeInTheDocument();
    });
});

describe("UsersPage budget filter", () => {
    beforeEach(() => {
        Element.prototype.scrollIntoView = vi.fn();
        Element.prototype.hasPointerCapture = vi.fn(() => false);
        Element.prototype.releasePointerCapture = vi.fn();
    });

    const chooseBudget = async (name: string) => {
        await userEvent.click(screen.getByRole("combobox", { name: "Filter by budget" }));
        await userEvent.click(await screen.findByRole("option", { name }));
    };

    test("changing it starts again from page 1, and Clear Filters resets it", async () => {
        const onEngineering = Object.fromEntries((MANY_USERS_SEED.users ?? []).map(user => [user.id, "budget-engineering"]));
        seedRbacForStory({ ...MANY_USERS_SEED, ...BUDGET_RBAC_SEED, budgetByUser: onEngineering });
        renderPage({ readBudgets: true });
        await screen.findByText("User 001");
        await userEvent.click(screen.getByLabelText("Go to next page"));
        await screen.findByText("User 021");

        await chooseBudget("Engineering");
        expect(await screen.findByText("User 001")).toBeInTheDocument();

        await chooseBudget("Standard Monthly Default");
        expect(await screen.findByText("No Users Match Your Filters")).toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: "Clear Filters" }));
        expect(await screen.findByText("User 001")).toBeInTheDocument();
        expect(screen.getByRole("combobox", { name: "Filter by budget" })).toHaveTextContent("Any Budget");
    });

    test("when the budgets fail to load, the users still list and the filter is disabled", async () => {
        seedRbacForStory({ ...FULL_RBAC_SEED, ...BUDGET_RBAC_SEED });
        server.use(budgetsListErrorHandler);
        renderPage({ readBudgets: true });
        expect(await screen.findByText("Couldn't load budgets. The budget filter is unavailable.")).toBeInTheDocument();
        expect(screen.getByRole("combobox", { name: "Filter by budget" })).toBeDisabled();
        expect(within(screen.getByText("Alice Anderson").closest("tr")!).getByText("Engineering")).toBeInTheDocument();
    });
});

const veraTwo = (roles: { roleId: string; source: "direct"; manageable: boolean }[] = []) => ({
    id: "u-v2",
    type: "user",
    displayName: "Vera Two",
    userName: null,
    email: "vera@example.com",
    issuer: null,
    sub: null,
    oidcProvider: null,
    provisionedVia: "jit",
    active: true,
    groups: [],
    roles,
    lastLoginTime: null,
    createdBy: null,
    updatedBy: null,
    createdTime: "2026-01-02T03:04:05.678Z",
    updatedTime: "2026-01-02T03:04:05.678Z",
    budget: null,
    budgetTopUp: null,
    effectiveBudgetAmount: null,
});

const v2Page = (data: unknown[]) => ({ data, meta: { pagination: { pageNumber: 1, count: data.length, pageSize: 100, nextPage: null, totalPages: 1 } } });

describe("UsersPage", () => {
    test.each([false, true])("with budget read %s, lists users from /api/v2", async readBudgets => {
        server.use(
            http.get("*/api/v2/platform/rbac/roles", () => HttpResponse.json(v2Page([]))),
            http.get("*/api/v2/platform/users", () => HttpResponse.json(v2Page([veraTwo()])))
        );

        renderPage({ readBudgets });

        await screen.findByText("Vera Two");
        expect(usersRequests().map(u => u.pathname)).toEqual(expect.arrayContaining(["/api/v2/platform/users"]));
        expect(usersRequests().every(u => u.searchParams.has("expand") === readBudgets)).toBe(true);
    });
    describe("role chips", () => {
        const roleId = "3f2a6c1e-8b7d-5e4f-9a0b-1c2d3e4f5a6b";
        const role = { id: roleId, type: "role", name: "Auditor", description: null, scopes: [], inherits: [], origin: "custom", createdBy: null, updatedBy: null, createdTime: null, updatedTime: null };

        beforeEach(() => server.use(http.get("*/api/v2/platform/users", () => HttpResponse.json(v2Page([veraTwo([{ roleId, source: "direct", manageable: true }])])))));

        test("hold a placeholder for roles that arrive after the users, rather than calling a real role unknown", async () => {
            let releaseRoles!: () => void;
            const rolesReleased = new Promise<void>(resolve => (releaseRoles = resolve));
            server.use(
                http.get("*/api/v2/platform/rbac/roles", async () => {
                    await rolesReleased;
                    return HttpResponse.json(v2Page([role]));
                })
            );
            renderPage({ readBudgets: false });

            await screen.findByText("Vera Two");
            expect(screen.getByRole("status", { name: "Loading roles" })).toBeInTheDocument();
            expect(screen.queryByText(/Unknown role/)).not.toBeInTheDocument();

            releaseRoles();
            expect(await screen.findByText("Auditor")).toBeInTheDocument();
        });

        test("show the bare id when the roles fail to load, rather than claiming the role doesn't exist", async () => {
            server.use(http.get("*/api/v2/platform/rbac/roles", () => HttpResponse.json({ message: "down", errorId: "e" }, { status: 500 })));
            renderPage({ readBudgets: false });

            await screen.findByText(roleId);
            expect(screen.queryByText(/Unknown role/)).not.toBeInTheDocument();
        });
    });
});
