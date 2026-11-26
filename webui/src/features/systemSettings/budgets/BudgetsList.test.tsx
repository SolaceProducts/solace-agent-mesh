// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { fullBudgetPermissions, mockBudgetRows } from "@/testing/data";

import { BudgetsList } from "./BudgetsList";

function renderList(overrides: Partial<Parameters<typeof BudgetsList>[0]> = {}) {
    const props = {
        budgets: mockBudgetRows,
        permissions: fullBudgetPermissions,
        onEdit: vi.fn(),
        onSetDefault: vi.fn(),
        onDelete: vi.fn().mockResolvedValue(undefined),
        onManageAssignments: vi.fn(),
        ...overrides,
    };
    render(<BudgetsList {...props} />);
    return props;
}

function rowFor(name: string) {
    const row = screen.getByText(name).closest("li");
    if (!row) throw new Error(`Row for "${name}" not found`);
    return within(row);
}

describe("BudgetsList", () => {
    test("shows each budget's limit and alert threshold, or No alert without one", () => {
        renderList();
        expect(rowFor("Engineering").getByText("$2,000.00")).toBeInTheDocument();
        expect(rowFor("Engineering").getByText("Alert at 90%")).toBeInTheDocument();
        expect(rowFor("Executive Exemption").getByText("Unlimited")).toBeInTheDocument();
        expect(rowFor("Executive Exemption").getByText("No alert")).toBeInTheDocument();
    });

    test("shows the Default badge only on the default budget", () => {
        renderList();
        expect(screen.getAllByText("Default")).toHaveLength(1);
        expect(rowFor("Standard Monthly").getByText("Default")).toBeInTheDocument();
    });

    test("a budget's name opens its details, as View Details does, and is plain text where there are none to view", () => {
        const onViewUsage = vi.fn();
        const { unmount } = render(<BudgetsList budgets={mockBudgetRows} permissions={fullBudgetPermissions} onViewUsage={onViewUsage} onEdit={vi.fn()} onSetDefault={vi.fn()} onDelete={vi.fn()} onManageAssignments={vi.fn()} />);
        fireEvent.click(screen.getByRole("button", { name: "Research Pilot" }));
        expect(onViewUsage).toHaveBeenCalledWith(mockBudgetRows.find(b => b.name === "Research Pilot"));
        unmount();
        renderList();
        expect(screen.queryByRole("button", { name: "Research Pilot" })).not.toBeInTheDocument();
    });
});
