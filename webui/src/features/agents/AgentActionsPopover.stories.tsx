// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, fn, userEvent, waitFor, within } from "storybook/test";

import { transformAgent } from "@/api/agents";
import { transformRemoteAgent } from "@/api/remote-agents";

import { AgentActionsPopover } from "@/features/agents/AgentActionsPopover";

import { getAgentById, mockAgents, mockConnectors, mockMessages, mockRemoteAgents, mockToolsets } from "@/testing/data";
import { agentToV2, connectorToV2, remoteAgentToV2, toolsetToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";
import type { Agent as AgentType } from "@/types";

const makeListHandlers = (agents: AgentType[] = mockAgents) =>
    withDefaults(
        http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse(agents.map(agentToV2)))),
        http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json(paginatedResponse(mockRemoteAgents.map(remoteAgentToV2)))),
        http.get("*/api/v2/platform/connectors", () => HttpResponse.json(paginatedResponse(mockConnectors.map(connectorToV2)))),
        http.get("*/api/v2/platform/toolsets", ({ request }) => {
            const type = new URL(request.url).searchParams.get("origin");
            const list = type === "builtin" ? mockToolsets : [];
            return HttpResponse.json(paginatedResponse(list.map(toolsetToV2)));
        })
    );

const baseChatContext = {
    sessionId: "mock-session-id",
    messages: mockMessages,
    userAgents: mockAgents,
    remoteAgents: [],
    remoteAgentCards: [],
};

const inactiveAgent = getAgentById("10")!;

const meta = {
    title: "Agents/AgentActionsPopover",
    component: AgentActionsPopover,
    parameters: {
        layout: "centered",
        msw: { handlers: makeListHandlers() },
        chatContext: baseChatContext,
    },
    args: {
        agent: transformAgent(inactiveAgent, mockToolsets, mockConnectors),
        userAgent: inactiveAgent,
        selectedState: "inactive",
        children: <button data-testid="agentActionsTrigger">Actions</button>,
    },
} satisfies Meta<typeof AgentActionsPopover>;

export default meta;
type Story = StoryObj<typeof meta>;

const offlineRemoteAgent = {
    ...mockRemoteAgents[0],
    id: "remote-offline-popover",
    name: "OfflineRemoteAgent",
    runtimeStatus: { status: "disconnected" as const },
    syncStatus: "in_sync" as const,
};

export const RemoteDeployedNotRunning: Story = {
    args: {
        agent: transformRemoteAgent(offlineRemoteAgent),
        userAgent: { id: offlineRemoteAgent.id, name: offlineRemoteAgent.name } as unknown as AgentType,
        selectedState: "active",
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            remoteAgents: [offlineRemoteAgent],
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const body = within(document.body);

        await userEvent.click(await canvas.findByTestId("agentActionsTrigger"));
        await body.findByRole("menuitem", { name: "Undeploy" });
        expect(body.queryByRole("menuitem", { name: "Redeploy" })).toBeNull();
        expect(body.queryByRole("menuitem", { name: "Delete" })).toBeNull();
    },
};

const deleteAgentSpy = fn(async () => true);

export const DeleteAgentConfirmation: Story = {
    parameters: {
        chatContext: {
            ...baseChatContext,
            handleDeleteAgent: deleteAgentSpy,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const body = within(document.body);

        await userEvent.click(await canvas.findByTestId("agentActionsTrigger"));
        await userEvent.click(await body.findByRole("menuitem", { name: "Delete" }));

        const dialog = within(await body.findByRole("dialog"));
        await dialog.findByText(/session history/i);

        const confirmButton = dialog.getByRole("button", { name: "Delete" });
        expect(confirmButton).toBeDisabled();
        await userEvent.type(dialog.getByRole("textbox"), "DELETE");
        await waitFor(() => expect(confirmButton).toBeEnabled());

        await userEvent.click(confirmButton);
        await waitFor(() => expect(deleteAgentSpy).toHaveBeenCalled());
        await waitFor(() => expect(body.queryByRole("dialog")).not.toBeInTheDocument());
    },
};
