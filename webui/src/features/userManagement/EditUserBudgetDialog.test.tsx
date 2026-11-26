// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { AppliedBudget, AppliedTopUp, Budget } from "@/api/budgets";

import { platformFetch } from "@/testing/helpers/platformFetch";
import { v2Error, v2Page } from "@/testing/helpers/platformResponses";
import type { User } from "@/types";

import { EditUserBudgetDialog } from "./EditUserBudgetDialog";

const platform = { get: vi.fn(), post: vi.fn(), patch: vi.fn(), put: vi.fn(), delete: vi.fn() };

const user: User = { id: "u-ada", displayName: "Ada Lovelace", email: "ada@example.com", roles: [] };

const budget = (partial: Partial<Budget>): Budget => ({
    id: "b",
    type: "budget",
    name: "Budget",
    subjectType: "user",
    amount: "100.00",
    warningPercentage: null,
    isDefault: false,
    assignedCount: 0,
    hiddenAssignedCount: 0,
    pendingCount: 0,
    createdBy: "x",
    createdTime: "2026-09-01T00:00:00.000Z",
    updatedBy: "x",
    updatedTime: "2026-09-01T00:00:00.000Z",
    ...partial,
});
const standard = budget({ id: "b-std", name: "Standard", amount: "200.00", isDefault: true });
const engineering = budget({ id: "b-eng", name: "Engineering", amount: "2000.00" });
const open = budget({ id: "b-open", name: "Open", amount: null });

const withBudget = (applied: AppliedBudget | null): User => ({ ...user, budget: applied });
const ASSIGNED_ENG = withBudget({ id: "b-eng", name: "Engineering", amount: "2000.00", source: "assigned", assignmentId: "a-1" });
const UNDER_DEFAULT = withBudget({ id: "b-std", name: "Standard", amount: "200.00", source: "default", assignmentId: null });
const PINNED_STD = withBudget({ id: "b-std", name: "Standard", amount: "200.00", source: "assigned", assignmentId: "a-2" });

const USER_PATH = "/api/v2/platform/users/";
const ASSIGNMENTS = "/api/v2/platform/budgetAssignments";
const TOP_UP_PATH = "/api/v2/platform/budgetSubjects/u-ada/topUp";

const assignmentResponse = (id: string, budgetId: string) => ({
    data: {
        id,
        type: "budgetAssignment",
        state: "concrete",
        subject: { type: "user", id: "u-ada", displayName: null, user: null, identity: null },
        budget: { id: budgetId, name: "Budget" },
        createdBy: "x",
        createdTime: null,
        updatedBy: "x",
        updatedTime: null,
    },
});
const topUpResponse = (amount: string) => ({ data: { amount: amount === "0" ? null : amount, expiresTime: null, createdBy: null, createdTime: null, updatedBy: null, updatedTime: null } });

const userReads = () => platform.get.mock.calls.filter(([url]) => String(url).startsWith(USER_PATH));

const userResponse = ({ id, displayName, email, budget, budgetTopUp }: User) => ({
    data: {
        id,
        displayName: displayName ?? null,
        userName: null,
        email: email ?? null,
        issuer: null,
        sub: null,
        oidcProvider: null,
        provisionedVia: "jit",
        type: "user",
        active: true,
        groups: [],
        roles: [],
        lastLoginTime: null,
        createdBy: null,
        updatedBy: null,
        createdTime: null,
        updatedTime: null,
        budget: budget ?? null,
        budgetTopUp: budgetTopUp ?? null,
        effectiveBudgetAmount: null,
    },
});

function answer(currentUser: () => User) {
    platform.get.mockImplementation(async (url: string) => (url.startsWith(USER_PATH) ? userResponse(currentUser()) : v2Page([standard, engineering, open])));
}

const serve = (current: User) => answer(() => current);

const TOP_UP: AppliedTopUp = { amount: "100.00", expiresTime: "2026-11-01T00:00:00.000Z" };
const withTopUp = (state: User, topUp: AppliedTopUp | null): User => ({ ...state, budgetTopUp: topUp });
const amountInput = () => screen.getByRole("textbox", { name: "Additional Temporary Amount (USD)" });

async function enterAmount(text: string) {
    await userEvent.clear(amountInput());
    if (text) await userEvent.type(amountInput(), text);
}

function renderDialog() {
    const onOpenChange = vi.fn();
    render(
        <OpenFeatureTestProvider flagValueMap={{}}>
            <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}>
                <EditUserBudgetDialog user={user} open onOpenChange={onOpenChange} />
            </QueryClientProvider>
        </OpenFeatureTestProvider>
    );
    return { onOpenChange };
}

const save = () => screen.getByRole("button", { name: "Save" });
const revertToDefault = () => screen.getByRole("button", { name: "Revert to Default" });

async function choose(budgetName: string) {
    await userEvent.click(screen.getByRole("combobox", { name: "Budget" }));
    await userEvent.click(await screen.findByRole("option", { name: new RegExp(`^${budgetName}\\b`) }));
}

beforeEach(() => {
    vi.stubGlobal("fetch", platformFetch(platform));
    localStorage.clear();
    for (const fn of Object.values(platform)) fn.mockReset().mockResolvedValue(undefined);
    platform.post.mockImplementation(async (_url: string, body: { budgetId: string }) => assignmentResponse("a-new", body.budgetId));
    platform.patch.mockImplementation(async (url: string, body: { budgetId: string }) => assignmentResponse(url.split("/").at(-1)!, body.budgetId));
    platform.put.mockImplementation(async (_url: string, body: { amount: string }) => topUpResponse(body.amount));
});
afterEach(() => vi.unstubAllGlobals());

describe("EditUserBudgetDialog", () => {
    test("asks for this user's budget by id and lists the user budgets", async () => {
        serve(ASSIGNED_ENG);
        renderDialog();
        await waitFor(() => expect(screen.getByRole("combobox", { name: "Budget" })).toHaveTextContent("Engineering $2,000.00"));
        expect(userReads().map(([url]) => url)).toContain(`${USER_PATH}u-ada?expand=budget`);
        expect(platform.get).toHaveBeenCalledWith("/api/v2/platform/budgets?pageNumber=1&pageSize=100&subjectType=user");
    });

    test("each option badges the default and tags the user's current budget, with its limit beneath", async () => {
        serve(ASSIGNED_ENG);
        renderDialog();
        await waitFor(() => expect(screen.getByRole("combobox", { name: "Budget" })).toBeEnabled());
        await userEvent.click(screen.getByRole("combobox", { name: "Budget" }));
        expect(await screen.findByRole("option", { name: "Engineering Current" })).toHaveTextContent("$2,000.00");
        expect(screen.getByRole("option", { name: "Standard Default" })).toHaveTextContent("$200.00");
        expect(screen.getByRole("option", { name: "Open" })).toHaveTextContent("Unlimited");
    });

    test("moving an assigned user patches their assignment", async () => {
        serve(ASSIGNED_ENG);
        renderDialog();
        await waitFor(() => expect(revertToDefault()).toBeEnabled());
        expect(save()).toBeDisabled();
        await choose("Standard");
        await userEvent.click(save());
        await waitFor(() => expect(platform.patch).toHaveBeenCalledWith(`${ASSIGNMENTS}/a-1`, { budgetId: "b-std" }));
        expect(platform.post).not.toHaveBeenCalled();
    });

    test("clearing an assigned user deletes their assignment", async () => {
        serve(ASSIGNED_ENG);
        renderDialog();
        await waitFor(() => expect(revertToDefault()).toBeEnabled());
        await userEvent.click(revertToDefault());
        expect(revertToDefault()).toBeDisabled();
        await userEvent.click(save());
        await waitFor(() => expect(platform.delete).toHaveBeenCalledWith(`${ASSIGNMENTS}/a-1`));
    });

    test("a user under the default starts with nothing selected and Revert to Default disabled", async () => {
        serve(UNDER_DEFAULT);
        renderDialog();
        const trigger = screen.getByRole("combobox", { name: "Budget" });
        await waitFor(() => expect(within(trigger).getByText("Default limit: $200.00")).toBeInTheDocument());
        expect(within(trigger).getByText("No budget assigned")).toBeInTheDocument();
        expect(revertToDefault()).toBeDisabled();
        expect(save()).toBeDisabled();
    });

    test("choosing the default for a user under it creates an explicit assignment", async () => {
        serve(UNDER_DEFAULT);
        renderDialog();
        await waitFor(() => expect(screen.getByRole("combobox", { name: "Budget" })).toBeEnabled());
        await choose("Standard");
        await userEvent.click(save());
        await waitFor(() => expect(platform.post).toHaveBeenCalledWith(ASSIGNMENTS, { subjectType: "user", subjectId: "u-ada", budgetId: "b-std" }));
    });

    test("a refused save shows the server's message and keeps the dialog open", async () => {
        serve(UNDER_DEFAULT);
        platform.post.mockImplementation(async () => v2Error(422, "budgetId: no budget with this id"));
        const { onOpenChange } = renderDialog();
        await waitFor(() => expect(screen.getByRole("combobox", { name: "Budget" })).toBeEnabled());
        await choose("Engineering");
        await userEvent.click(save());
        expect(await screen.findByText("budgetId: no budget with this id")).toBeInTheDocument();
        expect(onOpenChange).not.toHaveBeenCalledWith(false);
    });

    test.each([
        { status: 409, method: "post" as const, state: UNDER_DEFAULT, choice: "Engineering" },
        { status: 404, method: "patch" as const, state: ASSIGNED_ENG, choice: "Standard" },
    ])("a $status from a stale dialog asks the admin to choose again, not the server's wording", async ({ status, method, state, choice }) => {
        serve(state);
        platform[method].mockImplementation(async () => v2Error(status, "raw server text"));
        renderDialog();
        await waitFor(() => expect(screen.getByRole("combobox", { name: "Budget" })).toBeEnabled());
        await choose(choice);
        const readsBeforeSave = userReads().length;
        await userEvent.click(save());
        await waitFor(() => expect(userReads().length).toBeGreaterThan(readsBeforeSave));
        expect(await screen.findByText("This user's budget was changed by someone else. The dialog has been refreshed; select a budget again and save.")).toBeInTheDocument();
        expect(screen.queryByText("raw server text")).not.toBeInTheDocument();
    });

    test("prefills the current additional amount", async () => {
        serve(withTopUp(ASSIGNED_ENG, TOP_UP));
        renderDialog();
        await waitFor(() => expect(amountInput()).toHaveValue("100.00"));
        expect(screen.getByText(/Manage the budget for/)).toHaveTextContent(/applies until Nov 1, 2026, 12:00 AM UTC\.$/);
        expect(save()).toBeDisabled();
        await enterAmount("100");
        expect(save()).toBeDisabled();
    });

    test("an amount for a user without one grants it, and touches no assignment", async () => {
        serve(ASSIGNED_ENG);
        renderDialog();
        await waitFor(() => expect(amountInput()).toBeEnabled());
        expect(amountInput()).toHaveValue("");
        await enterAmount("50");
        await userEvent.click(save());
        await waitFor(() => expect(platform.put).toHaveBeenCalledWith(TOP_UP_PATH, { amount: "50" }));
        expect(platform.patch).not.toHaveBeenCalled();
        expect(platform.post).not.toHaveBeenCalled();
    });

    test("a new amount replaces the current one; emptying it removes it", async () => {
        serve(withTopUp(ASSIGNED_ENG, TOP_UP));
        renderDialog();
        await waitFor(() => expect(amountInput()).toHaveValue("100.00"));
        await enterAmount("80");
        await userEvent.click(save());
        await waitFor(() => expect(platform.put).toHaveBeenCalledWith(TOP_UP_PATH, { amount: "80" }));

        await enterAmount("");
        await userEvent.click(save());
        await waitFor(() => expect(platform.put).toHaveBeenLastCalledWith(TOP_UP_PATH, { amount: "0" }));
        expect(platform.put).toHaveBeenCalledTimes(2);
    });

    test("moving the budget and changing the amount sends the move and one top-up", async () => {
        serve(withTopUp(ASSIGNED_ENG, TOP_UP));
        const { onOpenChange } = renderDialog();
        await waitFor(() => expect(amountInput()).toHaveValue("100.00"));
        await choose("Standard");
        await enterAmount("80");
        await userEvent.click(save());
        await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false));
        expect(platform.patch).toHaveBeenCalledWith(`${ASSIGNMENTS}/a-1`, { budgetId: "b-std" });
        expect(platform.put.mock.calls).toEqual([[TOP_UP_PATH, { amount: "80" }]]);
        expect(platform.post).not.toHaveBeenCalled();
    });

    test("while the budget is unlimited, a held amount is named and can still be deleted", async () => {
        serve(withTopUp(ASSIGNED_ENG, TOP_UP));
        renderDialog();
        await waitFor(() => expect(amountInput()).toBeEnabled());
        await choose("Open");
        expect(amountInput()).toBeEnabled();
        expect(screen.getByText("$100.00 is kept but has no effect while Open is unlimited.")).toBeInTheDocument();
        await enterAmount("");
        expect(screen.getByText("Not available when the selected budget is unlimited.")).toBeInTheDocument();
        await userEvent.click(save());
        await waitFor(() => expect(platform.put).toHaveBeenCalledWith(TOP_UP_PATH, { amount: "0" }));
        expect(platform.patch).toHaveBeenCalledWith(`${ASSIGNMENTS}/a-1`, { budgetId: "b-open" });
        expect(platform.post).not.toHaveBeenCalled();
    });

    test("while the budget is unlimited, a held amount cannot be changed to another", async () => {
        serve(withTopUp(ASSIGNED_ENG, TOP_UP));
        renderDialog();
        await waitFor(() => expect(amountInput()).toBeEnabled());
        await choose("Open");
        await enterAmount("80");
        expect(amountInput()).toHaveAttribute("aria-invalid", "true");
        expect(amountInput()).toHaveAccessibleDescription("Can't change the amount while Open is unlimited. Delete it or choose a limited budget.");
        expect(save()).toBeDisabled();
    });

    test("the description says when a new amount expires, the start of next month in UTC", async () => {
        vi.useFakeTimers({ toFake: ["Date"] });
        vi.setSystemTime(new Date("2026-10-31T23:30:00.000Z"));
        try {
            serve(ASSIGNED_ENG);
            renderDialog();
            await waitFor(() => expect(amountInput()).toBeEnabled());
            expect(screen.getByText(/Manage the budget for/)).toHaveTextContent(/applies until Nov 1, 2026, 12:00 AM UTC\.$/);
            await enterAmount("50");
            expect(amountInput()).not.toHaveAccessibleDescription();
        } finally {
            vi.useRealTimers();
        }
    });

    test.each([
        { text: "100.", message: "Enter an amount such as 100 or 100.50" },
        { text: "1.234", message: "Enter an amount such as 100 or 100.50" },
        { text: "0", message: "Enter an amount above 0, or leave it empty for none" },
        { text: "100000.01", message: "Enter at most $100,000.00" },
    ])("refuses $text before saving", async ({ text, message }) => {
        serve(ASSIGNED_ENG);
        renderDialog();
        await waitFor(() => expect(amountInput()).toBeEnabled());
        await enterAmount(text);
        expect(screen.getByText(message)).toBeInTheDocument();
        expect(amountInput()).toHaveAttribute("aria-invalid", "true");
        expect(save()).toBeDisabled();
    });

    test("allows the maximum", async () => {
        serve(ASSIGNED_ENG);
        renderDialog();
        await waitFor(() => expect(amountInput()).toBeEnabled());
        await enterAmount("100000");
        expect(save()).toBeEnabled();
    });

    test("is unavailable while the budget the user ends up on is unlimited", async () => {
        serve(ASSIGNED_ENG);
        renderDialog();
        await waitFor(() => expect(amountInput()).toBeEnabled());
        await enterAmount("50");
        await choose("Open");
        expect(amountInput()).toBeDisabled();
        expect(amountInput()).toHaveAccessibleDescription("Not available when the selected budget is unlimited.");
        await userEvent.click(save());
        await waitFor(() => expect(platform.patch).toHaveBeenCalledWith(`${ASSIGNMENTS}/a-1`, { budgetId: "b-open" }));
        expect(platform.post).not.toHaveBeenCalled();
        expect(platform.put).not.toHaveBeenCalled();
        expect(platform.delete).not.toHaveBeenCalled();
    });

    test("saves the budget before the amount, so the amount is checked against the new budget", async () => {
        serve(UNDER_DEFAULT);
        renderDialog();
        await waitFor(() => expect(amountInput()).toBeEnabled());
        await choose("Engineering");
        await enterAmount("25");
        await userEvent.click(save());
        await waitFor(() => expect(platform.put).toHaveBeenCalledWith(TOP_UP_PATH, { amount: "25" }));
        expect(platform.post).toHaveBeenCalledWith(ASSIGNMENTS, { subjectType: "user", subjectId: "u-ada", budgetId: "b-eng" });
        expect(platform.post.mock.invocationCallOrder[0]).toBeLessThan(platform.put.mock.invocationCallOrder[0]);
    });

    test("says the budget was saved when only the amount fails", async () => {
        serve(UNDER_DEFAULT);
        platform.put.mockImplementation(async () => v2Error(409, "The budget that applies to this subject is unlimited"));
        const { onOpenChange } = renderDialog();
        await waitFor(() => expect(amountInput()).toBeEnabled());
        await choose("Engineering");
        await enterAmount("25");
        await userEvent.click(save());
        expect(await screen.findByText("The budget was saved, but the additional amount was not: The budget that applies to this subject is unlimited")).toBeInTheDocument();
        expect(onOpenChange).not.toHaveBeenCalledWith(false);
    });

    test("a retry after only the amount failed sends only the amount", async () => {
        let current = UNDER_DEFAULT;
        answer(() => current);
        let refuseTopUp = true;
        platform.post.mockImplementation(async (_url: string, body: { budgetId: string }) => {
            current = withBudget({ id: "b-eng", name: "Engineering", amount: "2000.00", source: "assigned", assignmentId: "a-new" });
            return assignmentResponse("a-new", body.budgetId);
        });
        platform.put.mockImplementation(async (_url: string, body: { amount: string }) => (refuseTopUp ? v2Error(503, "try again") : topUpResponse(body.amount)));
        renderDialog();
        await waitFor(() => expect(amountInput()).toBeEnabled());
        await choose("Engineering");
        await enterAmount("25");
        await userEvent.click(save());
        expect(await screen.findByText("The budget was saved, but the additional amount was not: try again")).toBeInTheDocument();

        refuseTopUp = false;
        await waitFor(() => expect(userReads().length).toBeGreaterThan(1));
        platform.post.mockClear();
        platform.put.mockClear();
        await userEvent.click(save());
        await waitFor(() => expect(platform.put).toHaveBeenCalledTimes(1));
        expect(platform.put).toHaveBeenCalledWith(TOP_UP_PATH, { amount: "25" });
        expect(platform.post).not.toHaveBeenCalled();
    });

    test("cancelling an amount for a user who no longer exists shows the failure", async () => {
        serve(withTopUp(ASSIGNED_ENG, TOP_UP));
        platform.put.mockImplementation(async () => v2Error(404, "budgetSubject not found: u-ada"));
        const { onOpenChange } = renderDialog();
        await waitFor(() => expect(amountInput()).toHaveValue("100.00"));
        await enterAmount("");
        await userEvent.click(save());
        expect(await screen.findByText(/not found/)).toBeInTheDocument();
        expect(onOpenChange).not.toHaveBeenCalledWith(false);
    });

    describe("following versus pinning the default", () => {
        const FOLLOWS = "The user follows the default budget.";
        const PINNED = "The user is pinned to this budget.";
        const INFO = /^When no budget is assigned, a user follows the default budget\./;

        test.each([
            { state: PINNED_STD, label: "the default" },
            { state: ASSIGNED_ENG, label: "another budget" },
        ])("the help line says the user is pinned when $label is selected", async ({ state }) => {
            serve(state);
            renderDialog();
            expect(await screen.findByText(PINNED)).toBeInTheDocument();
            expect(screen.queryByText(FOLLOWS)).toBeNull();
        });

        test("the help line follows the selection", async () => {
            serve(ASSIGNED_ENG);
            renderDialog();
            await waitFor(() => expect(revertToDefault()).toBeEnabled());
            await userEvent.click(revertToDefault());
            expect(screen.getByText(FOLLOWS)).toBeInTheDocument();
            expect(screen.getByRole("combobox", { name: "Budget" })).toHaveAccessibleDescription(FOLLOWS);
            await choose("Standard");
            expect(screen.getByText(PINNED)).toBeInTheDocument();
        });

        test("the info banner explains both, until dismissed for good", async () => {
            serve(UNDER_DEFAULT);
            const first = renderDialog();
            expect(await screen.findByText(INFO)).toBeInTheDocument();
            await userEvent.click(screen.getByRole("button", { name: "Dismiss" }));
            expect(screen.queryByText(INFO)).toBeNull();
            expect(localStorage.getItem("budgets.manageBudgetDefaultInfoDismissed")).toBe("true");
            expect(first.onOpenChange).not.toHaveBeenCalledWith(false);
        });

        test("the info banner stays dismissed when the dialog opens again", async () => {
            localStorage.setItem("budgets.manageBudgetDefaultInfoDismissed", "true");
            serve(UNDER_DEFAULT);
            renderDialog();
            await screen.findByText(FOLLOWS);
            expect(screen.queryByText(INFO)).toBeNull();
        });
    });

    test("a failed load shows why and keeps Save disabled", async () => {
        platform.get.mockImplementation(async () => v2Error(503, "Service unavailable"));
        renderDialog();
        expect(await screen.findByText("Service unavailable")).toBeInTheDocument();
        expect(save()).toBeDisabled();
    });
});
