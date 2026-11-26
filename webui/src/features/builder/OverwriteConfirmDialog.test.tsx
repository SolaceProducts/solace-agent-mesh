// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";

import { OverwriteConfirmDialog } from "./OverwriteConfirmDialog";
import { shouldSkipOverwriteConfirm } from "./overwriteConfirmPreference";

const SKIP_KEY = "sam_builder_skip_overwrite_confirm";

describe("OverwriteConfirmDialog", () => {
    afterEach(() => localStorage.clear());

    const overwrites = [
        { name: "HaikuBot", kind: "agent", changedFields: ["systemPrompt", "skills"] },
        { name: "PetAPI", kind: "connector", changedFields: [] },
    ];

    test("names each component and the fields that will change", () => {
        render(<OverwriteConfirmDialog overwrites={overwrites} onConfirm={vi.fn()} onCancel={vi.fn()} />);

        expect(screen.getByText("HaikuBot")).toBeTruthy();
        expect(screen.getByText("Changes: systemPrompt, skills")).toBeTruthy();
        expect(screen.getByText("PetAPI")).toBeTruthy();
        expect(screen.getByText("No field changes reported")).toBeTruthy();
    });

    test("is closed when there is nothing to confirm", () => {
        render(<OverwriteConfirmDialog overwrites={null} onConfirm={vi.fn()} onCancel={vi.fn()} />);

        expect(screen.queryByText("HaikuBot")).toBeNull();
        expect(screen.queryByRole("dialog")).toBeNull();
    });

    test("cancelling does not confirm, and confirming does not cancel", () => {
        const onConfirm = vi.fn();
        const onCancel = vi.fn();
        const { unmount } = render(<OverwriteConfirmDialog overwrites={overwrites} onConfirm={onConfirm} onCancel={onCancel} />);

        fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
        expect(onCancel).toHaveBeenCalledTimes(1);
        expect(onConfirm).not.toHaveBeenCalled();
        unmount();

        const confirm = vi.fn();
        const cancel = vi.fn();
        render(<OverwriteConfirmDialog overwrites={overwrites} onConfirm={confirm} onCancel={cancel} />);
        fireEvent.click(screen.getByRole("button", { name: "Replace and save" }));
        expect(confirm).toHaveBeenCalledTimes(1);
        expect(cancel).not.toHaveBeenCalled();
    });

    test("remembers the opt-out only when it is ticked and the user confirms", () => {
        const { unmount } = render(<OverwriteConfirmDialog overwrites={overwrites} onConfirm={vi.fn()} onCancel={vi.fn()} />);
        fireEvent.click(screen.getByRole("button", { name: "Replace and save" }));
        expect(shouldSkipOverwriteConfirm()).toBe(false);
        unmount();

        const ticked = render(<OverwriteConfirmDialog overwrites={overwrites} onConfirm={vi.fn()} onCancel={vi.fn()} />);
        fireEvent.click(screen.getByRole("checkbox"));
        fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
        expect(shouldSkipOverwriteConfirm()).toBe(false);
        ticked.unmount();

        render(<OverwriteConfirmDialog overwrites={overwrites} onConfirm={vi.fn()} onCancel={vi.fn()} />);
        fireEvent.click(screen.getByRole("checkbox"));
        fireEvent.click(screen.getByRole("button", { name: "Replace and save" }));
        expect(localStorage.getItem(SKIP_KEY)).toBe("true");
        expect(shouldSkipOverwriteConfirm()).toBe(true);
    });

    test("asks again when storage is unavailable", () => {
        const original = Object.getOwnPropertyDescriptor(window, "localStorage")!;
        Object.defineProperty(window, "localStorage", {
            configurable: true,
            value: {
                getItem: () => {
                    throw new Error("denied");
                },
            },
        });
        try {
            expect(shouldSkipOverwriteConfirm()).toBe(false);
        } finally {
            Object.defineProperty(window, "localStorage", original);
        }
    });

    test("Escape cancels", () => {
        const onCancel = vi.fn();
        const onConfirm = vi.fn();
        render(<OverwriteConfirmDialog overwrites={overwrites} onConfirm={onConfirm} onCancel={onCancel} />);

        fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });

        expect(onCancel).toHaveBeenCalledTimes(1);
        expect(onConfirm).not.toHaveBeenCalled();
    });

    test("a box ticked before cancelling is not still ticked, or saved, by the next prompt", () => {
        const { rerender } = render(<OverwriteConfirmDialog overwrites={overwrites} onConfirm={vi.fn()} onCancel={vi.fn()} />);
        fireEvent.click(screen.getByRole("checkbox"));
        fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
        rerender(<OverwriteConfirmDialog overwrites={null} onConfirm={vi.fn()} onCancel={vi.fn()} />);

        rerender(<OverwriteConfirmDialog overwrites={overwrites} onConfirm={vi.fn()} onCancel={vi.fn()} />);

        expect((screen.getByRole("checkbox") as HTMLInputElement).checked).toBe(false);
        fireEvent.click(screen.getByRole("button", { name: "Replace and save" }));
        expect(shouldSkipOverwriteConfirm()).toBe(false);
    });

    test("Escape also clears a ticked box, so it is not carried into the next prompt", () => {
        const { rerender } = render(<OverwriteConfirmDialog overwrites={overwrites} onConfirm={vi.fn()} onCancel={vi.fn()} />);
        fireEvent.click(screen.getByRole("checkbox"));
        fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
        rerender(<OverwriteConfirmDialog overwrites={null} onConfirm={vi.fn()} onCancel={vi.fn()} />);

        rerender(<OverwriteConfirmDialog overwrites={overwrites} onConfirm={vi.fn()} onCancel={vi.fn()} />);

        expect((screen.getByRole("checkbox") as HTMLInputElement).checked).toBe(false);
    });
});
