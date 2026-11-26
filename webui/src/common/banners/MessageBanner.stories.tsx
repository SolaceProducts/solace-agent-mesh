// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import { MessageBanner } from "./MessageBanner";

const meta = {
    title: "Common/MessageBanner",
    component: MessageBanner,
    decorators: [
        Story => (
            <div className="max-w-2xl">
                <Story />
            </div>
        ),
    ],
    parameters: {
        layout: "padded",
        docs: {
            description: { component: "Banner that displays messages with optional dismiss action" },
        },
    },
} satisfies Meta<typeof MessageBanner>;

export default meta;
type Story = StoryObj<typeof meta>;

const VARIANTS = ["error", "warning", "info", "success"] as const;

export const AllVariants: Story = {
    args: { message: "ignored", onDismiss: fn() },
    render: args => (
        <div className="flex flex-col gap-4">
            {VARIANTS.map(variant => (
                <MessageBanner key={variant} variant={variant} message={`${variant} banner message`} />
            ))}
            <MessageBanner variant="info" dismissible message="dismissible banner message" onDismiss={args.onDismiss} />
        </div>
    ),
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);

        for (const variant of VARIANTS) {
            expect(canvas.getByText(`${variant} banner message`)).toBeInTheDocument();
        }

        await userEvent.click(canvas.getByRole("button"));
        expect(args.onDismiss).toHaveBeenCalled();
    },
};
