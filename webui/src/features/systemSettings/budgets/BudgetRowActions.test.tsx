// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { fullBudgetPermissions, mockBudgetRows, noBudgetPermissions } from "@/testing/data";

import { BudgetRowActions } from "./BudgetRowActions";
import type { BudgetPermissions, BudgetRow } from "./types";

const defaultBudget = mockBudgetRows.find(b => b.isDefault)!;
const regularBudget = mockBudgetRows.find(b => !b.isDefault && b.assignedCount === 0)!;
const assignedBudget = mockBudgetRows.find(b => !b.isDefault && b.assignedCount > 0)!;

function renderActions(budget: BudgetRow, permissions: BudgetPermissions = fullBudgetPermissions, overrides: Partial<Parameters<typeof BudgetRowActions>[0]> = {}) {
    const props = {
        budget,
        permissions,
        onEdit: vi.fn(),
        onSetDefault: vi.fn(),
        onDelete: vi.fn().mockResolvedValue(undefined),
        ...overrides,
    };
    const result = render(<BudgetRowActions {...props} />);
    return { ...result, props };
}

function openMenu() {
    fireEvent.click(screen.getByRole("button", { name: "More" }));
}

function clickItem(label: string) {
    fireEvent.click(screen.getByText(label).closest('[role="menuitem"]')!);
}

describe("BudgetRowActions — menu contents", () => {
    test("canAssign on the default budget shows Set as Default disabled, since a default is replaced by promoting another budget and never cleared", () => {
        const { props } = renderActions(defaultBudget, { ...noBudgetPermissions, canAssign: true });
        openMenu();
        const setDefault = screen.getByText("Set as Default").closest('[role="menuitem"]')!;
        expect(setDefault).toHaveAttribute("aria-disabled", "true");
        fireEvent.click(setDefault);
        expect(props.onSetDefault).not.toHaveBeenCalled();
        expect(screen.queryByText("Remove as Default")).not.toBeInTheDocument();
    });
});

describe("BudgetRowActions — delete", () => {
    test("deleting the default budget shows the block dialog and never calls onDelete", () => {
        const { props } = renderActions(defaultBudget);
        openMenu();
        clickItem("Delete");
        expect(screen.getByText("Cannot Delete Default Budget")).toBeInTheDocument();
        expect(screen.getByText(/because it's the default budget/)).toBeInTheDocument();
        expect(screen.queryByText(/will be permanently deleted/)).not.toBeInTheDocument();
        expect(props.onDelete).not.toHaveBeenCalled();
    });

    test("deleting a budget with assigned users confirms with the revert banner, then calls onDelete", async () => {
        const { props } = renderActions(assignedBudget);
        openMenu();
        clickItem("Delete");
        expect(screen.getByText(/will be permanently deleted/)).toHaveTextContent(assignedBudget.name);
        expect(screen.getByText("Users assigned to this budget revert to the default user budget.")).toBeInTheDocument();
        await act(async () => fireEvent.click(screen.getByTestId("dialogConfirmButton")));
        expect(props.onDelete).toHaveBeenCalledWith(assignedBudget);
    });

    test("a budget with only unlisted assignments shows the same banner", () => {
        renderActions({ ...regularBudget, hasUnlistedAssignments: true });
        openMenu();
        clickItem("Delete");
        expect(screen.getByText("Users assigned to this budget revert to the default user budget.")).toBeInTheDocument();
    });
});
