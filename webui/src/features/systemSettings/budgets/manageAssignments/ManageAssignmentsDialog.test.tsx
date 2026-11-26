// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import type { BudgetAssignment, BudgetSubject } from "@/api/budgets";

import { ManageAssignmentsDialog, type ManageAssignmentsDialogProps } from "./ManageAssignmentsDialog";

const ENGINEERING = { id: "b-eng", name: "Engineering", isDefault: false };
const STANDARD = { id: "b-std", name: "Standard", isDefault: true };

const assignment = (id: string, name: string | null, email: string | null = null, budget = ENGINEERING): BudgetAssignment => ({
    id: `a-${id}`,
    type: "budgetAssignment",
    state: "concrete",
    subject: { type: "user", id, displayName: name, user: { email, isSystemUser: false } },
    budget: { id: budget.id, name: budget.name },
    createdBy: "x",
    createdTime: "2026-10-01T00:00:00.000Z",
    updatedBy: "x",
    updatedTime: "2026-10-01T00:00:00.000Z",
});

const candidate = (id: string, name: string, budget: BudgetSubject["budget"]): BudgetSubject => ({
    id,
    type: "budgetSubject",
    subjectType: "user",
    displayName: name,
    user: { email: null, isSystemUser: false },
    budget,
    topUp: null,
    effectiveAmount: budget?.amount ?? null,
});

const onEngineering = (id: string): BudgetSubject["budget"] => ({ id: ENGINEERING.id, name: ENGINEERING.name, amount: "2000.00", source: "assigned", assignmentId: `a-${id}` });
const onOperations: BudgetSubject["budget"] = { id: "b-ops", name: "Operations", amount: "500.00", source: "assigned", assignmentId: "a-ops" };
const underDefault: BudgetSubject["budget"] = { id: STANDARD.id, name: STANDARD.name, amount: "200.00", source: "default", assignmentId: null };

function renderDialog(overrides: Partial<ManageAssignmentsDialogProps> = {}, assigned: Partial<ManageAssignmentsDialogProps["assigned"]> = {}, candidates: Partial<ManageAssignmentsDialogProps["candidates"]> = {}) {
    const props: ManageAssignmentsDialogProps = {
        open: true,
        onOpenChange: vi.fn(),
        budget: ENGINEERING,
        canEdit: true,
        onSave: vi.fn().mockResolvedValue(undefined),
        ...overrides,
        assigned: {
            items: [assignment("u-amy", "Amy Adams", "amy@example.com"), assignment("u-raj", "Raj Patel")],
            total: 2,
            isLoading: false,
            error: null,
            hasMore: false,
            isLoadingMore: false,
            onLoadMore: vi.fn(),
            ...assigned,
        },
        candidates: { items: [], isLoading: false, error: null, search: "", onSearchChange: vi.fn(), ...candidates },
    };
    const view = render(<ManageAssignmentsDialog {...props} />);
    return { ...props, rerender: (next: ManageAssignmentsDialogProps) => view.rerender(<ManageAssignmentsDialog {...next} />) };
}

const save = () => screen.getByRole("button", { name: "Save" });
const assignedList = () => within(screen.getByRole("list", { name: "Assigned users" }));
const addBox = () => screen.getByRole("combobox", { name: "User to add" });

async function searchFor(search: string) {
    await userEvent.click(addBox());
    fireEvent.change(addBox(), { target: { value: search } });
}

describe("ManageAssignmentsDialog", () => {
    test("shows the explicit assignments as chips under their total, the email as hidden text, and loads more", async () => {
        const props = renderDialog({}, { hasMore: true, total: 25 });
        expect(screen.getByRole("heading", { name: "Assigned Users (25)" })).toBeInTheDocument();
        expect(assignedList().getAllByRole("listitem")).toHaveLength(2);
        expect(assignedList().getByText("amy@example.com")).toHaveClass("sr-only");
        await userEvent.click(screen.getByRole("button", { name: "Load more" }));
        expect(props.assigned.onLoadMore).toHaveBeenCalled();
        expect(save()).toBeDisabled();
    });

    test("a pending assignment that reaches the list is not shown", () => {
        const waiting: BudgetAssignment = {
            ...assignment("unused", null),
            id: "a-waiting",
            state: "pending",
            subject: { type: "user", id: null, displayName: null },
        };
        renderDialog({}, { items: [waiting, assignment("u-raj", "Raj Patel")] });
        expect(assignedList().getByText("Raj Patel")).toBeInTheDocument();
        expect(assignedList().queryByText("new@example.com")).not.toBeInTheDocument();
        expect(assignedList().getAllByRole("listitem")).toHaveLength(1);
    });

    test("removing a user marks the chip To Be Removed and counts it in the heading, Undo restores it, and Save sends the removal", async () => {
        const props = renderDialog();
        await userEvent.click(screen.getByRole("button", { name: "Remove Raj Patel" }));
        expect(assignedList().getByText("To Be Removed")).toBeInTheDocument();
        expect(screen.getByRole("status")).toHaveTextContent("Unsaved changes: 1 to remove");
        expect(screen.getByRole("heading", { name: "Assigned Users (2)" })).toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: "Undo removing Raj Patel" }));
        expect(assignedList().queryByText("To Be Removed")).not.toBeInTheDocument();
        expect(screen.getByRole("status")).toBeEmptyDOMElement();
        expect(save()).toBeDisabled();

        await userEvent.click(screen.getByRole("button", { name: "Remove Raj Patel" }));
        await userEvent.click(save());
        await waitFor(() => expect(props.onSave).toHaveBeenCalledWith({ subjectType: "user", add: [], remove: ["u-raj"] }));
        expect(props.onOpenChange).toHaveBeenCalledWith(false);
    });

    test("the add search finds users; picking one adds a chip and clears the search, and Save sends the add", async () => {
        const props = renderDialog({}, {}, { items: [candidate("u-ola", "Ola Olsen", onOperations), candidate("u-dee", "Dee Dawson", underDefault)] });
        await searchFor("o");
        expect(props.candidates.onSearchChange).toHaveBeenLastCalledWith("o");
        expect(screen.getByRole("button", { name: "Ola Olsen" })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Dee Dawson" })).toBeInTheDocument();
        expect(screen.queryByText("Current")).not.toBeInTheDocument();

        await userEvent.click(screen.getByText("Ola Olsen"));
        expect(addBox()).toHaveValue("");
        expect(props.candidates.onSearchChange).toHaveBeenLastCalledWith("");
        expect(assignedList().getByText("Ola Olsen")).toBeInTheDocument();
        expect(assignedList().getByText("To Be Moved from Operations")).toBeInTheDocument();
        expect(screen.getByRole("status")).toHaveTextContent("Unsaved changes: 1 to move");

        await userEvent.click(save());
        await waitFor(() => expect(props.onSave).toHaveBeenCalledWith({ subjectType: "user", add: ["u-ola"], remove: [] }));
    });

    test("users already on this budget are offered disabled", async () => {
        const props = renderDialog({}, {}, { items: [candidate("u-raj", "Raj Patel", onEngineering("u-raj"))] });
        await searchFor("raj");
        const option = screen.getByText("Current").closest("button");
        expect(option).toHaveAttribute("aria-disabled", "true");
        await userEvent.click(option!);
        expect(save()).toBeDisabled();
        expect(props.onSave).not.toHaveBeenCalled();
    });

    test("picks accumulate, and a picked user is not offered again", async () => {
        const props = renderDialog({}, {}, { items: [candidate("u-ola", "Ola Olsen", onOperations), candidate("u-dee", "Dee Dawson", underDefault)] });
        await searchFor("ola");
        await userEvent.click(screen.getByText("Ola Olsen"));

        await searchFor("o");
        expect(screen.queryByRole("button", { name: "Ola Olsen" })).not.toBeInTheDocument();
        await userEvent.click(screen.getByText("Dee Dawson"));
        expect(assignedList().getByText("Ola Olsen")).toBeInTheDocument();
        expect(assignedList().getByText("Dee Dawson")).toBeInTheDocument();

        await userEvent.click(save());
        await waitFor(() => expect(props.onSave).toHaveBeenCalledWith({ subjectType: "user", add: ["u-ola", "u-dee"], remove: [] }));
    });

    test("the X on an added chip cancels the add, and the user is offered again", async () => {
        renderDialog({}, {}, { items: [candidate("u-ola", "Ola Olsen", onOperations)] });
        await searchFor("ola");
        await userEvent.click(screen.getByText("Ola Olsen"));
        expect(save()).toBeEnabled();

        await userEvent.click(screen.getByRole("button", { name: "Cancel adding Ola Olsen" }));
        expect(assignedList().queryByText("Ola Olsen")).not.toBeInTheDocument();
        expect(addBox()).toHaveFocus();
        expect(save()).toBeDisabled();
        await searchFor("ola");
        expect(screen.getByRole("button", { name: "Ola Olsen" })).toBeInTheDocument();
    });

    test("on the default budget, a note explains it and the X unpins", async () => {
        renderDialog({ budget: STANDARD }, { items: [assignment("u-raj", "Raj Patel", null, STANDARD)], total: 1 });
        expect(screen.getByText(/to users to limit their spending/)).toHaveTextContent("Assign Standard to users to limit their spending.");
        expect(screen.getByText(/is the default budget, so it applies to users without an assignment/)).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Remove Raj Patel" })).not.toBeInTheDocument();
        await userEvent.click(screen.getByRole("button", { name: "Unpin Raj Patel" }));
        expect(assignedList().getByText("To Be Unpinned")).toBeInTheDocument();
        expect(screen.getByRole("status")).toHaveTextContent("Unsaved changes: 1 to unpin");
    });

    test("without permission to assign it is read-only, and says View rather than Manage", () => {
        renderDialog({ canEdit: false });
        expect(screen.getByRole("heading", { name: "View Budget Assignments" })).toBeInTheDocument();
        expect(screen.getByText(/^Users assigned to/)).toHaveTextContent("Users assigned to Engineering.");
        expect(screen.getByRole("heading", { name: "Assigned Users (2)" })).toBeInTheDocument();
        expect(screen.queryByRole("combobox", { name: "User to add" })).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Remove Raj Patel" })).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Save" })).not.toBeInTheDocument();
        expect(screen.getAllByRole("button", { name: "Close" }).length).toBeGreaterThan(0);
    });

    test("a refused save shows why and keeps the changes", async () => {
        const props = renderDialog({ onSave: vi.fn().mockRejectedValue(new Error("1 subject has no users row")) });
        await userEvent.click(screen.getByRole("button", { name: "Remove Raj Patel" }));
        await userEvent.click(save());
        expect(await screen.findByText("1 subject has no users row")).toBeInTheDocument();
        expect(props.onOpenChange).not.toHaveBeenCalledWith(false);
        expect(screen.getByRole("button", { name: "Undo removing Raj Patel" })).toBeInTheDocument();
    });

    test("Cancel closes without asking, even with unsaved changes", async () => {
        const props = renderDialog();
        await userEvent.click(screen.getByRole("button", { name: "Remove Raj Patel" }));
        await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
        expect(props.onOpenChange).toHaveBeenCalledWith(false);
        expect(props.onSave).not.toHaveBeenCalled();
    });

    test("while saving, neither Cancel nor Escape closes the dialog", async () => {
        let finishSave: () => void = () => {};
        const props = renderDialog({ onSave: vi.fn(() => new Promise<void>(resolve => (finishSave = resolve))) });
        await userEvent.click(screen.getByRole("button", { name: "Remove Raj Patel" }));
        await userEvent.click(save());

        expect(screen.getByRole("button", { name: "Cancel" })).toBeDisabled();
        await userEvent.keyboard("{Escape}");
        expect(props.onOpenChange).not.toHaveBeenCalled();

        finishSave();
        await waitFor(() => expect(props.onOpenChange).toHaveBeenCalledWith(false));
        expect(screen.getByRole("button", { name: "Cancel" })).toBeEnabled();
    });

    test("reopening starts with no pending changes and an empty search", async () => {
        const props = renderDialog();
        await userEvent.click(screen.getByRole("button", { name: "Remove Raj Patel" }));
        await searchFor("ola");
        props.rerender({ ...props, open: false });
        props.rerender({ ...props, open: true });
        expect(screen.getByRole("button", { name: "Remove Raj Patel" })).toBeInTheDocument();
        expect(addBox()).toHaveValue("");
        expect(save()).toBeDisabled();
    });

    test("a failed load more keeps the chips and pending changes in view under the error", async () => {
        const { rerender, ...props } = renderDialog({}, { hasMore: true, total: 25 }, { items: [candidate("u-ola", "Ola Olsen", onOperations)] });
        await searchFor("ola");
        await userEvent.click(screen.getByText("Ola Olsen"));
        rerender({ ...props, assigned: { ...props.assigned, error: new Error("Service unavailable") } });
        expect(screen.getByText(/Couldn't load the assignments: Service unavailable/)).toBeInTheDocument();
        expect(assignedList().getByText("Ola Olsen")).toBeInTheDocument();
        expect(assignedList().getByText("Raj Patel")).toBeInTheDocument();
        expect(save()).toBeEnabled();
    });
});
