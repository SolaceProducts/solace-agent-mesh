// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import { TeamsWebhookUrlSection } from "./TeamsWebhookUrlSection";

const meta = {
    title: "Entrypoints/TeamsWebhookUrlSection",
    component: TeamsWebhookUrlSection,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Reusable component for displaying a Teams Gateway webhook URL with one-click copy functionality.",
            },
        },
    },
    args: {
        onCopy: fn(),
    },
    decorators: [
        Story => (
            <div className="w-150">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof TeamsWebhookUrlSection>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        webhookUrl: "https://teams-gateway.example.test/api/messages",
    },
    play: async ({ canvasElement, args }) => {
        Object.defineProperty(navigator, "clipboard", {
            value: {
                writeText: fn(() => Promise.resolve()),
            },
            writable: true,
            configurable: true,
        });

        const canvas = within(canvasElement);

        expect(canvas.getByText("https://teams-gateway.example.test/api/messages")).toBeInTheDocument();

        const copyButton = canvas.getByRole("button", { name: /Copy URL/i });
        expect(copyButton).toBeInTheDocument();

        await userEvent.click(copyButton);

        expect(args.onCopy).toHaveBeenCalledTimes(1);

        expect(canvas.getByRole("button", { name: /Copied!/i })).toBeInTheDocument();
    },
};

export const LongUrl: Story = {
    args: {
        webhookUrl: "https://very-long-teams-gateway-name-for-testing-horizontal-overflow-and-scroll-behavior.example.test/api/messages",
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByText("https://very-long-teams-gateway-name-for-testing-horizontal-overflow-and-scroll-behavior.example.test/api/messages")).toBeInTheDocument();
    },
};

export const WithCustomClassName: Story = {
    args: {
        webhookUrl: "https://teams-gateway.example.test/api/messages",
        className: "mb-4",
    },
};

export const WithOnCopyCallback: Story = {
    args: {
        webhookUrl: "https://teams-gateway.example.test/api/messages",
        onCopy: fn(),
    },
    play: async ({ canvasElement, args }) => {
        Object.defineProperty(navigator, "clipboard", {
            value: {
                writeText: fn(() => Promise.resolve()),
            },
            writable: true,
            configurable: true,
        });

        const canvas = within(canvasElement);
        const copyButton = canvas.getByRole("button", { name: /Copy URL/i });

        await userEvent.click(copyButton);

        expect(args.onCopy).toHaveBeenCalledTimes(1);
        expect(canvas.getByRole("button", { name: /Copied!/i })).toBeInTheDocument();
    },
};
