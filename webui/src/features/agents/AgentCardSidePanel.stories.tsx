// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, fn, userEvent, waitFor, within } from "storybook/test";

import { transformAgentCard } from "@/api/agent-cards";
import { transformAgent } from "@/api/agents";
import { transformRemoteAgent } from "@/api/remote-agents";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import { AgentCardSidePanel } from "@/features/agents/AgentCardSidePanel";

import { getAgentById, mockAgentCards, mockAgents, mockAgentsWithDuplicates, mockConnectors, mockGetConnectorSchema, mockMessages, mockRemoteAgents, mockRemoteAgentsWithDuplicates, mockToolsets, orchestratorAgent } from "@/testing/data";
import { agentToV2, connectorToV2, remoteAgentToV2, toolsetToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";
import type { Agent as AgentType } from "@/types";

const makeListHandlers = (agents: AgentType[] = mockAgents) =>
    withDefaults(
        http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse(agents.map(agentToV2)))),
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
    isResponding: false,
    isCancelling: false,
    agents: mockAgentCards,
    remoteAgents: [],
    remoteAgentCards: [],
    isSidePanelCollapsed: true,
    activeSidePanelTab: "files",
    getConnectorSchema: mockGetConnectorSchema,
};

const meta = {
    title: "Agents/AgentCardSidePanel",
    component: AgentCardSidePanel,
    parameters: {
        docs: {
            description: {
                component: "The main agent card side panel component that displays the agent's details.",
            },
        },
        msw: { handlers: makeListHandlers() },
    },
    decorators: [
        Story => (
            <div className="ml-auto h-screen w-2/5">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof AgentCardSidePanel>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        agent: transformAgentCard(orchestratorAgent),
        selectedState: "active",
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            selectedAgentName: "Orchestrator",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("chatWithAgent");
    },
};

export const Agent: Story = {
    args: {
        agent: transformAgent(getAgentById("0")!, mockToolsets, mockConnectors),
        selectedState: "active",
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            selectedAgentName: "Orchestrator",
        },
    },

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("chatWithAgent");
        await canvas.findByText("Toolsets (1)");
        await canvas.findByText("Connectors (1)");
        await userEvent.click(await canvas.findByText("Agent Card"));
        await canvas.findByText("Abilities (2)");
    },
};

export const InactiveAgent: Story = {
    args: {
        agent: transformAgent(getAgentById("10")!, mockToolsets, mockConnectors),
        selectedState: "inactive",
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            userAgents: mockAgents,
        },
    },

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("deployAgent");
        await canvas.findByTestId("openAgent");
    },
};

export const InactiveAgentReadOnly: Story = {
    args: {
        agent: transformAgent(getAgentById("10")!, mockToolsets, mockConnectors),
        selectedState: "inactive",
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            userAgents: mockAgents,
        },
        userContext: {
            userHasCapability: (capability: string) => capability === CAPABILITIES.readAgents,
            CAPABILITIES,
        },
    },

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("openAgent");
        expect(canvas.queryByTestId("deployAgent")).toBeNull();
        expect(canvas.queryByTestId("deleteAgent")).toBeNull();
    },
};

export const DeployFailedAgent: Story = {
    args: {
        agent: transformAgent(getAgentById("6")!, mockToolsets, mockConnectors),
        selectedState: "inactive",
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            userAgents: mockAgents,
        },
    },

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("messageBanner");
        await canvas.findByTestId("redeployAgent");
        await canvas.findByTestId("deleteAgent");
        await canvas.findByTestId("openAgent");
    },
};

export const DisconnectedAgent: Story = {
    args: {
        agent: transformAgent(getAgentById("1")!, mockToolsets, mockConnectors),
        selectedState: "inactive",
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            userAgents: mockAgents,
        },
    },

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("messageBanner");
        expect(await canvas.findByTestId("redeployAgent")).toBeInTheDocument();
        expect(await canvas.findByTestId("deleteAgent")).toBeInTheDocument();
        expect(await canvas.findByTestId("openAgent")).toBeInTheDocument();
    },
};

export const DisconnectedOutOfSyncAgent: Story = {
    args: {
        agent: transformAgent(getAgentById("4")!, mockToolsets, mockConnectors),
        selectedState: "inactive",
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            userAgents: mockAgents,
        },
    },

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const banners = await canvas.findAllByTestId("messageBanner");
        expect(banners.length).toBe(1);
        expect(canvas.queryByTestId("deployAgent")).toBeNull();
        await canvas.findByTestId("previewUpdates");
        await canvas.findByTestId("deleteAgent");
        await canvas.findByTestId("openAgent");
    },
};

export const DeployFailedOutOfSyncAgent: Story = {
    args: {
        agent: transformAgent(getAgentById("7")!, mockToolsets, mockConnectors),
        selectedState: "inactive",
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            userAgents: mockAgents,
        },
    },

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("messageBanner");
        expect(canvas.queryByTestId("redeployAgent")).toBeNull();
        expect(canvas.queryByTestId("deployAgent")).toBeNull();
        await canvas.findByTestId("previewUpdates");
        await canvas.findByTestId("deleteAgent");
        await canvas.findByTestId("openAgent");
    },
};

export const ComparisonDialogMatchingStatus: Story = {
    args: {
        agent: transformAgent(getAgentById("3")!, mockToolsets, mockConnectors),
        selectedState: "active",
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            userAgents: mockAgents,
        },
    },

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const previewButton = await canvas.findByTestId("previewUpdates");
        await userEvent.click(previewButton);

        const body = within(document.body);
        const dialog = await body.findByRole("dialog");
        const dialogCanvas = within(dialog);
        await dialogCanvas.findByText("Running");
        const notDeployedElements = await dialogCanvas.findAllByText("Not Deployed");
        expect(notDeployedElements.length).toBeGreaterThan(0);
    },
};

export const DuplicateNames: Story = {
    args: {
        agent: transformAgent(getAgentById("18")!, mockToolsets, mockConnectors),
        selectedState: "active",
    },
    parameters: {
        msw: { handlers: makeListHandlers(mockAgentsWithDuplicates) },
        chatContext: {
            ...baseChatContext,
            yamlAgentCards: mockAgentsWithDuplicates.filter(agent => agent.deploymentStatus === "deployed").map(agent => transformAgent(agent, mockToolsets, mockConnectors)),
        },
    },

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText(/agents have the same name/, undefined, { timeout: 5000 });

        await canvas.findByText("Agent 18 description - deployed, in sync");

        await canvas.findByText("Running");
    },
};

export const ViewOnlyAdmin: Story = {
    args: {
        agent: transformAgent(getAgentById("0")!, mockToolsets, mockConnectors),
        selectedState: "active",
        viewOnly: true,
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            userAgents: mockAgents,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("manageAgent");
        await canvas.findByTestId("chatWithAgent");
        expect(canvas.queryByTestId("deployAgent")).toBeNull();
        expect(canvas.queryByTestId("undeployAgent")).toBeNull();
        expect(canvas.queryByTestId("redeployAgent")).toBeNull();
        expect(canvas.queryByTestId("deleteAgent")).toBeNull();
        expect(canvas.queryByTestId("openAgent")).toBeNull();
        expect(canvas.queryByTestId("previewUpdates")).toBeNull();
    },
};

export const ViewOnlyNonAdmin: Story = {
    args: {
        agent: transformAgent(getAgentById("0")!, mockToolsets, mockConnectors),
        selectedState: "active",
        viewOnly: true,
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            userAgents: mockAgents,
        },
        userContext: {
            userHasCapability: (capability: string) => capability === "readAgents",
            CAPABILITIES,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("chatWithAgent");
        expect(canvas.queryByTestId("manageAgent")).toBeNull();
        expect(canvas.queryByTestId("deployAgent")).toBeNull();
        expect(canvas.queryByTestId("undeployAgent")).toBeNull();
    },
};

const offlineRemoteAgent = {
    ...mockRemoteAgents[0],
    id: "remote-offline-sidepanel",
    name: "OfflineRemoteAgent",
    runtimeStatus: { status: "disconnected" as const },
    syncStatus: "in_sync" as const,
};

export const RemoteDeployedNotRunning: Story = {
    args: {
        agent: transformRemoteAgent(offlineRemoteAgent),
        selectedState: "active",
    },
    parameters: {
        msw: {
            handlers: withDefaults(http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json(paginatedResponse([offlineRemoteAgent].map(remoteAgentToV2))))),
        },
        chatContext: {
            ...baseChatContext,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("undeployAgent");
        expect(canvas.queryByTestId("redeployAgent")).toBeNull();
        expect(canvas.queryByTestId("deleteAgent")).toBeNull();
        await canvas.findByText("Not Running");
    },
};

export const DuplicateNamesExternal: Story = {
    args: {
        agent: transformRemoteAgent(mockRemoteAgentsWithDuplicates.find(agent => agent.id === "dupe-agent-001")!),
        selectedState: "active",
    },
    parameters: {
        msw: {
            handlers: withDefaults(http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json(paginatedResponse(mockRemoteAgentsWithDuplicates.map(remoteAgentToV2))))),
        },
        chatContext: {
            ...baseChatContext,
            userAgentCards: [],
            yamlAgentCards: [],
        },
    },

    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText(/agents have the same name/, undefined, { timeout: 5000 });

        await canvas.findByText("An agent with a duplicate");

        await canvas.findByText("External");
    },
};

const sidePanelDeleteAgentSpy = fn(async () => true);

export const DeleteAgentConfirmation: Story = {
    args: {
        agent: transformAgent(getAgentById("6")!, mockToolsets, mockConnectors),
        selectedState: "inactive",
    },
    parameters: {
        chatContext: {
            ...baseChatContext,
            userAgents: mockAgents,
            handleDeleteAgent: sidePanelDeleteAgentSpy,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const body = within(document.body);

        await userEvent.click(await canvas.findByTestId("deleteAgent"));
        const dialog = within(await body.findByRole("dialog"));

        await dialog.findByText(/session history/i);

        const confirmButton = dialog.getByRole("button", { name: "Delete" });
        expect(confirmButton).toBeDisabled();
        await userEvent.type(dialog.getByRole("textbox"), "DELETE");
        await waitFor(() => expect(confirmButton).toBeEnabled());

        await userEvent.click(confirmButton);
        await waitFor(() => expect(sidePanelDeleteAgentSpy).toHaveBeenCalled());
        await waitFor(() => expect(body.queryByRole("dialog")).not.toBeInTheDocument());
    },
};
