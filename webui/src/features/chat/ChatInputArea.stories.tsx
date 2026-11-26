// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { ChatInputArea } from "@/features/chat/ChatInputArea";

import { getEmptyResponse, withDefaults } from "@/testing/helpers";
import type { AgentCardInfo } from "@/types";

const LIVE_AGENT = { name: "live-agent", displayName: "Live Agent" } as AgentCardInfo;

const handlers = withDefaults(
    getEmptyResponse("*/api/v1/prompts/groups/all"),
    http.get("*/api/v1/share/link/:sessionId", () => new HttpResponse(null, { status: 404 })),
    http.get("*/api/v1/sessions/:sessionId/contextUsage", () =>
        HttpResponse.json({
            sessionId: "mock-session-id",
            currentContextTokens: 0,
            promptTokens: 0,
            completionTokens: 0,
            cachedTokens: 0,
            maxInputTokens: null,
            usagePercentage: 0,
            model: "mock",
            totalEvents: 0,
            totalMessages: 0,
            totalTasks: 0,
            hasCompaction: false,
        })
    )
);

const meta = {
    title: "Chat/ChatInputArea",
    component: ChatInputArea,
    parameters: {
        layout: "padded",
        msw: { handlers },
    },
} satisfies Meta<typeof ChatInputArea>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: { agents: [LIVE_AGENT] },
    parameters: {
        chatContext: { sessionId: "mock-session-id", messages: [], selectedAgentName: "live-agent" },
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);
        const input = await canvas.findByTestId("chat-input");
        expect(input).toHaveAttribute("contenteditable", "true");
    },
};

export const AgentUnavailable: Story = {
    args: { agents: [LIVE_AGENT] },
    parameters: {
        chatContext: { sessionId: "mock-session-id", messages: [], selectedAgentName: "deleted-agent" },
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText(/Select an agent to continue the conversation/);
        const input = await canvas.findByTestId("chat-input");
        expect(input).toHaveAttribute("contenteditable", "false");
        expect(input).toHaveClass("min-h-12");
    },
};
