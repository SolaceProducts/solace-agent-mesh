// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { AlreadySignedInHandoff, SignedInHandoff } from "./SignedInHandoff";

const meta = {
    title: "App/SignedInHandoff",
    component: SignedInHandoff,
    parameters: {
        layout: "fullscreen",
        docs: {
            description: {
                component: "Shown after a sign-in started from a chat app's link: confirms the sign-in and closes the tab when the browser allows it, otherwise asks the user to close it.",
            },
        },
    },
} satisfies Meta<typeof SignedInHandoff>;

export default meta;
type Story = StoryObj<typeof meta>;

export const FromWhatsApp: Story = {
    args: { sourceLabel: "WhatsApp", onContinue: () => {} },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByText("You're signed in")).toBeInTheDocument();
    },
};

export const AlreadySignedIn: Story = {
    args: { sourceLabel: "WhatsApp", onContinue: () => {} },
    render: () => <AlreadySignedInHandoff sourceLabel="WhatsApp" account="ada@example.com" onContinue={() => {}} onUseDifferentAccount={() => {}} />,
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await expect(canvas.getByRole("button", { name: "Use a different account" })).toBeInTheDocument();
        await expect(canvas.getByText(/You're signed in as ada@example.com/)).toBeInTheDocument();
    },
};
