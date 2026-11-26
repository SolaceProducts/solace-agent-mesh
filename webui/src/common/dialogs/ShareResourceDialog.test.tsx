// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { ShareResourceDialog, type ShareResourceEntry } from "./ShareResourceDialog";

const existingShares: ShareResourceEntry[] = [
    { id: "s-1", userEmail: "viewer@example.com", accessLevel: "resource_viewer" },
    { id: "s-2", userEmail: "editor@example.com", accessLevel: "resource_editor" },
];

const accessLevels = [
    { value: "resource_viewer", label: "Viewer" },
    { value: "resource_editor", label: "Editor" },
];

function renderDialog(overrides: Partial<React.ComponentProps<typeof ShareResourceDialog>> = {}) {
    const props: React.ComponentProps<typeof ShareResourceDialog> = {
        isOpen: true,
        onClose: vi.fn(),
        resourceLabel: "Prompt",
        resourceName: "My Template",
        shares: existingShares,
        isLoading: false,
        identityServiceType: null,
        accessLevels,
        onCreate: vi.fn().mockResolvedValue(undefined),
        onReplace: vi.fn().mockResolvedValue(undefined),
        ...overrides,
    };
    render(<ShareResourceDialog {...props} />);
    return props;
}

describe("ShareResourceDialog", () => {
    test("renders the resource label and name in the header", () => {
        renderDialog();
        expect(screen.getByRole("heading", { name: /share prompt/i })).toBeInTheDocument();
        expect(screen.getByText(/my template/i)).toBeInTheDocument();
    });

    test("shows a spinner while loading shares", () => {
        renderDialog({ isLoading: true, shares: [] });
        expect(screen.getByRole("status")).toBeInTheDocument();
    });

    test("renders an empty-state message when there are no shares", () => {
        renderDialog({ shares: [] });
        expect(screen.getByText(/no users have access/i)).toBeInTheDocument();
    });

    test("lists each existing share with its email", () => {
        renderDialog();
        expect(screen.getByText("viewer@example.com")).toBeInTheDocument();
        expect(screen.getByText("editor@example.com")).toBeInTheDocument();
    });

    test("Save is disabled until the user makes a change", () => {
        renderDialog();
        expect(screen.getByRole("button", { name: /^save$/i })).toBeDisabled();
    });

    test("clicking Add opens a new row for a pending share", async () => {
        const user = userEvent.setup();
        renderDialog();
        await user.click(screen.getByRole("button", { name: /^add$/i }));
        expect(screen.getByPlaceholderText(/enter email/i)).toBeInTheDocument();
    });

    test("Save calls onCreate when only new shares are added (Enter commits the email)", async () => {
        const user = userEvent.setup();
        const props = renderDialog();
        await user.click(screen.getByRole("button", { name: /^add$/i }));
        const input = screen.getByPlaceholderText(/enter email/i);
        await user.type(input, "new@example.com{Enter}");
        const saveBtn = screen.getByRole("button", { name: /^save$/i });
        expect(saveBtn).not.toBeDisabled();
        await user.click(saveBtn);

        expect(props.onCreate).toHaveBeenCalledWith([{ userEmail: "new@example.com", accessLevel: "resource_viewer" }]);
        expect(props.onReplace).not.toHaveBeenCalled();
    });

    test("Save calls onCreate with the access level picked on the add row", async () => {
        const user = userEvent.setup();
        const props = renderDialog();
        await user.click(screen.getByRole("button", { name: /^add$/i }));
        const input = screen.getByPlaceholderText(/enter email/i);
        await user.type(input, "new@example.com{Enter}");

        const addRow = screen.getByTestId("pending-share-row");
        await user.click(within(addRow).getByRole("combobox"));
        await user.click(await screen.findByRole("option", { name: /editor/i }));

        await user.click(screen.getByRole("button", { name: /^save$/i }));

        expect(props.onCreate).toHaveBeenCalledWith([{ userEmail: "new@example.com", accessLevel: "resource_editor" }]);
    });

    test("multiple emails added to one row map to multiple shares with the same access level", async () => {
        const user = userEvent.setup();
        const props = renderDialog();
        await user.click(screen.getByRole("button", { name: /^add$/i }));
        const input = screen.getByPlaceholderText(/enter email/i);
        await user.type(input, "a@example.com{Enter}");
        await user.type(input, "b@example.com{Enter}");
        await user.click(screen.getByRole("button", { name: /^save$/i }));

        expect(props.onCreate).toHaveBeenCalledWith([
            { userEmail: "a@example.com", accessLevel: "resource_viewer" },
            { userEmail: "b@example.com", accessLevel: "resource_viewer" },
        ]);
    });

    test("Save calls onReplace when an existing share is removed", async () => {
        const user = userEvent.setup();
        const props = renderDialog();
        const viewerRow = screen.getByText("viewer@example.com").closest("[data-testid='share-row']");
        const removeBtn = viewerRow!.querySelector("[data-testid='remove-share']") as HTMLButtonElement;
        await user.click(removeBtn);
        await user.click(screen.getByRole("button", { name: /^save$/i }));

        expect(props.onReplace).toHaveBeenCalledWith([{ userEmail: "editor@example.com", accessLevel: "resource_editor" }]);
        expect(props.onCreate).not.toHaveBeenCalled();
    });

    test("Discard Changes closes the dialog", async () => {
        const user = userEvent.setup();
        const props = renderDialog();
        await user.click(screen.getByRole("button", { name: /discard/i }));
        expect(props.onClose).toHaveBeenCalled();
    });

    test("Save calls onReplace and onSuccess.updated when an existing share's level is edited", async () => {
        const user = userEvent.setup();
        const onSuccess = vi.fn();
        const props = renderDialog({ onSuccess });

        const viewerRow = screen.getByText("viewer@example.com").closest("[data-testid='share-row']") as HTMLElement;
        const viewerTrigger = within(viewerRow).getByRole("combobox");
        await user.click(viewerTrigger);

        const editorOption = await screen.findByRole("option", { name: /editor/i });
        await user.click(editorOption);

        await user.click(screen.getByRole("button", { name: /^save$/i }));

        expect(props.onReplace).toHaveBeenCalledWith([
            { userEmail: "viewer@example.com", accessLevel: "resource_editor" },
            { userEmail: "editor@example.com", accessLevel: "resource_editor" },
        ]);
        expect(props.onCreate).not.toHaveBeenCalled();
        expect(onSuccess).toHaveBeenCalledWith(
            expect.objectContaining({
                added: [],
                removed: [],
                updated: ["viewer@example.com"],
            })
        );
    });

    test("extraColumns before-access renders one cell per visible share with its header", () => {
        renderDialog({
            extraColumns: [
                {
                    key: "shared-on",
                    position: "before-access",
                    header: "Shared On",
                    render: (s: ShareResourceEntry) => <span data-testid={`meta-${s.id}`}>m</span>,
                },
            ],
        });
        expect(screen.getByText("Shared On")).toBeInTheDocument();
        expect(screen.getByTestId("meta-s-1")).toBeInTheDocument();
        expect(screen.getByTestId("meta-s-2")).toBeInTheDocument();
    });

    test("extraColumns after-access renders alongside the built-in remove button", () => {
        renderDialog({
            extraColumns: [
                {
                    key: "row-actions",
                    position: "after-access",
                    header: "",
                    render: (s: ShareResourceEntry) => <button data-testid={`acc-${s.id}`}>x</button>,
                },
            ],
        });
        const viewerRow = screen.getByText("viewer@example.com").closest("[data-testid='share-row']") as HTMLElement;
        expect(within(viewerRow).getByTestId("acc-s-1")).toBeInTheDocument();
        expect(within(viewerRow).getByTestId("remove-share")).toBeInTheDocument();
    });

    test("extraColumns preserves declared order on both sides of the access column", () => {
        renderDialog({
            extraColumns: [
                { key: "after-1", position: "after-access", header: "A1", render: () => <span data-testid="cell-a1" /> },
                { key: "before-1", position: "before-access", header: "B1", render: () => <span data-testid="cell-b1" /> },
                { key: "before-2", position: "before-access", header: "B2", render: () => <span data-testid="cell-b2" /> },
            ],
        });
        const row = screen.getByText("viewer@example.com").closest("[data-testid='share-row']") as HTMLElement;
        const cells = Array.from(row.children) as HTMLElement[];
        const indexOfTestId = (id: string) => cells.findIndex(c => c.querySelector(`[data-testid='${id}']`) || c.getAttribute("data-testid") === id);
        const iEmail = 0;
        const iB1 = indexOfTestId("cell-b1");
        const iB2 = indexOfTestId("cell-b2");
        const iA1 = indexOfTestId("cell-a1");
        const iRemove = indexOfTestId("remove-share");
        expect(iB1).toBeGreaterThan(iEmail);
        expect(iB2).toBeGreaterThan(iB1);
        expect(iA1).toBeGreaterThan(iB2);
        expect(iRemove).toBeGreaterThan(iA1);
    });

    test("extraColumns leaves the owner row with blank placeholders so columns align", () => {
        renderDialog({
            ownerEmail: "owner@example.com",
            extraColumns: [{ key: "shared-on", position: "before-access", header: "Shared On", render: () => <span data-testid="should-not-render" /> }],
        });
        const ownerRow = screen.getByTestId("owner-row");
        expect(within(ownerRow).queryByTestId("should-not-render")).not.toBeInTheDocument();
    });

    test("defaultOpenAddRow opens an empty add-row on mount", () => {
        renderDialog({ defaultOpenAddRow: true });
        expect(screen.getByPlaceholderText(/enter email/i)).toBeInTheDocument();
    });

    test("clicking Add focuses the freshly-mounted input on the new row", async () => {
        const user = userEvent.setup();
        renderDialog();
        await user.click(screen.getByRole("button", { name: /^add$/i }));
        const input = screen.getByPlaceholderText(/enter email/i);
        expect(document.activeElement).toBe(input);
    });

    test("extraHasChanges enables Save even when there are no user-share edits", () => {
        renderDialog({ extraHasChanges: true });
        expect(screen.getByRole("button", { name: /^save$/i })).not.toBeDisabled();
    });

    test("onSave fires on Save and runs before onClose so wrappers can commit out-of-band state", async () => {
        const user = userEvent.setup();
        const calls: string[] = [];
        const onClose = vi.fn(() => calls.push("close"));
        const onSave = vi.fn(() => {
            calls.push("save");
        });
        renderDialog({ extraHasChanges: true, onSave, onClose });
        await user.click(screen.getByRole("button", { name: /^save$/i }));
        expect(onSave).toHaveBeenCalledTimes(1);
        expect(onClose).toHaveBeenCalledTimes(1);
        expect(calls).toEqual(["save", "close"]);
    });
});
