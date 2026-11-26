// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, screen, userEvent } from "storybook/test";

import { BUDGET_ASSIGNMENTS_PAGE_SIZE, type BudgetAssignment, type BudgetSubject } from "@/api/budgets";

import { ManageAssignmentsDialog, type ManageAssignmentsDialogProps } from "./ManageAssignmentsDialog";

const ENGINEERING = { id: "budget-engineering", name: "Engineering", isDefault: false };
const STANDARD = { id: "budget-standard", name: "Standard Monthly", isDefault: true };
const WREN_NAME = "Wren Alexandra Walsh-Montgomery-Fitzgerald-Ashworth-Pemberton-Kingsley";
const WREN_EMAIL = "wren.walsh-montgomery.research-and-development.platform-engineering.monthly-allowance@a-very-long-subdomain.engineering.corp.example.com";
const person = (n: number) => ({ id: `user-${n}`, name: `User ${String(n).padStart(2, "0")}`, email: `user${n}@example.com` });

const assignmentOf = (n: number, budget = ENGINEERING): BudgetAssignment => ({
    id: `assignment-${n}`,
    type: "budgetAssignment",
    state: "concrete",
    subject: { type: "user", id: person(n).id, displayName: person(n).name, user: { email: person(n).email, isSystemUser: false } },
    budget: { id: budget.id, name: budget.name },
    createdBy: "amy@example.com",
    createdTime: "2026-10-01T00:00:00.000Z",
    updatedBy: "amy@example.com",
    updatedTime: "2026-10-01T00:00:00.000Z",
});

const assignmentsOf = (count: number, budget = ENGINEERING) => Array.from({ length: count }, (_, i) => assignmentOf(i + 1, budget));

const CANDIDATES: BudgetSubject[] = [
    {
        id: "user-1",
        type: "budgetSubject",
        subjectType: "user",
        displayName: "User 01",
        user: { email: "user1@example.com", isSystemUser: false },
        budget: { id: ENGINEERING.id, name: ENGINEERING.name, amount: "2000.00", source: "assigned", assignmentId: "assignment-1" },
        topUp: null,
        effectiveAmount: "2000.00",
    },
    {
        id: "user-ola",
        type: "budgetSubject",
        subjectType: "user",
        displayName: "Ola Olsen",
        user: { email: "ola@example.com", isSystemUser: false },
        budget: { id: "budget-ops", name: "Operations", amount: "500.00", source: "assigned", assignmentId: "assignment-ola" },
        topUp: null,
        effectiveAmount: "500.00",
    },
    {
        id: "user-wren",
        type: "budgetSubject",
        subjectType: "user",
        displayName: WREN_NAME,
        user: { email: WREN_EMAIL, isSystemUser: false },
        budget: { id: "budget-research", name: "Research and Development Platform Engineering Monthly Allowance", amount: "750.00", source: "assigned", assignmentId: "assignment-wren" },
        topUp: null,
        effectiveAmount: "750.00",
    },
    {
        id: "user-dee",
        type: "budgetSubject",
        subjectType: "user",
        displayName: "Dee Dawson",
        user: { email: "dee@example.com", isSystemUser: false },
        budget: { id: STANDARD.id, name: STANDARD.name, amount: "200.00", source: "default", assignmentId: null },
        topUp: null,
        effectiveAmount: "200.00",
    },
    {
        id: "user-eval",
        type: "budgetSubject",
        subjectType: "user",
        displayName: "Eval",
        user: { email: null, isSystemUser: true },
        budget: { id: STANDARD.id, name: STANDARD.name, amount: "200.00", source: "default", assignmentId: null },
        topUp: null,
        effectiveAmount: "200.00",
    },
];

interface StoryArgs extends Pick<ManageAssignmentsDialogProps, "budget" | "canEdit" | "onSave" | "onOpenChange"> {
    assignments: BudgetAssignment[];
}

const matches = (search: string, ...fields: (string | null)[]) => fields.some(field => (field ?? "").toLowerCase().includes(search.trim().toLowerCase()));

function InteractiveDialog({ assignments, budget, canEdit, onSave, onOpenChange }: Readonly<StoryArgs>) {
    const [addSearch, setAddSearch] = useState("");
    const [pages, setPages] = useState(1);
    return (
        <ManageAssignmentsDialog
            open
            onOpenChange={onOpenChange}
            budget={budget}
            canEdit={canEdit}
            onSave={onSave}
            assigned={{
                items: assignments.slice(0, pages * BUDGET_ASSIGNMENTS_PAGE_SIZE),
                total: assignments.length,
                isLoading: false,
                error: null,
                hasMore: assignments.length > pages * BUDGET_ASSIGNMENTS_PAGE_SIZE,
                isLoadingMore: false,
                onLoadMore: () => setPages(p => p + 1),
            }}
            candidates={{ items: addSearch.trim() ? CANDIDATES.filter(c => matches(addSearch, c.displayName, c.user.email)) : [], isLoading: false, error: null, search: addSearch, onSearchChange: setAddSearch }}
        />
    );
}

const meta = {
    title: "SystemSettings/ManageAssignmentsDialog",
    component: InteractiveDialog,
    parameters: { docs: { description: { component: "One budget's explicit assignments: add users through add boxes, mark assigned users for removal, and save it all together." } } },
    args: { assignments: assignmentsOf(3), budget: ENGINEERING, canEdit: true, onSave: fn(async () => {}), onOpenChange: fn() },
} satisfies Meta<typeof InteractiveDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const PopulatedWithPaging: Story = {
    args: { assignments: assignmentsOf(100) },
    play: async () => {
        expect(await screen.findByRole("heading", { name: "Assigned Users (100)" })).toBeInTheDocument();
    },
};

export const Searching: Story = {
    play: async () => {
        await userEvent.type(await screen.findByRole("combobox", { name: "User to add" }), "e");
        expect(await screen.findByText("ola@example.com")).toBeInTheDocument();
        const longOption = screen.getByText(WREN_EMAIL).closest("button")!;
        expect(longOption.scrollWidth).toBeLessThanOrEqual(longOption.clientWidth);
        expect(screen.getByText("Current").closest("button")).toHaveAttribute("aria-disabled", "true");
        expect(screen.getByText("Eval")).toBeInTheDocument();
    },
};
