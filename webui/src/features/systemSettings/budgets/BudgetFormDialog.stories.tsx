// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, screen, within } from "storybook/test";

import { mockBudgetRows } from "@/testing/data";

import { BudgetFormDialog } from "./BudgetFormDialog";

const engineering = mockBudgetRows.find(b => b.id === "budget-engineering")!;

const meta = {
    title: "SystemSettings/BudgetFormDialog",
    component: BudgetFormDialog,
    parameters: {
        layout: "fullscreen",
        docs: { description: { component: "Create/edit budget dialog: name (user budgets only), a read-only scope and period, limit in USD (or Unlimited), and an optional alert threshold." } },
    },
    args: { open: true, onOpenChange: fn(), onSubmit: fn(async () => {}) },
} satisfies Meta<typeof BudgetFormDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Create: Story = {
    play: async () => {
        const dialog = within(await screen.findByRole("dialog"));
        expect(dialog.getByRole("heading", { name: "Create User Budget" })).toBeInTheDocument();
    },
};

export const Edit: Story = {
    args: { budget: engineering },
    play: async () => {
        const dialog = within(await screen.findByRole("dialog"));
        expect(dialog.getByRole("heading", { name: "Edit User Budget" })).toBeInTheDocument();
    },
};
