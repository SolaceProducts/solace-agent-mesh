// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { agentUIMode } from "@/providers/contexts";

import { RecentChatsPage } from "@/features/chat/recentChats/RecentChatsPage";

import { mockSessions } from "@/testing/data/sessions";
import { getEmptyResponse, paginatedResponse, withDefaults } from "@/testing/helpers";

const handlers = withDefaults(
    http.get("*/api/v1/sessions", () => HttpResponse.json(paginatedResponse(mockSessions, 20))),
    getEmptyResponse("*/api/v1/config/features")
);
const emptyHandlers = withDefaults(getEmptyResponse("*/api/v1/config/features"));

const meta = {
    title: "Chat/RecentChatsPage",
    component: RecentChatsPage,
    parameters: {
        msw: { handlers },
        configContext: {
            persistenceEnabled: true,
        },
        chatContext: { sessionId: "session-1" },
    },
} satisfies Meta<typeof RecentChatsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const WithSessions: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Recent Chats");
        await canvas.findByText("Session 1");
        await canvas.findByText("Session 2");
        await canvas.findByText("Session 3");
        await canvas.findByText("New Chat", { selector: "button" });
    },
};

export const Empty: Story = {
    parameters: {
        msw: { handlers: emptyHandlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Recent Chats");
        await canvas.findByText("No chat sessions available");
    },
};

export const WithActiveSession: Story = {
    parameters: {
        chatContext: { sessionId: "session-2" },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const session2 = await canvas.findByText("Session 2");
        const card = session2.closest("[class*='bg-']");
        expect(card).toBeTruthy();
    },
};

export const AgentMode: Story = {
    parameters: {
        featureContext: { scheduler: true, chat_sharing: true },
        configContext: { persistenceEnabled: true, identityServiceType: "okta" },
        chatContext: { sessionId: "session-1", selectedAgentName: "OrchestratorAgent" },
        uiMode: agentUIMode("OrchestratorAgent"),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        (await canvas.findByTestId("showRecentChats")).click();
        expect(await canvas.findByRole("button", { name: /view all/i })).toBeDisabled();
        await canvas.findAllByText("Session 1");
        await canvas.findByText("New Chat", { selector: "button" });

        expect(canvas.queryByText("Scheduled Tasks")).not.toBeInTheDocument();
        expect(canvas.queryByText("Shared with Me")).not.toBeInTheDocument();
        expect(canvas.queryByText("Project:")).not.toBeInTheDocument();
    },
};
