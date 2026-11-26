// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { fullBudgetPermissions, mockBudgetRows, noBudgetPermissions } from "@/testing/data";

import { BudgetsPage, type BudgetsPageProps } from "./BudgetsPage";

function renderPage(overrides: Partial<BudgetsPageProps> = {}) {
    const props: BudgetsPageProps = {
        budgets: mockBudgetRows,
        totalCount: mockBudgetRows.length,
        page: 1,
        pageSize: 20,
        onPageChange: vi.fn(),
        nameFilter: "",
        onNameFilterChange: vi.fn(),
        hasActiveFilters: false,
        meshBudget: null,
        permissions: fullBudgetPermissions,
        createOpen: false,
        onCreateOpenChange: vi.fn(),
        onCreate: vi.fn().mockResolvedValue(undefined),
        onUpdate: vi.fn().mockResolvedValue(undefined),
        onSetDefault: vi.fn(),
        onDelete: vi.fn().mockResolvedValue(undefined),
        onManageAssignments: vi.fn(),
        ...overrides,
    };
    render(<BudgetsPage {...props} />);
    return props;
}

describe("BudgetsPage", () => {
    beforeEach(() => localStorage.clear());

    test.each([
        { permissions: fullBudgetPermissions, offered: true },
        { permissions: noBudgetPermissions, offered: false },
    ])("the empty state offers Create User Budget: $offered", ({ permissions, offered }) => {
        renderPage({ budgets: [], totalCount: 0, permissions });
        expect(screen.getByText("No Budgets Found")).toBeInTheDocument();
        expect(screen.queryAllByRole("button", { name: /Create User Budget/ })).toHaveLength(offered ? 1 : 0);
    });

    describe("without user budgets", () => {
        test("describes only the Agent Mesh budget in the learning banner", () => {
            renderPage({ userBudgetsSupported: false });
            expect(screen.getByText(/limit how much Agent Mesh can spend/)).toBeInTheDocument();
            expect(screen.queryByText(/each user/)).not.toBeInTheDocument();
        });
    });
});
