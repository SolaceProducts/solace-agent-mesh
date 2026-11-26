// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, type Mock, test, vi } from "vitest";

import { DuplicateBudgetNameError } from "@/api/budgets";
import { ApiV2Error } from "@/api/common";

import { mockBudgetRows } from "@/testing/data";

import type { BudgetFormValues } from "./budgetForm.schema";
import { BudgetFormDialog } from "./BudgetFormDialog";
import type { BudgetRow, BudgetScope } from "./types";

const engineering = mockBudgetRows.find(b => b.id === "budget-engineering")!;
const unlimitedBudget = mockBudgetRows.find(b => b.amount === null)!;

type SubmitFn = (values: BudgetFormValues) => Promise<void>;

function duplicateName() {
    return new DuplicateBudgetNameError(new ApiV2Error(409, "A budget with this name already exists."));
}

function renderDialog({ budget, scope, onSubmit = vi.fn<SubmitFn>().mockResolvedValue(undefined) }: { budget?: BudgetRow | null; scope?: BudgetScope; onSubmit?: Mock<SubmitFn> } = {}) {
    const onOpenChange = vi.fn();
    render(<BudgetFormDialog open onOpenChange={onOpenChange} budget={budget} scope={scope} onSubmit={onSubmit} />);
    return { onSubmit, onOpenChange, user: userEvent.setup() };
}

const nameInput = () => screen.getByLabelText(/^Name/);
const amountInput = () => screen.getByLabelText(/^Limit \(USD\)/);
const warnInput = () => screen.getByLabelText("Alert Threshold");
const unlimitedCheckbox = () => screen.getByRole("checkbox", { name: "Unlimited" });

describe("BudgetFormDialog — create", () => {
    test("submits a limited budget with a threshold, then closes", async () => {
        const { onSubmit, onOpenChange, user } = renderDialog();
        await user.type(nameInput(), "Engineering");
        await user.type(amountInput(), "500.50");
        await user.type(warnInput(), "75");
        await user.click(screen.getByRole("button", { name: "Create" }));

        await waitFor(() => expect(onSubmit).toHaveBeenCalledWith({ name: "Engineering", scope: "user", amount: "500.50", warnPercentage: 75 }));
        expect(onOpenChange).toHaveBeenCalledWith(false);
    });

    test("Unlimited clears and disables the limit and the threshold", async () => {
        const { user } = renderDialog();
        await user.type(amountInput(), "500");
        await user.type(warnInput(), "75");
        await user.click(unlimitedCheckbox());

        expect(unlimitedCheckbox()).toBeChecked();
        expect(amountInput()).toBeDisabled();
        expect(amountInput()).toHaveValue("");
        expect(warnInput()).toBeDisabled();
        expect(warnInput()).toHaveValue("");
        expect(screen.getByText("Set a limit to add an alert threshold.")).toBeInTheDocument();
    });

    test("submits an unlimited budget as null amount and null threshold", async () => {
        const { onSubmit, user } = renderDialog();
        await user.type(nameInput(), "Executive Exemption");
        await user.click(unlimitedCheckbox());
        await user.click(screen.getByRole("button", { name: "Create" }));
        await waitFor(() => expect(onSubmit).toHaveBeenCalledWith({ name: "Executive Exemption", scope: "user", amount: null, warnPercentage: null }));
    });
});

describe("BudgetFormDialog — Agent Mesh", () => {
    test("edit keeps the existing name", async () => {
        const { onSubmit, user } = renderDialog({ budget: { ...engineering, id: "budget-mesh", name: "Deployment Cap", scope: "mesh" } });
        expect(screen.getByRole("heading", { name: "Edit Agent Mesh Budget" })).toBeInTheDocument();
        await user.click(screen.getByRole("button", { name: "Save" }));
        await waitFor(() => expect(onSubmit).toHaveBeenCalledWith({ name: "Deployment Cap", scope: "mesh", amount: "2000.00", warnPercentage: 90 }));
    });
});

describe("BudgetFormDialog — validation", () => {
    test("shows field errors and does not submit", async () => {
        const { onSubmit, user } = renderDialog();
        await user.type(nameInput(), "ab");
        await user.type(amountInput(), "500.123");
        await user.type(warnInput(), "150");
        await user.click(screen.getByRole("button", { name: "Create" }));

        expect(await screen.findByText("Use at least 3 characters")).toBeInTheDocument();
        expect(screen.getByText("Enter an amount such as 500 or 500.50")).toBeInTheDocument();
        expect(screen.getByText("Enter a whole number from 1 to 100")).toBeInTheDocument();
        expect(nameInput()).toHaveAttribute("aria-invalid", "true");
        expect(onSubmit).not.toHaveBeenCalled();
    });

    test("checking Unlimited clears a limit error", async () => {
        const { user } = renderDialog();
        await user.type(nameInput(), "Engineering");
        await user.click(screen.getByRole("button", { name: "Create" }));
        await screen.findByText("Enter a limit, or select Unlimited");
        await user.click(unlimitedCheckbox());
        expect(screen.queryByText("Enter a limit, or select Unlimited")).not.toBeInTheDocument();
    });
});

describe("BudgetFormDialog — edit", () => {
    test("prefills a limited budget", () => {
        renderDialog({ budget: engineering });
        expect(screen.getByRole("heading", { name: "Edit User Budget" })).toBeInTheDocument();
        expect(nameInput()).toHaveValue("Engineering");
        expect(amountInput()).toHaveValue("2000.00");
        expect(warnInput()).toHaveValue("90");
        expect(unlimitedCheckbox()).not.toBeChecked();
    });

    test("prefills an unlimited budget with the checkbox on", () => {
        renderDialog({ budget: unlimitedBudget });
        expect(unlimitedCheckbox()).toBeChecked();
        expect(amountInput()).toBeDisabled();
        expect(warnInput()).toBeDisabled();
    });
});

describe("BudgetFormDialog — submit failures and pending", () => {
    test("a duplicate-name rejection shows on the Name field and keeps the dialog open", async () => {
        const message = "A budget with this name already exists.";
        const onSubmit = vi.fn<SubmitFn>().mockRejectedValue(duplicateName());
        const { onOpenChange, user } = renderDialog({ onSubmit });
        await user.type(nameInput(), "engineering");
        await user.type(amountInput(), "500");
        await user.click(screen.getByRole("button", { name: "Create" }));

        expect(await screen.findByText(message)).toBeInTheDocument();
        expect(nameInput()).toHaveAttribute("aria-invalid", "true");
        expect(onOpenChange).not.toHaveBeenCalledWith(false);
    });

    test("in mesh mode, a duplicate-name 409 shows as a banner, since there is no Name field", async () => {
        const message = "A budget with this name already exists.";
        const onSubmit = vi.fn<SubmitFn>().mockRejectedValue(duplicateName());
        const { onOpenChange, user } = renderDialog({ scope: "mesh", onSubmit });
        await user.type(amountInput(), "5000");
        await user.click(screen.getByRole("button", { name: "Create" }));
        expect(await screen.findByText(message)).toBeInTheDocument();
        expect(onOpenChange).not.toHaveBeenCalledWith(false);
    });

    test("any other rejection shows a banner", async () => {
        const onSubmit = vi.fn<SubmitFn>().mockRejectedValue(new Error("The budgets service is unavailable."));
        const { user } = renderDialog({ onSubmit });
        await user.type(nameInput(), "Engineering");
        await user.type(amountInput(), "500");
        await user.click(screen.getByRole("button", { name: "Create" }));
        expect(await screen.findByText("The budgets service is unavailable.")).toBeInTheDocument();
        expect(nameInput()).not.toHaveAttribute("aria-invalid", "true");
    });

    test("buttons are disabled while submitting", async () => {
        let resolve!: () => void;
        const onSubmit = vi.fn<SubmitFn>(() => new Promise<void>(r => (resolve = r)));
        const { user } = renderDialog({ onSubmit });
        await user.type(nameInput(), "Engineering");
        await user.type(amountInput(), "500");
        await user.click(screen.getByRole("button", { name: "Create" }));

        await waitFor(() => expect(screen.getByRole("button", { name: "Create" })).toBeDisabled());
        expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
        await act(async () => resolve());
    });
});

describe("BudgetFormDialog — default user budget", () => {
    const systemUsersNote = /also applies to every service account with no assigned budget/;

    test("a limit on the default notes that it also limits system users, until Unlimited is checked", async () => {
        const { user } = renderDialog({ budget: { ...engineering, isDefault: true } });
        expect(screen.getByText(systemUsersNote)).toBeInTheDocument();

        await user.click(unlimitedCheckbox());
        expect(screen.queryByText(systemUsersNote)).not.toBeInTheDocument();
    });
});
