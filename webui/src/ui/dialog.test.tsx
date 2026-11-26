// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, test, vi } from "vitest";

import { Dialog, DialogContent, DialogTitle } from "./dialog";

function renderDialog(onOpenChange: (open: boolean) => void, contentProps: Partial<React.ComponentProps<typeof DialogContent>> = {}) {
    render(
        <Dialog open onOpenChange={onOpenChange}>
            <DialogContent {...contentProps}>
                <DialogTitle>Decide something</DialogTitle>
            </DialogContent>
        </Dialog>
    );
}

function overlay() {
    const node = document.querySelector('[data-slot="dialog-overlay"]');
    expect(node).not.toBeNull();
    return node!;
}

describe("DialogContent dismissal defaults", () => {
    test("renders no close affordance unless asked", () => {
        renderDialog(vi.fn());

        expect(screen.queryByRole("button", { name: /close/i })).not.toBeInTheDocument();
    });

    test("renders a close affordance on request", () => {
        renderDialog(vi.fn(), { showCloseButton: true });

        expect(screen.getByRole("button", { name: /close/i })).toBeInTheDocument();
    });

    test("does not close when the backdrop is clicked", async () => {
        const onOpenChange = vi.fn();
        renderDialog(onOpenChange);

        await userEvent.click(overlay());

        expect(onOpenChange).not.toHaveBeenCalled();
    });

    test("closes on Escape by default", async () => {
        const onOpenChange = vi.fn();
        renderDialog(onOpenChange);

        await userEvent.keyboard("{Escape}");

        expect(onOpenChange).toHaveBeenCalledWith(false);
    });

    test("honours a caller that blocks Escape", async () => {
        const onOpenChange = vi.fn();
        renderDialog(onOpenChange, { onEscapeKeyDown: e => e.preventDefault() });

        await userEvent.keyboard("{Escape}");

        expect(onOpenChange).not.toHaveBeenCalled();
    });
});
