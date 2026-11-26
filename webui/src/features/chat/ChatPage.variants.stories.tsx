// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { agentUIMode } from "@/providers/contexts";

import { ChatPage } from "@/features/chat/ChatPage";

import { mockMessages } from "@/testing/data";
import { getEmptyResponse, withDefaults } from "@/testing/helpers";

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

const baseChatContext = {
    sessionId: "mock-session-id",
    messages: mockMessages,
    isResponding: false,
    isCancelling: false,
    selectedAgentName: "OrchestratorAgent",
    isSidePanelCollapsed: true,
    activeSidePanelTab: "files",
};

const meta = {
    title: "Chat/ChatPage Variants",
    component: ChatPage,
    parameters: {
        msw: { handlers },
    },
} satisfies Meta<typeof ChatPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const AgentMode: Story = {
    parameters: {
        chatContext: { ...baseChatContext, messages: [], sessionId: "" },
        uiMode: agentUIMode("OrchestratorAgent"),
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByTestId("chat-input");
        expect(canvas.queryByTestId("chatAgentSelect")).not.toBeInTheDocument();
        await canvas.findByTestId("Files");
        expect(canvas.queryByTestId("Activity")).not.toBeInTheDocument();
        await canvas.findByTestId("showRecentChats");
    },
};

const emptyAgentsChatContext = { ...baseChatContext, messages: [], sessionId: "", agents: [] };

export const NoAgentsFound: Story = {
    parameters: {
        chatContext: emptyAgentsChatContext,
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("No Agents Found");
        await canvas.findByRole("button", { name: /Refresh/i });
    },
};

export const AgentDiscoveryError: Story = {
    parameters: {
        chatContext: emptyAgentsChatContext,
        msw: { handlers: [http.get("*/api/v1/agentCards", () => new HttpResponse(null, { status: 500 })), ...handlers] },
    },
    play: async ({ canvasElement }: { canvasElement: HTMLElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Unable to Load Agents");
        expect(canvas.queryByText("No Agents Found")).not.toBeInTheDocument();
        await canvas.findByRole("button", { name: /Refresh/i });
    },
};
