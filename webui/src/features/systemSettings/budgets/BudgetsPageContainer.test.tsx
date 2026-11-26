// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { Budget } from "@/api/budgets";

import { CAPABILITIES, type Capability } from "@/providers/contexts/UserContext";

import { disableWailsRuntime, enableWailsRuntime } from "@/testing/helpers";
import { getPaginatedMeta, paginatedResponse } from "@/testing/helpers/handlers";
import { platformFetch } from "@/testing/helpers/platformFetch";
import { v2Error } from "@/testing/helpers/platformResponses";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { BudgetsPageContainer } from "./BudgetsPageContainer";

const platform = { get: vi.fn(), post: vi.fn(), patch: vi.fn(), put: vi.fn(), delete: vi.fn() };

function makeBudget(partial: Partial<Budget>): Budget {
    return {
        id: "b",
        name: "Budget",
        subjectType: "user",
        type: "budget",
        amount: "100.00",
        warningPercentage: null,
        isDefault: false,
        assignedCount: 0,
        hiddenAssignedCount: 0,
        pendingCount: 0,
        createdBy: "amy@example.com",
        createdTime: "2026-09-01T00:00:00.000Z",
        updatedBy: "amy@example.com",
        updatedTime: "2026-09-01T00:00:00.000Z",
        ...partial,
    };
}

const engineering = makeBudget({ id: "b-eng", name: "Engineering", amount: "2000.00", warningPercentage: 90 });
const standard = makeBudget({ id: "b-std", name: "Standard Monthly", amount: "200.00", warningPercentage: 80, isDefault: true });

function renderContainer(
    capabilities: Capability[] = [CAPABILITIES.readBudgets, CAPABILITIES.createBudgets, CAPABILITIES.updateBudgets, CAPABILITIES.deleteBudgets, CAPABILITIES.assignBudgets],
    gcTime?: number,
    { usageDashboard = false }: { usageDashboard?: boolean } = {}
) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime }, mutations: { retry: false } } });
    renderWithProviders(
        <QueryClientProvider client={client}>
            <Routes>
                <Route path="/" element={<BudgetsPageContainer />} />
                <Route path="/budgets/:budgetId/usage" element={<p>Budget usage page</p>} />
            </Routes>
        </QueryClientProvider>,
        {
            userContextValues: { userHasCapability: cap => capabilities.includes(cap), hasScope: () => false, scopes: usageDashboard ? ["*:*:read_usage"] : [] },
        }
    );
}

function openRowMenu(name: string) {
    fireEvent.click(within(screen.getByText(name).closest("li")!).getByRole("button", { name: "More" }));
}

const USER_LIST = "/api/v2/platform/budgets?pageNumber=1&pageSize=20&subjectType=user&sort=amount%3Adesc";
const MESH_LIST = "/api/v2/platform/budgets?pageNumber=1&pageSize=1&subjectType=mesh";

let userBudgets: Budget[];
let meshBudgets: Budget[];

const listCalls = (url: string) => platform.get.mock.calls.filter(([called]) => called === url).length;

function pageOf(url: string, items: Budget[]) {
    const query = new URL(url, "http://localhost").searchParams;
    const pageNumber = Number(query.get("pageNumber"));
    const pageSize = Number(query.get("pageSize"));
    const { pagination } = getPaginatedMeta(items.length, pageSize);
    return { data: items.slice((pageNumber - 1) * pageSize, pageNumber * pageSize), meta: { pagination: { ...pagination, pageNumber } } };
}

beforeEach(() => {
    vi.stubGlobal("fetch", platformFetch(platform));
    for (const fn of Object.values(platform)) fn.mockReset().mockResolvedValue(undefined);
    for (const fn of [platform.post, platform.patch, platform.put]) fn.mockResolvedValue({ data: engineering });
    userBudgets = [standard, engineering];
    meshBudgets = [];
    platform.get.mockImplementation(async (url: string) => paginatedResponse(url.includes("subjectType=mesh") ? meshBudgets : userBudgets, 20));
});

afterEach(() => {
    disableWailsRuntime();
    vi.unstubAllGlobals();
});

describe("BudgetsPageContainer", () => {
    test("lists user budgets under a Budgets header", async () => {
        renderContainer();
        expect(await screen.findByText("Engineering")).toBeInTheDocument();
        expect(screen.getByText("Budgets")).toBeInTheDocument();
        expect(screen.getByText("$2,000.00")).toBeInTheDocument();
        expect(platform.get).toHaveBeenCalledWith(USER_LIST);
    });

    test("shows a load failure", async () => {
        platform.get.mockImplementation(async () => v2Error(503, "Service unavailable"));
        renderContainer();
        expect(await screen.findByText("Couldn't Load Budgets")).toBeInTheDocument();
    });

    test("Delete takes assign beside delete", async () => {
        renderContainer([CAPABILITIES.readBudgets, CAPABILITIES.updateBudgets, CAPABILITIES.deleteBudgets]);
        await screen.findByText("Engineering");
        openRowMenu("Engineering");
        expect(screen.getAllByRole("menuitem").map(item => item.textContent)).toEqual(["Edit"]);
    });

    test("delete with assign offers Delete", async () => {
        renderContainer([CAPABILITIES.readBudgets, CAPABILITIES.deleteBudgets, CAPABILITIES.assignBudgets]);
        await screen.findByText("Engineering");
        openRowMenu("Engineering");
        expect(screen.getAllByRole("menuitem").map(item => item.textContent)).toEqual(["Set as Default", "Delete"]);
    });

    test("View Details opens the budget's usage page only for a viewer who may read usage", async () => {
        renderContainer([CAPABILITIES.readBudgets], undefined, { usageDashboard: true });
        await screen.findByText("Engineering");
        openRowMenu("Engineering");
        fireEvent.click(screen.getByRole("menuitem", { name: "View Details" }));
        expect(await screen.findByText("Budget usage page")).toBeInTheDocument();
    });

    test("derives permissions from the budget capabilities", async () => {
        renderContainer([CAPABILITIES.readBudgets]);
        await screen.findByText("Engineering");
        expect(screen.queryByTestId("createBudget")).not.toBeInTheDocument();
        expect(within(screen.getByText("Engineering").closest("li")!).queryByRole("button", { name: "More" })).not.toBeInTheDocument();
    });

    test("the assignments are viewable with only budget read, and manageable with assign", async () => {
        renderContainer([CAPABILITIES.readBudgets]);
        const row = within((await screen.findByText("Engineering")).closest("li")!);
        expect(row.getByRole("button", { name: "View Assignments for Engineering" })).toBeInTheDocument();
        expect(row.queryByRole("button", { name: /^Manage Assignments/ })).not.toBeInTheDocument();
    });

    test("Manage Assignments opens the budget's assignments dialog", async () => {
        platform.get.mockImplementation(async (url: string) => (url.includes("/budgetAssignments") ? paginatedResponse([], 20) : paginatedResponse(url.includes("subjectType=mesh") ? meshBudgets : userBudgets, 20)));
        renderContainer();
        await screen.findByText("Engineering");
        fireEvent.click(within(screen.getByText("Engineering").closest("li")!).getByRole("button", { name: "Manage Assignments for Engineering" }));
        expect(await screen.findByText("No users are assigned to this budget.")).toBeInTheDocument();
        expect(platform.get).toHaveBeenCalledWith("/api/v2/platform/budgetAssignments?budgetId=b-eng&pageNumber=1&pageSize=20&state=concrete");
    });

    test("keystrokes inside the debounce window send one filtered request, not one each", async () => {
        renderContainer();
        await screen.findByText("Engineering");
        const search = screen.getByTestId("budgetNameSearch");
        for (const value of ["e", "en", "eng"]) fireEvent.change(search, { target: { value } });
        await waitFor(() => expect(platform.get).toHaveBeenCalledWith("/api/v2/platform/budgets?pageNumber=1&pageSize=20&subjectType=user&search=eng&sort=amount%3Adesc"));
        const filtered = platform.get.mock.calls.filter(([url]) => String(url).includes("&search="));
        expect(filtered).toEqual([["/api/v2/platform/budgets?pageNumber=1&pageSize=20&subjectType=user&search=eng&sort=amount%3Adesc"]]);
    });

    test("a new name filter starts again from page 1", async () => {
        userBudgets = Array.from({ length: 21 }, (_, i) => makeBudget({ id: `b-${i + 1}`, name: `Team ${String(i + 1).padStart(2, "0")}` }));
        platform.get.mockImplementation(async (url: string) => pageOf(url, url.includes("subjectType=mesh") ? meshBudgets : userBudgets));
        renderContainer();
        await screen.findByText("Team 01");
        fireEvent.click(screen.getByLabelText("Go to next page"));
        expect(await screen.findByText("Team 21")).toBeInTheDocument();

        fireEvent.change(screen.getByTestId("budgetNameSearch"), { target: { value: "Team" } });
        await waitFor(() => expect(platform.get).toHaveBeenCalledWith("/api/v2/platform/budgets?pageNumber=1&pageSize=20&subjectType=user&search=Team&sort=amount%3Adesc"));
        expect(platform.get).not.toHaveBeenCalledWith("/api/v2/platform/budgets?pageNumber=2&pageSize=20&subjectType=user&search=Team&sort=amount%3Adesc");
    });

    test("clearing a filter that matched nothing never shows the unfiltered empty state", async () => {
        platform.get.mockImplementation(async (url: string) => paginatedResponse(url.includes("&search=") ? [] : url.includes("subjectType=mesh") ? meshBudgets : userBudgets, 20));
        renderContainer();
        await screen.findByText("Engineering");
        fireEvent.change(screen.getByTestId("budgetNameSearch"), { target: { value: "zzz" } });
        expect(await screen.findByText("No Budgets Match Your Filters")).toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: "Clear Filters" }));
        expect(screen.queryByText("No Budgets Found")).not.toBeInTheDocument();
        expect(await screen.findByText("Engineering")).toBeInTheDocument();
        expect(screen.queryByText("No Budgets Found")).not.toBeInTheDocument();
    });

    test("with the unfiltered page no longer cached, the filtered placeholder still reads as filtered", async () => {
        let releaseUnfiltered!: () => void;
        let filteredOnce = false;
        platform.get.mockImplementation(async (url: string) => {
            if (url.includes("subjectType=mesh")) return paginatedResponse(meshBudgets, 20);
            if (url.includes("&search=")) {
                filteredOnce = true;
                return paginatedResponse([], 20);
            }
            if (filteredOnce) await new Promise<void>(resolve => (releaseUnfiltered = resolve));
            return paginatedResponse(userBudgets, 20);
        });
        renderContainer(undefined, 0);
        await screen.findByText("Engineering");
        fireEvent.change(screen.getByTestId("budgetNameSearch"), { target: { value: "zzz" } });
        expect(await screen.findByText("No Budgets Match Your Filters")).toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: "Clear Filters" }));
        await waitFor(() => expect(listCalls(USER_LIST)).toBe(2));
        expect(screen.getByText("No Budgets Match Your Filters")).toBeInTheDocument();
        expect(screen.queryByText("No Budgets Found")).not.toBeInTheDocument();

        releaseUnfiltered();
        expect(await screen.findByText("Engineering")).toBeInTheDocument();
    });

    test("creating posts a user-tier budget and refetches the list", async () => {
        renderContainer();
        await screen.findByText("Engineering");
        fireEvent.click(screen.getByTestId("createBudget"));
        const dialog = within(screen.getByRole("dialog"));
        fireEvent.change(dialog.getByLabelText(/^Name/), { target: { value: "Research" } });
        fireEvent.change(dialog.getByLabelText(/^Limit \(USD\)/), { target: { value: "500" } });
        fireEvent.click(dialog.getByRole("button", { name: "Create" }));

        await waitFor(() => expect(platform.post).toHaveBeenCalledWith("/api/v2/platform/budgets", { name: "Research", subjectType: "user", amount: "500", warningPercentage: null }));
        await waitFor(() => expect(listCalls(USER_LIST)).toBe(2));
    });

    test("a duplicate name shows the server's conflict on the Name field", async () => {
        platform.post.mockImplementation(async () => v2Error(409, "A budget with this name already exists."));
        renderContainer();
        await screen.findByText("Engineering");
        fireEvent.click(screen.getByTestId("createBudget"));
        const dialog = within(screen.getByRole("dialog"));
        fireEvent.change(dialog.getByLabelText(/^Name/), { target: { value: "Engineering" } });
        fireEvent.change(dialog.getByLabelText(/^Limit \(USD\)/), { target: { value: "500" } });
        fireEvent.click(dialog.getByRole("button", { name: "Create" }));

        expect(await dialog.findByText("A budget with this name already exists.")).toBeInTheDocument();
        await waitFor(() => expect(dialog.getByLabelText(/^Name/)).toHaveAttribute("aria-invalid", "true"));
    });

    test("the Agent Mesh line shows the mesh budget, fetched separately", async () => {
        meshBudgets = [makeBudget({ id: "b-mesh", name: "Agent Mesh", subjectType: "mesh", amount: "5000.00", warningPercentage: 90 })];
        renderContainer();
        const line = within(screen.getByTestId("meshBudgetSummary"));
        expect(await line.findByText("$5,000.00")).toBeInTheDocument();
        expect(line.getByText("90%")).toBeInTheDocument();
        expect(platform.get).toHaveBeenCalledWith(MESH_LIST);
        await screen.findByText("Engineering");
        expect(within(screen.getByRole("list", { name: "User budgets" })).queryByText("Agent Mesh")).not.toBeInTheDocument();
    });

    test("in the desktop app, shows the Agent Mesh budget and never fetches user budgets", async () => {
        enableWailsRuntime();
        meshBudgets = [makeBudget({ id: "b-mesh", name: "Agent Mesh", subjectType: "mesh", amount: "5000.00" })];
        renderContainer();
        expect(await within(screen.getByTestId("meshBudgetSummary")).findByText("$5,000.00")).toBeInTheDocument();
        expect(screen.getByText(/User budgets are available in production deployments/)).toBeInTheDocument();
        expect(listCalls(USER_LIST)).toBe(0);
        expect(screen.queryByText("Engineering")).not.toBeInTheDocument();
        expect(screen.queryByTestId("createBudget")).not.toBeInTheDocument();
    });

    test("editing from the Agent Mesh line patches only the changed fields", async () => {
        meshBudgets = [makeBudget({ id: "b-mesh", name: "Agent Mesh", subjectType: "mesh", amount: "5000.00" })];
        renderContainer();
        fireEvent.click(await screen.findByTestId("meshBudgetMoreTrigger"));
        fireEvent.click(screen.getByRole("menuitem", { name: "Edit Budget" }));
        const dialog = within(screen.getByRole("dialog"));
        fireEvent.change(dialog.getByLabelText(/^Limit \(USD\)/), { target: { value: "6000" } });
        fireEvent.click(dialog.getByRole("button", { name: "Save" }));
        await waitFor(() => expect(platform.patch).toHaveBeenCalledWith("/api/v2/platform/budgets/b-mesh", { amount: "6000" }));
    });

    test("paging keeps its page while the next one loads, and steps back when the last page empties", async () => {
        userBudgets = Array.from({ length: 21 }, (_, i) => makeBudget({ id: `b-${i + 1}`, name: `Team ${String(i + 1).padStart(2, "0")}` }));
        platform.get.mockImplementation(async (url: string) => pageOf(url, url.includes("subjectType=mesh") ? meshBudgets : userBudgets));
        platform.delete.mockImplementation(async () => {
            userBudgets = userBudgets.slice(0, 20);
        });
        renderContainer();
        await screen.findByText("Team 01");

        fireEvent.click(screen.getByLabelText("Go to next page"));
        expect(await screen.findByText("Team 21")).toBeInTheDocument();
        expect(platform.get).toHaveBeenCalledWith("/api/v2/platform/budgets?pageNumber=2&pageSize=20&subjectType=user&sort=amount%3Adesc");
        expect(screen.getByText("Showing 21-21 of 21")).toBeInTheDocument();

        openRowMenu("Team 21");
        fireEvent.click(screen.getByText("Delete").closest('[role="menuitem"]')!);
        fireEvent.click(screen.getByTestId("dialogConfirmButton"));
        await waitFor(() => expect(listCalls(USER_LIST)).toBe(2));
        expect(await screen.findByText("Team 01")).toBeInTheDocument();
    });

    test("a failed later page shows its error rather than stepping back to page 1", async () => {
        userBudgets = Array.from({ length: 21 }, (_, i) => makeBudget({ id: `b-${i + 1}`, name: `Team ${String(i + 1).padStart(2, "0")}` }));
        platform.get.mockImplementation(async (url: string) => {
            if (url.includes("pageNumber=2")) return v2Error(503, "Service unavailable");
            return pageOf(url, url.includes("subjectType=mesh") ? meshBudgets : userBudgets);
        });
        renderContainer();
        await screen.findByText("Team 01");

        fireEvent.click(screen.getByLabelText("Go to next page"));
        expect(await screen.findByText("Couldn't Load Budgets")).toBeInTheDocument();
        expect(screen.queryByText("Team 01")).not.toBeInTheDocument();
    });

    test("saving an unchanged budget sends no request", async () => {
        renderContainer();
        await screen.findByText("Engineering");
        openRowMenu("Engineering");
        fireEvent.click(screen.getByText("Edit").closest('[role="menuitem"]')!);
        fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Save" }));

        await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
        expect(platform.patch).not.toHaveBeenCalled();
    });

    describe("Set as Default", () => {
        const DEFAULT_LOOKUP = "/api/v2/platform/budgets?pageNumber=1&pageSize=100&subjectType=user";
        const currentDefault = () => within(screen.getByRole("region", { name: "Current Default" }));
        const newDefault = () => within(screen.getByRole("region", { name: "New Default" }));
        const saveButton = () => within(screen.getByRole("dialog")).getByRole("button", { name: "Save" });

        function failLookup() {
            const listed = platform.get.getMockImplementation()!;
            platform.get.mockImplementation(async (url: string) => (url === DEFAULT_LOOKUP ? v2Error(503, "Service unavailable") : listed(url)));
        }

        async function openConfirmation() {
            renderContainer();
            await screen.findByText("Engineering");
            openRowMenu("Engineering");
            fireEvent.click(screen.getByText("Set as Default").closest('[role="menuitem"]')!);
        }

        test("looks up the current default only once the confirmation opens, then compares it and saves", async () => {
            renderContainer();
            await screen.findByText("Engineering");
            expect(listCalls(DEFAULT_LOOKUP)).toBe(0);

            openRowMenu("Engineering");
            fireEvent.click(screen.getByText("Set as Default").closest('[role="menuitem"]')!);
            expect(saveButton()).toBeDisabled();
            expect(await screen.findByRole("region", { name: "Current Default" })).toBeInTheDocument();
            expect(listCalls(DEFAULT_LOOKUP)).toBe(1);
            expect(currentDefault().getByText("Standard Monthly")).toBeInTheDocument();
            expect(newDefault().getByText("Engineering")).toBeInTheDocument();
            expect(newDefault().getByText("Alert Threshold")).toBeInTheDocument();
            expect(within(screen.getByRole("dialog")).getByText(/also applies to every service account with no assigned budget/)).toBeInTheDocument();
            expect(within(screen.getByRole("dialog")).getByText(/Users assigned to/)).toHaveTextContent("Users assigned to Standard Monthly directly keep that budget.");
            expect(platform.put).not.toHaveBeenCalled();

            fireEvent.click(saveButton());
            await waitFor(() => expect(platform.put).toHaveBeenCalledWith("/api/v2/platform/budgets/b-eng/makeDefault"));
        });

        test("while saving, the confirmation keeps the comparison the user confirmed", async () => {
            let finishRefresh!: () => void;
            const listed = platform.get.getMockImplementation()!;
            platform.get.mockImplementation(async (url: string) => {
                if (url !== DEFAULT_LOOKUP || listCalls(DEFAULT_LOOKUP) === 1) return listed(url);
                await new Promise<void>(resolve => (finishRefresh = resolve));
                return paginatedResponse([makeBudget({ ...standard, isDefault: false }), makeBudget({ ...engineering, isDefault: true })], 20);
            });
            await openConfirmation();
            await screen.findByRole("region", { name: "Current Default" });

            fireEvent.click(saveButton());
            await waitFor(() => expect(listCalls(DEFAULT_LOOKUP)).toBe(2));
            expect(currentDefault().getByText("Standard Monthly")).toBeInTheDocument();
            expect(newDefault().getByText("Engineering")).toBeInTheDocument();

            finishRefresh();
            await waitFor(() => expect(screen.queryByRole("region", { name: "New Default" })).not.toBeInTheDocument());
        });

        test("a failed lookup falls back to the default listed on the page", async () => {
            failLookup();
            await openConfirmation();
            expect(await screen.findByRole("region", { name: "Current Default" })).toBeInTheDocument();
            expect(currentDefault().getByText("Standard Monthly")).toBeInTheDocument();
            expect(screen.queryByText(/current default budget couldn't be loaded/)).not.toBeInTheDocument();
        });

        test("a failed lookup with the default not listed says so and shows only the new default, with no system-users note for an unlimited default", async () => {
            userBudgets = [{ ...engineering, amount: null }];
            failLookup();
            await openConfirmation();
            expect(await screen.findByText(/current default budget couldn't be loaded/)).toBeInTheDocument();
            expect(screen.queryByRole("region", { name: "Current Default" })).not.toBeInTheDocument();
            expect(newDefault().getByText("Engineering")).toBeInTheDocument();
            expect(screen.queryByText(/also applies to every service account/)).not.toBeInTheDocument();
        });

        test("a rejected change stays open with the server's message, so it can be retried", async () => {
            platform.put.mockImplementationOnce(async () => v2Error(404, "Budget not found."));
            await openConfirmation();
            await screen.findByRole("region", { name: "Current Default" });
            fireEvent.click(saveButton());
            expect(await screen.findByText("Budget not found.")).toBeInTheDocument();
            expect(currentDefault().getByText("Standard Monthly")).toBeInTheDocument();

            fireEvent.click(saveButton());
            await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
            expect(platform.put).toHaveBeenCalledTimes(2);
        });

        test("Cancel closes the confirmation without saving", async () => {
            await openConfirmation();
            await screen.findByRole("region", { name: "Current Default" });
            fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" }));
            await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
            expect(platform.put).not.toHaveBeenCalled();
        });
    });

    test("deleting confirms, deletes, shows a server refusal, and refetches the list", async () => {
        platform.delete.mockImplementation(async () => v2Error(409, "This budget was assigned while it was being deleted. Try again."));
        renderContainer();
        await screen.findByText("Engineering");
        openRowMenu("Engineering");
        fireEvent.click(screen.getByText("Delete").closest('[role="menuitem"]')!);
        fireEvent.click(screen.getByTestId("dialogConfirmButton"));

        await waitFor(() => expect(platform.delete).toHaveBeenCalledWith("/api/v2/platform/budgets/b-eng"));
        expect(await screen.findByText("This budget was assigned while it was being deleted. Try again.")).toBeInTheDocument();
        await waitFor(() => expect(listCalls(USER_LIST)).toBe(2));
    });
});
