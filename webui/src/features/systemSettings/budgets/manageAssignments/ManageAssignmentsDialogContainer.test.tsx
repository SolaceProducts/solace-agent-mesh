// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { BudgetAssignment, BudgetSubject } from "@/api/budgets";

import { CAPABILITIES, type Capability } from "@/providers/contexts/UserContext";

import { paginatedResponse } from "@/testing/helpers/handlers";
import { platformFetch } from "@/testing/helpers/platformFetch";
import { v2Error } from "@/testing/helpers/platformResponses";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import type { BudgetRow } from "../types";

import { ManageAssignmentsDialogContainer } from "./ManageAssignmentsDialogContainer";

const platform = { get: vi.fn(), post: vi.fn(), patch: vi.fn(), put: vi.fn(), delete: vi.fn() };

const ENGINEERING: BudgetRow = { id: "b-eng", scope: "user", name: "Engineering", amount: "2000.00", warnPercentage: null, isDefault: false, assignedCount: 0, hasUnlistedAssignments: false };
const OPERATIONS: BudgetRow = { ...ENGINEERING, id: "b-ops", name: "Operations" };

const raj: BudgetAssignment = {
    id: "a-raj",
    type: "budgetAssignment",
    state: "concrete",
    subject: { type: "user", id: "u-raj", displayName: "Raj Patel", user: { email: null, isSystemUser: false } },
    budget: { id: "b-eng", name: "Engineering" },
    createdBy: "x",
    createdTime: "2026-10-01T00:00:00.000Z",
    updatedBy: "x",
    updatedTime: "2026-10-01T00:00:00.000Z",
};
const ola: BudgetSubject = {
    id: "u-ola",
    type: "budgetSubject",
    subjectType: "user",
    displayName: "Ola Olsen",
    user: { email: null, isSystemUser: false },
    budget: { id: "b-ops", name: "Operations", amount: "500.00", source: "assigned", assignmentId: "a-ola" },
    topUp: null,
    effectiveAmount: "500.00",
};

function renderContainer(budget: BudgetRow | null = ENGINEERING, capabilities: Capability[] = [CAPABILITIES.readBudgets, CAPABILITIES.assignBudgets]) {
    const onClose = vi.fn();
    const addNotification = vi.fn();
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    renderWithProviders(
        <QueryClientProvider client={client}>
            <ManageAssignmentsDialogContainer budget={budget} onClose={onClose} />
        </QueryClientProvider>,
        { userContextValues: { userHasCapability: cap => capabilities.includes(cap) }, chatContextValues: { addNotification } }
    );
    return { onClose, addNotification };
}

beforeEach(() => {
    vi.stubGlobal("fetch", platformFetch(platform));
    for (const fn of Object.values(platform)) fn.mockReset().mockResolvedValue(undefined);
    platform.get.mockImplementation(async (url: string) => (url.includes("/budgetSubjects") ? paginatedResponse([ola]) : paginatedResponse([raj], 20)));
});

afterEach(() => vi.unstubAllGlobals());

describe("ManageAssignmentsDialogContainer", () => {
    test("renders nothing and fetches nothing while closed", () => {
        renderContainer(null);
        expect(screen.queryByText("Manage Assignments")).not.toBeInTheDocument();
        expect(platform.get).not.toHaveBeenCalled();
    });

    test("searches users to add, and saves the changes with a summary toast", async () => {
        platform.put.mockResolvedValue({
            data: { added: [], moved: [{ assignmentId: "a-ola", subject: { id: "u-ola" }, fromBudget: { id: "b-ops", name: "Operations" } }], removed: [{ assignmentId: "a-raj", subject: { id: "u-raj" } }], unchanged: [] },
        });
        const { onClose, addNotification } = renderContainer();
        await screen.findByText("Raj Patel");

        await userEvent.click(screen.getByRole("combobox", { name: "User to add" }));
        fireEvent.change(screen.getByRole("combobox", { name: "User to add" }), { target: { value: " ola " } });
        await waitFor(() => expect(platform.get).toHaveBeenCalledWith("/api/v2/platform/budgetSubjects?subjectType=user&pageNumber=1&pageSize=10&search=ola"));
        await userEvent.click(await screen.findByText("Ola Olsen"));
        await userEvent.click(screen.getByRole("button", { name: "Remove Raj Patel" }));
        await userEvent.click(screen.getByRole("button", { name: "Save" }));

        await waitFor(() => expect(platform.put).toHaveBeenCalledWith("/api/v2/platform/budgets/b-eng/updateAssignments", { subjectType: "user", add: ["u-ola"], remove: ["u-raj"] }));
        await waitFor(() => expect(addNotification).toHaveBeenCalledWith("1 moved from Operations, 1 removed", "success"));
        expect(onClose).toHaveBeenCalled();
    });

    test("sends no user search until something is typed, spaces included", async () => {
        renderContainer();
        await screen.findByText("Raj Patel");
        const subjectSearches = () => platform.get.mock.calls.map(([url]) => String(url)).filter(url => url.includes("/budgetSubjects"));
        await userEvent.click(screen.getByRole("combobox", { name: "User to add" }));
        const box = screen.getByRole("combobox", { name: "User to add" });
        fireEvent.change(box, { target: { value: "   " } });
        fireEvent.change(box, { target: { value: "ola" } });
        await waitFor(() => expect(subjectSearches()).toContain("/api/v2/platform/budgetSubjects?subjectType=user&pageNumber=1&pageSize=10&search=ola"));
        expect(subjectSearches().every(url => url.includes("search="))).toBe(true);
    });

    test("after the search is cleared, a new search shows no users until its own results arrive", async () => {
        renderContainer();
        await screen.findByText("Raj Patel");
        await userEvent.click(screen.getByRole("combobox", { name: "User to add" }));
        const box = screen.getByRole("combobox", { name: "User to add" });
        fireEvent.change(box, { target: { value: "ola" } });
        expect(await screen.findByText("Ola Olsen")).toBeInTheDocument();
        fireEvent.change(box, { target: { value: "" } });
        await new Promise(resolve => setTimeout(resolve, 400));
        fireEvent.change(box, { target: { value: "d" } });
        expect(screen.queryByText("Ola Olsen")).not.toBeInTheDocument();
    });

    test("opening another budget shows none of the previous budget's users while its own load", async () => {
        platform.get.mockImplementation((url: string) => (url.includes("budgetId=b-ops") ? new Promise(() => {}) : Promise.resolve(paginatedResponse([raj], 20))));
        function Reopen() {
            const [budget, setBudget] = useState<BudgetRow | null>(ENGINEERING);
            return (
                <>
                    <button onClick={() => setBudget(OPERATIONS)}>Open Operations</button>
                    <ManageAssignmentsDialogContainer budget={budget} onClose={() => setBudget(null)} />
                </>
            );
        }
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        renderWithProviders(
            <QueryClientProvider client={client}>
                <Reopen />
            </QueryClientProvider>,
            { userContextValues: { userHasCapability: () => true } }
        );
        await screen.findByText("Raj Patel");
        await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
        fireEvent.click(screen.getByRole("button", { name: "Open Operations" }));

        await waitFor(() => expect(platform.get).toHaveBeenCalledWith(expect.stringContaining("budgetId=b-ops")));
        expect(screen.queryByText("Raj Patel")).not.toBeInTheDocument();
        expect(screen.queryByText("Assigned Users (20)")).not.toBeInTheDocument();
    });

    test("a failed user search reaches the add box", async () => {
        platform.get.mockImplementation(async (url: string) => {
            if (url.includes("/budgetSubjects")) return v2Error(503, "Service unavailable");
            return paginatedResponse([raj], 20);
        });
        renderContainer();
        await screen.findByText("Raj Patel");
        await userEvent.click(screen.getByRole("combobox", { name: "User to add" }));
        fireEvent.change(screen.getByRole("combobox", { name: "User to add" }), { target: { value: "ola" } });
        expect(await screen.findByText("Couldn't search users.")).toBeInTheDocument();
    });

    test("without permission to assign it is read-only", async () => {
        renderContainer(ENGINEERING, [CAPABILITIES.readBudgets]);
        await screen.findByText("Raj Patel");
        expect(screen.queryByRole("combobox", { name: "User to add" })).not.toBeInTheDocument();
    });
});
