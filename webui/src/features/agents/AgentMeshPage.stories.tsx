// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, userEvent, waitFor, within } from "storybook/test";

import { transformAgentCard } from "@/api/agent-cards";
import { transformAgent } from "@/api/agents";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import type { AgentCard } from "@/types/a2a";

import { AgentMeshPage } from "@/features/agents/AgentMeshPage";

import { createAgentCards, createAgents, createDiscoveredAgentCardInfos, mockAgentCards, mockAgents, mockAgentsWithDuplicates, mockConnectors, mockRemoteAgentCardsFromMesh, mockRemoteAgents, mockToolsets } from "@/testing/data";
import { agentToV2, connectorToV2, remoteAgentToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";
import type { Agent, AgentCardInfo, Connector, RemoteAgent } from "@/types";

interface PageHandlers {
    agents?: Agent[];
    remoteAgents?: RemoteAgent[];
    agentCards?: AgentCard[];
    connectors?: Connector[];
}

const handlersFor = ({ agents = [], remoteAgents = [], agentCards = [], connectors = [] }: PageHandlers = {}) =>
    withDefaults(
        http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse(agents.map(agentToV2)))),
        http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json(paginatedResponse(remoteAgents.map(remoteAgentToV2)))),
        http.get("*/api/v1/agentCards", () => HttpResponse.json(paginatedResponse(agentCards))),
        http.get("*/api/v2/platform/connectors", () => HttpResponse.json(paginatedResponse(connectors.map(connectorToV2))))
    );

const clickLayoutButton = async (canvas: ReturnType<typeof within>, name: RegExp) => {
    await waitFor(() => userEvent.click(canvas.getByRole("button", { name })));
};

const asDeployed = (agents: Agent[]): Agent[] => agents.map(a => ({ ...a, deploymentStatus: "deployed", runtimeStatus: { status: "running" } }));

const meta = {
    title: "Agents/AgentMeshPage",
    component: AgentMeshPage,
    parameters: {
        docs: {
            description: {
                component: "The main agents page component that displays the agents list, details and handles user interactions.",
            },
        },
    },
} satisfies Meta<typeof AgentMeshPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        msw: { handlers: handlersFor() },
        chatContext: {
            sessionId: "mock-session-id",
            yamlAgentCards: [] as AgentCardInfo[],
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("No Agents Found")).toBeInTheDocument();
    },
};

export const AgentsWithPagination: Story = {
    parameters: {
        msw: { handlers: handlersFor({ agents: asDeployed(createAgents(25)), agentCards: createAgentCards(25) }) },
        chatContext: {
            sessionId: "mock-session-id",
            yamlAgentCards: createAgentCards(25).map(transformAgentCard),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Agent 1")).toBeInTheDocument();
        expect(canvas.queryByText("Agent 49")).toBeNull();
    },
};

export const AgentsWithPaginationTable: Story = {
    parameters: {
        msw: { handlers: handlersFor({ agents: asDeployed(createAgents(25)), agentCards: createAgentCards(25) }) },
        chatContext: {
            sessionId: "mock-session-id",
            yamlAgentCards: createAgentCards(25).map(transformAgentCard),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await clickLayoutButton(canvas, /Table/i);
        expect(await canvas.findByRole("table")).toBeInTheDocument();
    },
};

export const AgentsActive: Story = {
    parameters: {
        msw: { handlers: handlersFor({ agents: mockAgents, agentCards: mockAgentCards }) },
        chatContext: {
            sessionId: "mock-session-id",
            yamlAgentCards: createDiscoveredAgentCardInfos(),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Agent 3");
        await canvas.findByText("Orchestrator");
    },
};

export const AgentsActiveNonAdmin: Story = {
    parameters: {
        msw: { handlers: handlersFor({ agents: mockAgents, agentCards: mockAgentCards }) },
        chatContext: {
            sessionId: "mock-session-id",
            yamlAgentCards: createDiscoveredAgentCardInfos(),
        },
        configContext: {
            persistenceEnabled: true,
        },
        userContext: {
            user: { capabilities: [CAPABILITIES.readAgents] },
            userHasCapability: (capability: string) => capability === "readAgents",
            CAPABILITIES,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Agent 3");
        await canvas.findByText("Orchestrator");
    },
};

export const RemoteAgents: Story = {
    parameters: {
        msw: { handlers: handlersFor({ remoteAgents: mockRemoteAgents, agentCards: mockRemoteAgentCardsFromMesh as unknown as AgentCard[] }) },
        chatContext: {
            sessionId: "mock-session-id",
            yamlAgentCards: [...mockRemoteAgentCardsFromMesh],
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect((await canvas.findAllByText("WeatherAgent")).length).toBeGreaterThan(0);
        expect((await canvas.findAllByText("AdditionAgent")).length).toBeGreaterThan(0);
        expect((await canvas.findAllByText("Division Agent")).length).toBeGreaterThan(0);
        expect((await canvas.findAllByText("LegacySystemAgent")).length).toBeGreaterThan(0);

        expect(canvas.queryByText("UndeployedAgent")).toBeNull();
        expect(canvas.queryByText("PartnerAPIAgent")).toBeNull();
    },
};

export const RegisteredAgents: Story = {
    parameters: {
        msw: { handlers: handlersFor({ remoteAgents: mockRemoteAgents }) },
        chatContext: {
            sessionId: "mock-session-id",
            yamlAgentCards: [] as AgentCardInfo[],
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("LegacySystemAgent");
        expect((await canvas.findAllByText("Registered")).length).toBeGreaterThan(0);
    },
};

export const DuplicateNamesCards: Story = {
    parameters: {
        msw: { handlers: handlersFor({ agents: asDeployed(mockAgentsWithDuplicates) }) },
        chatContext: {
            sessionId: "mock-session-id",
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByTestId("duplicate-name-warning-banner", undefined, { timeout: 3000 });
    },
};

export const DuplicateNamesHierarchy: Story = {
    parameters: {
        msw: { handlers: handlersFor({ agents: asDeployed(mockAgentsWithDuplicates) }) },
        chatContext: {
            sessionId: "mock-session-id",
            yamlAgentCards: mockAgentsWithDuplicates.map(agent => transformAgent(agent, mockToolsets, mockConnectors)),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await clickLayoutButton(canvas, /Hierarchical/i);
        await canvas.findByTestId("duplicate-name-warning-banner", undefined, { timeout: 3000 });
    },
};

export const LayoutSwitchingTableToHierarchical: Story = {
    parameters: {
        msw: { handlers: handlersFor({ agents: mockAgents, agentCards: mockAgentCards }) },
        chatContext: {
            sessionId: "mock-session-id",
            yamlAgentCards: createDiscoveredAgentCardInfos(),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await clickLayoutButton(canvas, /Table/i);

        await canvas.findByText("Name");
        await canvas.findByText("Status");

        await clickLayoutButton(canvas, /Hierarchical/i);

        const nameHeader = canvas.queryByText("Name");
        const statusHeader = canvas.queryByText("Status");
        expect(nameHeader).not.toBeInTheDocument();
        expect(statusHeader).not.toBeInTheDocument();
    },
};

export const LayoutSwitchingFullCycle: Story = {
    parameters: {
        msw: { handlers: handlersFor({ agents: mockAgents, agentCards: mockAgentCards }) },
        chatContext: {
            sessionId: "mock-session-id",
            yamlAgentCards: createDiscoveredAgentCardInfos(),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await clickLayoutButton(canvas, /Table/i);
        await canvas.findByText("Name");

        await clickLayoutButton(canvas, /Hierarchical/i);

        await clickLayoutButton(canvas, /Cards/i);
        await canvas.findByText("Agent 3");
    },
};

export const PaginationNavigationCards: Story = {
    parameters: {
        msw: { handlers: handlersFor({ agents: asDeployed(createAgents(50)), agentCards: createAgentCards(50) }) },
        chatContext: {
            sessionId: "mock-session-id",
            yamlAgentCards: createAgentCards(50).map(transformAgentCard),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const pagination = await canvas.findByRole("navigation");
        expect(pagination).toBeInTheDocument();

        const paginationNav = within(pagination);

        const nextButton = paginationNav.queryByRole("button", { name: /next/i }) || paginationNav.queryByLabelText(/next/i);
        if (nextButton) {
            await userEvent.click(nextButton);
        }
    },
};

export const PaginationNavigationTable: Story = {
    parameters: {
        msw: { handlers: handlersFor({ agents: asDeployed(createAgents(50)), agentCards: createAgentCards(50) }) },
        chatContext: {
            sessionId: "mock-session-id",
            yamlAgentCards: createAgentCards(50).map(transformAgentCard),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await clickLayoutButton(canvas, /Table/i);

        const pagination = await canvas.findByRole("navigation");
        expect(pagination).toBeInTheDocument();
    },
};

export const SearchAgents: Story = {
    parameters: {
        msw: { handlers: handlersFor({ agents: mockAgents, agentCards: mockAgentCards }) },
        chatContext: {
            sessionId: "mock-session-id",
            yamlAgentCards: createDiscoveredAgentCardInfos(),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const searchInput = await canvas.findByPlaceholderText(/filter by name/i);
        expect(searchInput).toBeInTheDocument();

        await userEvent.type(searchInput, "Agent 3");

        await canvas.findByText("Agent 3");
    },
};

export const SearchNoResults: Story = {
    parameters: {
        msw: { handlers: handlersFor({ agents: mockAgents, agentCards: mockAgentCards }) },
        chatContext: {
            sessionId: "mock-session-id",
            yamlAgentCards: createDiscoveredAgentCardInfos(),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const searchInput = await canvas.findByPlaceholderText(/filter by name/i);
        await userEvent.type(searchInput, "NonExistentAgentXYZ123");

        await canvas.findByText(/no.*agents found/i);
    },
};
