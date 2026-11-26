// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { CAPABILITIES, type Capability } from "@/providers/contexts/UserContext";

import { createDeployedAgent, mockConnectors, mockGetConnectorSchema, mockToolsets } from "@/testing/data";
import { agentToV2, connectorToV2, toolsetToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";
import type { Agent } from "@/types";

import { AgentManagementPage } from "./AgentManagementPage";

const outOfSyncAgent: Agent = createDeployedAgent("read-only-mgmt", {
    name: "OutOfSyncAgent",
    description: "Deployed with undeployed changes.",
    syncStatus: "out_of_sync",
});

const handlers = withDefaults(
    http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse([outOfSyncAgent].map(agentToV2)))),
    http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json(paginatedResponse([]))),
    http.get("*/api/v2/platform/toolsets", () => HttpResponse.json(paginatedResponse(mockToolsets.map(toolsetToV2)))),
    http.get("*/api/v2/platform/connectors", () => HttpResponse.json(paginatedResponse(mockConnectors.map(connectorToV2)))),
    http.get("*/api/v1/agentCards", () => HttpResponse.json(paginatedResponse([])))
);

const meta = {
    title: "Agents/AgentManagementPage/Reduced Access",
    component: AgentManagementPage,
    parameters: {
        docs: {
            description: {
                component: "Agents page as seen by an agent_builder:*:read holder — the page is reachable read-only, its create affordance is not.",
            },
        },
        msw: { handlers },
        chatContext: {
            sessionId: "mock-session-id",
            userAgents: [outOfSyncAgent],
            toolsets: mockToolsets,
            connectors: mockConnectors,
            getConnectorSchema: mockGetConnectorSchema,
            remoteAgents: [],
            remoteAgentCards: [],
        },
    },
} satisfies Meta<typeof AgentManagementPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const CanRead: Story = {
    parameters: {
        userContext: {
            user: { capabilities: [CAPABILITIES.readAgents] },
            userHasCapability: (capability: Capability) => capability === CAPABILITIES.readAgents,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("OutOfSyncAgent")).toBeInTheDocument();
        expect(canvas.queryByTestId("addAgentDropdownTrigger")).not.toBeInTheDocument();

        expect(canvas.getByTestId("deploymentStatusInfo")).toBeInTheDocument();
    },
};

export const CanCreate: Story = {
    parameters: {
        userContext: {
            user: { capabilities: [CAPABILITIES.readAgents, CAPABILITIES.createAgents] },
            userHasCapability: (capability: Capability) => capability === CAPABILITIES.readAgents || capability === CAPABILITIES.createAgents,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("OutOfSyncAgent")).toBeInTheDocument();
        expect(await canvas.findByTestId("addAgentDropdownTrigger")).toBeInTheDocument();
    },
};
