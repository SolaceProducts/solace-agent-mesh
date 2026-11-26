// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { CAPABILITIES, type Capability } from "@/providers/contexts/UserContext";

import type { AgentCard } from "@/types/a2a";

import { AgentMeshPage } from "@/features/agents/AgentMeshPage";

import { createAgentCards, createAgents } from "@/testing/data";
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

const contextHolding = (...held: Capability[]) => ({
    user: { capabilities: held },
    userHasCapability: (capability: Capability) => held.includes(capability),
    CAPABILITIES,
});

const meta = {
    title: "Agents/AgentMeshPage/Reduced Access",
    component: AgentMeshPage,
    parameters: {
        docs: {
            description: {
                component: "The main agents page component for a user with reduced access.",
            },
        },
    },
} satisfies Meta<typeof AgentMeshPage>;

export default meta;
type Story = StoryObj<typeof meta>;

const listHandlers = handlersFor({ agents: asDeployed(createAgents(5)), agentCards: createAgentCards(5) });

export const CanRead: Story = {
    parameters: {
        msw: { handlers: listHandlers },
        configContext: {
            persistenceEnabled: true,
        },
        userContext: contextHolding(CAPABILITIES.readAgents),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Agent 0");
        expect(await canvas.findByTestId("More")).toBeInTheDocument();
        expect(canvas.queryByTitle("Refresh Agents")).not.toBeInTheDocument();
    },
};

export const CanCreate: Story = {
    parameters: {
        msw: { handlers: listHandlers },
        configContext: {
            persistenceEnabled: true,
        },
        userContext: contextHolding(CAPABILITIES.readAgents, CAPABILITIES.createAgents),
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Agent 0");
        expect(await canvas.findByTitle("Refresh Agents")).toBeInTheDocument();
        expect(canvas.queryByTestId("More")).not.toBeInTheDocument();
    },
};
