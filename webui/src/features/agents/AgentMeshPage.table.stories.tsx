// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, fireEvent, waitFor, within } from "storybook/test";

import type { AgentCard } from "@/types/a2a";

import { AgentMeshPage } from "@/features/agents/AgentMeshPage";

import { mockAgentCards, mockAgents, mockAgentsWithDuplicates, mockRemoteAgents } from "@/testing/data";
import { agentToV2, connectorToV2, remoteAgentToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";
import type { Agent, Connector, RemoteAgent } from "@/types";

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

const asDeployed = (agents: Agent[]): Agent[] => agents.map(a => ({ ...a, deploymentStatus: "deployed", runtimeStatus: { status: "running" } }));

const meta = {
    title: "Agents/AgentMeshPage/Table",
    component: AgentMeshPage,
    parameters: {
        docs: {
            description: {
                component: "The main agents page table view that displays the agents list, details and handles user interactions.",
            },
        },
    },
    decorators: [
        Story => {
            localStorage.setItem("sam-layout-preference", "table");
            return <Story />;
        },
    ],
} satisfies Meta<typeof AgentMeshPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        msw: { handlers: handlersFor({ agentCards: mockAgentCards.slice(0, 1) }) },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findAllByText("Running")).toHaveLength(1);
    },
};

export const DuplicateNames: Story = {
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

export const Active: Story = {
    parameters: {
        msw: { handlers: handlersFor({ agents: asDeployed(mockAgents), agentCards: mockAgentCards }) },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const runningElements = await canvas.findAllByText("Running");
        expect(runningElements.length).toBeGreaterThanOrEqual(1);
        const internalElements = await canvas.findAllByText("Internal");
        expect(internalElements.length).toBeGreaterThanOrEqual(2);
    },
};

export const ActiveFiltered: Story = {
    parameters: {
        msw: { handlers: handlersFor({ agents: asDeployed(mockAgents), agentCards: mockAgentCards }) },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const runningElements = await canvas.findAllByText("Running");
        expect(runningElements.length).toBeGreaterThanOrEqual(1);
        await canvas.findByTestId("agentSearchInput").then(input => {
            fireEvent.change(input, { target: { value: "ZZZZZ" } });
        });
        expect(await canvas.findByText("No Agents Found")).toBeInTheDocument();
    },
};

export const RemoteAgents: Story = {
    parameters: {
        msw: { handlers: handlersFor({ remoteAgents: mockRemoteAgents }) },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await waitFor(async () => {
            const rows = await canvas.findAllByText("External");
            expect(rows.length).toBeGreaterThanOrEqual(4);
        });

        const searchInput = await canvas.findByTestId("agentSearchInput");
        fireEvent.change(searchInput, { target: { value: "WeatherAgent" } });
        await canvas.findByText("WeatherAgent");
        await canvas.findByText("user1@example.com");

        fireEvent.change(searchInput, { target: { value: "" } });

        await canvas.findByText("WeatherAgent");
        await canvas.findByText("AdditionAgent");
        await canvas.findByText("Division Agent");
        await canvas.findByText("LegacySystemAgent");

        expect(canvas.queryByText("UndeployedAgent")).toBeNull();
        expect(canvas.queryByText("PartnerAPIAgent")).toBeNull();
    },
};
