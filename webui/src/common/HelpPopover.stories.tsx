// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import { HelpPopover } from "./HelpPopover";

const meta = {
    title: "Common/HelpPopover",
    component: HelpPopover,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Inline help affordance: hover-or-focus opens, mouseleave-or-blur closes. Wraps the Radix Popover in controlled state so keyboard users (Tab → trigger) get the same disclosure as mouse users.",
            },
        },
    },
    args: {
        content: "Watchlist tracks the experiments and reports for the agents you select. Charts show the agent's 3 most-used experiments × last-10 reports.",
    },
} satisfies Meta<typeof HelpPopover>;

export default meta;
type Story = StoryObj<typeof meta>;

export const HoverOpensAndCloses: Story = {
    play: async ({ canvasElement }) => {
        const trigger = await within(canvasElement).findByRole("button", { name: "Help" });

        await userEvent.hover(trigger);
        await waitFor(async () => {
            await expect(screen.getByText(/Watchlist tracks/i)).toBeInTheDocument();
        });

        await userEvent.unhover(trigger);
        await waitFor(async () => {
            await expect(screen.queryByText(/Watchlist tracks/i)).not.toBeInTheDocument();
        });
    },
};

export const TriggerIsKeyboardReachable: Story = {
    play: async ({ canvasElement }) => {
        const trigger = await within(canvasElement).findByRole("button", { name: "Help" });
        await expect(trigger.tagName).toBe("BUTTON");
        await expect(trigger.getAttribute("tabindex")).not.toBe("-1");
    },
};
