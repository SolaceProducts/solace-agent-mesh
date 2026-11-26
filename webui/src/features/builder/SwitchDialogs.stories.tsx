// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, screen, userEvent, within } from "storybook/test";

import { ApplyChangesDialog } from "@/features/builder/ApplyChangesDialog";
import { SwitchToBuildWhileTestingDialog } from "@/features/builder/SwitchToBuildWhileTestingDialog";
import { SwitchToTestModeDialog } from "@/features/builder/SwitchToTestModeDialog";

const meta = {
    title: "Builder/SwitchDialogs",
} satisfies Meta;

export default meta;

export const SwitchToTestMode: StoryObj<typeof SwitchToTestModeDialog> = {
    args: { open: true, onConfirm: fn(), onCancel: fn() },
    render: args => <SwitchToTestModeDialog {...args} />,
    play: async ({ args }) => {
        const dialog = await screen.findByRole("dialog");

        const confirmButton = within(dialog).getByRole("button", { name: "Switch to Test Mode" });
        await userEvent.click(confirmButton);
        expect(args.onConfirm).toHaveBeenCalled();
    },
};

export const SwitchToBuildWhileTesting: StoryObj<typeof SwitchToBuildWhileTestingDialog> = {
    args: { open: true, onConfirm: fn(), onCancel: fn() },
    render: args => <SwitchToBuildWhileTestingDialog {...args} />,
    play: async ({ args }) => {
        const dialog = await screen.findByRole("dialog");

        await within(dialog).findByRole("button", { name: "Switch to Build Mode" });
        await userEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
        expect(args.onCancel).toHaveBeenCalled();
        expect(args.onConfirm).not.toHaveBeenCalled();
    },
};

export const ApplyChanges: StoryObj<typeof ApplyChangesDialog> = {
    args: { open: true, onConfirm: fn(), onCancel: fn() },
    render: args => <ApplyChangesDialog {...args} />,
    play: async ({ args }) => {
        const dialog = await screen.findByRole("dialog");
        await within(dialog).findByRole("heading", { name: "Apply Changes" });
        const buttons = within(dialog).getAllByRole("button");
        expect(buttons.length).toBeGreaterThanOrEqual(2);
        expect(args.onConfirm).not.toHaveBeenCalled();
    },
};
