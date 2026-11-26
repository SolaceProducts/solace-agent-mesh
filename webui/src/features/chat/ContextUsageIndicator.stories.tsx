// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { Ban } from "lucide-react";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import { ContextUsageIndicator } from "@/features/chat/ContextUsageIndicator";

import { withDefaults } from "@/testing/helpers";
import { Button } from "@/ui";

const SESSION_ID = "mock-session-id";
const MAX_INPUT_TOKENS = 200_000;

function contextUsageHandlers(percentage: number, onServed?: () => void) {
    return withDefaults(
        http.get("*/api/v1/sessions/:sessionId/contextUsage", () => {
            onServed?.();
            return HttpResponse.json({
                sessionId: SESSION_ID,
                currentContextTokens: (MAX_INPUT_TOKENS * percentage) / 100,
                promptTokens: 412_000,
                completionTokens: 18_400,
                cachedTokens: 0,
                maxInputTokens: MAX_INPUT_TOKENS,
                usagePercentage: percentage,
                model: "openai/vertex-claude-4-5-sonnet",
                totalEvents: 24,
                totalMessages: 12,
                totalTasks: 6,
                hasCompaction: false,
            });
        })
    );
}

let belowThresholdServed = false;

const meta = {
    title: "Chat/ContextUsageIndicator",
    component: ContextUsageIndicator,
    parameters: {
        docs: {
            description: {
                component: "Context-window usage ring shown in the chat input toolbar once usage reaches 50%. Hovering opens the usage details and compression panel.",
            },
        },
    },
    args: { sessionId: SESSION_ID, messageCount: 12 },
    decorators: [
        Story => (
            <div className="flex items-center justify-end gap-2 p-24">
                <Story />
                <Button className="gap-1.5" variant="outline">
                    <Ban className="size-4" />
                    <span>Stop</span>
                </Button>
            </div>
        ),
    ],
} satisfies Meta<typeof ContextUsageIndicator>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Moderate: Story = {
    parameters: { msw: { handlers: contextUsageHandlers(62) } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByRole("button", { name: "Context usage: 62%" });
    },
};

export const High: Story = {
    parameters: { msw: { handlers: contextUsageHandlers(84) } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByRole("button", { name: "Context usage: 84%" });
        await canvas.findByRole("button", { name: "Compress conversation" });
    },
};

export const NearLimit: Story = {
    parameters: { msw: { handlers: contextUsageHandlers(95) } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByRole("button", { name: "Context usage: 95%" });
    },
};

export const BelowThreshold: Story = {
    parameters: {
        msw: {
            handlers: contextUsageHandlers(30, () => {
                belowThresholdServed = true;
            }),
        },
    },
    beforeEach: () => {
        belowThresholdServed = false;
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await waitFor(() => expect(belowThresholdServed).toBe(true));
        await new Promise(resolve => setTimeout(resolve, 100));
        await expect(canvas.queryByRole("button", { name: /^Context usage:/ })).not.toBeInTheDocument();
    },
};

export const HoverOpensPanel: Story = {
    parameters: { msw: { handlers: contextUsageHandlers(84) } },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await userEvent.hover(await canvas.findByRole("button", { name: "Context usage: 84%" }));
        await expect(await screen.findByText("Context Window Usage")).toBeInTheDocument();
        await expect(screen.getByRole("button", { name: "Compress Conversation" })).toBeInTheDocument();
    },
};
