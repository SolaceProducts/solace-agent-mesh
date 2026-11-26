// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes, useLocation } from "react-router-dom";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { type Budget, useBudget } from "@/api/budgets";
import { useUsageList } from "@/api/usage";

import { usageUserRow } from "@/testing/data/usage";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { BudgetUsagePageContainer } from "./BudgetUsagePageContainer";

vi.mock("@/api/budgets", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/budgets")>()), useBudget: vi.fn() }));
vi.mock("@/api/usage", async importOriginal => ({ ...(await importOriginal<typeof import("@/api/usage")>()), useUsageList: vi.fn() }));

const BUDGET_ID = "0b0d6e70-0000-4000-8000-000000000100";

const standard = { id: BUDGET_ID, name: "Standard Monthly", subjectType: "user", amount: "200.00", warningPercentage: null, isDefault: true } as Budget;

const serveBudget = (query: { data?: Budget; error?: Error | null; isLoading?: boolean }) => vi.mocked(useBudget).mockReturnValue({ data: undefined, error: null, isLoading: false, ...query } as ReturnType<typeof useBudget>);

const valueOf = (label: string) => screen.getByText(label).parentElement as HTMLElement;

function LocationNow() {
    const { pathname, search } = useLocation();
    return <output data-testid="location">{pathname + search}</output>;
}

function renderPage(budgetId = BUDGET_ID, search = "") {
    return renderWithProviders(
        <Routes>
            <Route
                path="/budgets/:budgetId/usage"
                element={
                    <>
                        <BudgetUsagePageContainer />
                        <LocationNow />
                    </>
                }
            />
            <Route path="/usage" element={<LocationNow />} />
        </Routes>,
        { initialPath: `/budgets/${budgetId}/usage${search}` }
    );
}

function serveUsers(rows: ReturnType<typeof usageUserRow>[]) {
    const page = { data: rows, meta: { pagination: { pageNumber: 1, pageSize: 25, count: rows.length, totalPages: 1 } } };
    vi.mocked(useUsageList).mockReturnValue({ data: page, error: null, isLoading: false, isPlaceholderData: false } as unknown as ReturnType<typeof useUsageList>);
}

beforeEach(() => {
    vi.mocked(useUsageList).mockReturnValue({ data: undefined, error: null, isLoading: false, isPlaceholderData: false } as ReturnType<typeof useUsageList>);
});

describe("BudgetUsagePageContainer", () => {
    test("the default budget says it also holds the users with no budget assigned, and reads this month for that budget", () => {
        serveBudget({ data: standard });
        renderPage();
        expect(valueOf("Limit (USD)")).toHaveTextContent("$200.00");
        expect(valueOf("Alert Threshold")).toHaveTextContent("No alert");
        expect(screen.getByText("Includes users with no budget assigned.")).toBeInTheDocument();
        expect(useUsageList).toHaveBeenCalledWith("users", expect.objectContaining({ window: { period: "this_month" }, budgetId: BUDGET_ID, sortBy: "cost", sortDir: "desc" }), { enabled: true });
    });

    test("a users list past the server's time limit says to try again, with no Refresh to point to, and Retry reads it again", async () => {
        serveBudget({ data: standard });
        const refetch = vi.fn();
        const slowRead = Object.assign(new Error("Usage figures took too long to read"), { status: 503 });
        vi.mocked(useUsageList).mockReturnValue({ data: undefined, error: slowRead, isLoading: false, isPlaceholderData: false, refetch } as unknown as ReturnType<typeof useUsageList>);
        renderPage();
        expect(screen.getByText("The figures took too long to read. Try again in a moment.")).toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: "Retry" }));
        expect(refetch).toHaveBeenCalled();
    });

    test.each([
        ["a deleted budget", { error: Object.assign(new Error("Budget not found"), { status: 404 }) }],
        ["the mesh budget", { data: { ...standard, subjectType: "mesh" as const } }],
    ])("%s says there is no such user budget rather than listing no one, and reads no usage", (_name, query) => {
        serveBudget(query);
        renderPage();
        expect(screen.getByText("Budget Not Found")).toBeInTheDocument();
        expect(useUsageList).toHaveBeenCalledWith("users", expect.anything(), { enabled: false });
    });

    test("a user's View Usage opens their panel on the usage page's Users tab, by their id", async () => {
        serveBudget({ data: standard });
        const dana = usageUserRow({ name: "Dana Finance", cost: "40.15", activities: 179 });
        serveUsers([dana]);
        renderPage();
        await userEvent.click(screen.getByTestId("budgetUserMoreTrigger"));
        await userEvent.click(await screen.findByRole("menuitem", { name: "View Usage" }));
        expect(screen.getByTestId("location")).toHaveTextContent(`/usage?type=users&detail=${encodeURIComponent(`users:id:${dana.id}`)}`);
    });

    test("an id that is no id says there is no such budget without reading it", () => {
        serveBudget({});
        renderPage("not-a-uuid");
        expect(screen.getByText("Budget Not Found")).toBeInTheDocument();
        expect(useBudget).toHaveBeenCalledWith("not-a-uuid", { enabled: false });
    });
});
