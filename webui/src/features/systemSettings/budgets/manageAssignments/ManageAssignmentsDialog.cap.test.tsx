// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, test, vi } from "vitest";

import type { BudgetAssignment } from "@/api/budgets";

vi.mock("./useUnsavedAssignmentChanges", async importOriginal => ({ ...(await importOriginal<typeof import("./useUnsavedAssignmentChanges")>()), MAX_ASSIGNMENT_CHANGES: 1 }));

import { ManageAssignmentsDialog } from "./ManageAssignmentsDialog";

const assignment = (id: string, name: string): BudgetAssignment => ({
    id: `a-${id}`,
    type: "budgetAssignment",
    state: "concrete",
    subject: { type: "user", id, displayName: name, user: { email: null, isSystemUser: false } },
    budget: { id: "b-eng", name: "Engineering" },
    createdBy: "x",
    createdTime: "2026-10-01T00:00:00.000Z",
    updatedBy: "x",
    updatedTime: "2026-10-01T00:00:00.000Z",
});

test("over the cap, Save is blocked with a message", async () => {
    render(
        <ManageAssignmentsDialog
            open
            onOpenChange={vi.fn()}
            budget={{ id: "b-eng", name: "Engineering", isDefault: false }}
            canEdit
            onSave={vi.fn()}
            assigned={{ items: [assignment("u-a", "Amy"), assignment("u-b", "Bea")], total: 2, isLoading: false, error: null, hasMore: false, isLoadingMore: false, onLoadMore: vi.fn() }}
            candidates={{ items: [], isLoading: false, error: null, search: "", onSearchChange: vi.fn() }}
        />
    );
    await userEvent.click(screen.getByRole("button", { name: "Remove Amy" }));
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
    await userEvent.click(screen.getByRole("button", { name: "Remove Bea" }));
    expect(screen.getByText("Save at most 1 changes at a time. You have 2.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
});
