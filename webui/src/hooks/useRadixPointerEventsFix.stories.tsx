// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor } from "storybook/test";

import { deferPanelClose, useRadixPointerEventsFix } from "./useRadixPointerEventsFix";

function StackedDialogsHost() {
    useRadixPointerEventsFix();
    const [outerOpen, setOuterOpen] = useState(true);
    const [innerOpen, setInnerOpen] = useState(false);

    function handleConfirm() {
        setInnerOpen(false);
        deferPanelClose(() => setOuterOpen(false));
    }

    return (
        <>
            <DialogPrimitive.Root open={outerOpen} onOpenChange={setOuterOpen}>
                <DialogPrimitive.Portal>
                    <DialogPrimitive.Overlay />
                    <DialogPrimitive.Content aria-describedby={undefined}>
                        <DialogPrimitive.Title>Outer Dialog</DialogPrimitive.Title>
                        <button onClick={() => setInnerOpen(true)}>Open Inner</button>
                    </DialogPrimitive.Content>
                </DialogPrimitive.Portal>
            </DialogPrimitive.Root>

            <DialogPrimitive.Root open={innerOpen} onOpenChange={setInnerOpen}>
                <DialogPrimitive.Portal>
                    <DialogPrimitive.Overlay />
                    <DialogPrimitive.Content aria-describedby={undefined}>
                        <DialogPrimitive.Title>Inner Dialog</DialogPrimitive.Title>
                        <button data-testid="confirm-btn" onClick={handleConfirm}>
                            Confirm
                        </button>
                    </DialogPrimitive.Content>
                </DialogPrimitive.Portal>
            </DialogPrimitive.Root>
        </>
    );
}

const meta = {
    title: "Hooks/useRadixPointerEventsFix",
    component: StackedDialogsHost,
    parameters: {
        layout: "centered",
        docs: {
            description: {
                component:
                    "Workaround for radix-ui/primitives #2122: clears `pointer-events: none` " +
                    "left stuck on body when two modal Radix Dialogs unmount in the same tick. " +
                    "This story opens stacked dialogs, closes them in the same commit via `deferPanelClose`, " +
                    "and asserts document.body.style.pointerEvents is cleared.",
            },
        },
    },
} satisfies Meta<typeof StackedDialogsHost>;

export default meta;
type Story = StoryObj<typeof meta>;

export const StackedDialogsCloseSequencing: Story = {
    play: async () => {
        const openInnerBtn = await screen.findByRole("button", { name: "Open Inner" });
        await userEvent.click(openInnerBtn);

        const bodyBeforeClose = document.body.style.pointerEvents;
        expect(bodyBeforeClose).toBe("none");

        const confirmBtn = await screen.findByTestId("confirm-btn");
        await userEvent.click(confirmBtn);

        await waitFor(() => {
            expect(document.body.style.pointerEvents).toBe("");
        });
    },
};
