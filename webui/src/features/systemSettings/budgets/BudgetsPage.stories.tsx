// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, within } from "storybook/test";

import { fullBudgetPermissions, mockBudgetRows, mockMeshBudget } from "@/testing/data";

import { BudgetsPage } from "./BudgetsPage";

const meta = {
    title: "SystemSettings/BudgetsPage",
    component: BudgetsPage,
    parameters: {
        layout: "fullscreen",
        docs: { description: { component: "Budgets page body: learning banner, the Agent Mesh budget line, name filter, Create User Budget, and the user-budget table with its row actions." } },
    },
    decorators: [
        Story => (
            <div style={{ height: "100vh", width: "100vw", display: "flex", flexDirection: "column" }}>
                <Story />
            </div>
        ),
    ],
    args: {
        budgets: mockBudgetRows,
        totalCount: mockBudgetRows.length,
        page: 1,
        pageSize: 20,
        onPageChange: fn(),
        nameFilter: "",
        onNameFilterChange: fn(),
        hasActiveFilters: false,
        meshBudget: mockMeshBudget,
        permissions: fullBudgetPermissions,
        createOpen: false,
        onCreateOpenChange: fn(),
        onCreate: fn(async () => {}),
        onUpdate: fn(async () => {}),
        onSetDefault: fn(),
        onDelete: fn(async () => {}),
        onManageAssignments: fn(),
    },
} satisfies Meta<typeof BudgetsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Populated: Story = {
    play: async ({ canvasElement }) => {
        expect(await within(canvasElement).findByText("Engineering")).toBeInTheDocument();
    },
};

export const Desktop: Story = {
    args: { userBudgetsSupported: false },
    play: async ({ canvasElement }) => {
        expect(await within(canvasElement).findByText(/User budgets are available in production deployments/)).toBeInTheDocument();
    },
};
