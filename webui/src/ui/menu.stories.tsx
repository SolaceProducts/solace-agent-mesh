// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, screen, userEvent } from "storybook/test";

import { Menu } from "./menu";

const meta = {
    title: "UI/Menu",
    component: Menu,
    parameters: {
        layout: "padded",
        docs: { description: { component: "The menu component" } },
    },
} satisfies Meta<typeof Menu>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        actions: [
            { id: "action-1", label: "Name A-Z", onClick: fn() },
            { id: "action-2", label: "Name Z-A", onClick: fn() },
            { id: "action-3", label: "Date (Oldest first)", onClick: fn() },
            { id: "action-4", label: "Date (Newest first)", onClick: fn() },
        ],
    },
    play: async ({ args }) => {
        const item = await screen.findByText("Name A-Z");
        await userEvent.click(item);
        expect(args.actions![0].onClick).toHaveBeenCalled();
    },
};

export const DisabledOptions: Story = {
    args: {
        actions: [
            { id: "action-1", label: "Name A-Z", onClick: fn(), disabled: true },
            { id: "action-2", label: "Name Z-A", onClick: fn() },
            { id: "action-3", label: "Date (Oldest first)", onClick: fn() },
            { id: "action-4", label: "Date (Newest first)", onClick: fn() },
        ],
    },
    play: async () => {
        const disabledRow = (await screen.findByText("Name A-Z")).closest('[role="menuitem"]');
        const enabledRow = (await screen.findByText("Name Z-A")).closest('[role="menuitem"]');
        expect(disabledRow).toHaveAttribute("data-disabled", "true");
        expect(enabledRow?.getAttribute("data-disabled")).not.toBe("true");
    },
};
