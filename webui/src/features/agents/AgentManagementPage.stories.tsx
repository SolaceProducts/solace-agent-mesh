// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, fireEvent, userEvent, within } from "storybook/test";

import type { AgentCard } from "@/types/a2a";

import { createDeployedAgent, mockAgentCards, mockAgents, mockConnectors, mockGetConnectorSchema, mockRemoteAgents, mockToolsets } from "@/testing/data";
import { agentToV2, connectorToV2, remoteAgentToV2, toolsetToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";
import type { Agent, RemoteAgent } from "@/types";

import { AgentManagementPage } from "./AgentManagementPage";

const handlersFor = (agents: Agent[], remoteAgents: RemoteAgent[] = [], agentCards: AgentCard[] = mockAgentCards) =>
    withDefaults(
        http.get("*/api/v2/platform/agents", () => HttpResponse.json(paginatedResponse(agents.map(agentToV2)))),
        http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json(paginatedResponse(remoteAgents.map(remoteAgentToV2)))),
        http.get("*/api/v2/platform/toolsets", () => HttpResponse.json(paginatedResponse(mockToolsets.map(toolsetToV2)))),
        http.get("*/api/v2/platform/connectors", () => HttpResponse.json(paginatedResponse(mockConnectors.map(connectorToV2)))),
        http.get("*/api/v1/agentCards", () => HttpResponse.json(paginatedResponse(agentCards)))
    );

const isDeployedRunning = (a: { deploymentStatus?: string; runtimeStatus?: { status?: string } }) => a.deploymentStatus === "deployed" && (a.runtimeStatus?.status === "running" || a.runtimeStatus?.status === "degraded");
const dbBackedNames = new Set<string>([...mockAgents.map(a => a.name), ...mockRemoteAgents.map(a => a.name)]);
const deployedDbCount = mockAgents.filter(isDeployedRunning).length + mockRemoteAgents.filter(isDeployedRunning).length;
const discoveredCount = mockAgentCards.filter(c => !dbBackedNames.has(c.name)).length;
const EXPECTED_DEPLOYED_COUNT = deployedDbCount + discoveredCount;
const EXPECTED_UNDEPLOYED_COUNT = mockAgents.length + mockRemoteAgents.length - deployedDbCount;

const meta = {
    title: "Agents/AgentManagementPage",
    component: AgentManagementPage,
    parameters: {
        docs: {
            description: {
                component: "Admin-only Agents page with a deployed/undeployed selector.",
            },
        },
        chatContext: {
            sessionId: "mock-session-id",
            userAgents: mockAgents,
            toolsets: mockToolsets,
            connectors: mockConnectors,
            getConnectorSchema: mockGetConnectorSchema,
            remoteAgents: mockRemoteAgents,
            remoteAgentCards: [],
        },
    },
} satisfies Meta<typeof AgentManagementPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Deployed: Story = {
    parameters: {
        msw: { handlers: handlersFor(mockAgents, mockRemoteAgents) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByRole("button", { name: new RegExp(`^Deployed \\(${EXPECTED_DEPLOYED_COUNT}\\)`, "i") });
        await canvas.findByRole("button", { name: new RegExp(`^Undeployed \\(${EXPECTED_UNDEPLOYED_COUNT}\\)`, "i") });
        const rows = await canvas.findAllByRole("row");
        expect(rows.length).toBeGreaterThan(1);
    },
};

const offlineRemoteAgent: RemoteAgent = {
    ...mockRemoteAgents[0],
    id: "remote-offline-mgmt",
    name: "OfflineRemoteAgent",
    runtimeStatus: { status: "disconnected" },
    syncStatus: "in_sync",
};

export const DeployedNotRunningRemote: Story = {
    parameters: {
        msw: { handlers: handlersFor([], [offlineRemoteAgent], []) },
        chatContext: {
            sessionId: "mock-session-id",
            userAgents: [],
            toolsets: mockToolsets,
            connectors: mockConnectors,
            getConnectorSchema: mockGetConnectorSchema,
            remoteAgents: [offlineRemoteAgent],
            remoteAgentCards: [],
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByRole("button", { name: /^Deployed \(1\)/i });
        await canvas.findByRole("button", { name: /^Undeployed \(0\)/i });
        await canvas.findByText("OfflineRemoteAgent");
        await canvas.findByText("Not Running");
    },
};

const registeredRemoteAgent: RemoteAgent = {
    ...mockRemoteAgents[0],
    id: "remote-registered-mgmt",
    name: "RegisteredRemoteAgent",
    agentCardLocation: "none",
    runtimeStatus: { status: "disconnected" },
    syncStatus: "in_sync",
};

export const DeployedRegisteredRemote: Story = {
    parameters: {
        msw: { handlers: handlersFor([], [registeredRemoteAgent], []) },
        chatContext: {
            sessionId: "mock-session-id",
            userAgents: [],
            toolsets: mockToolsets,
            connectors: mockConnectors,
            getConnectorSchema: mockGetConnectorSchema,
            remoteAgents: [registeredRemoteAgent],
            remoteAgentCards: [],
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByRole("button", { name: /^Deployed \(1\)/i });
        await canvas.findByRole("button", { name: /^Undeployed \(0\)/i });
        await canvas.findByText("RegisteredRemoteAgent");
        await canvas.findByText("Registered");
    },
};

const degradedAgent: Agent = createDeployedAgent("degraded-mgmt", {
    name: "DegradedAgent",
    runtimeStatus: { status: "degraded", problems: [{ id: "queue:x", severity: "degraded", detail: "temporary receive error; retrying automatically", since: "2026-01-01T00:00:00Z" }] },
});

export const DeployedDegradedAgent: Story = {
    parameters: {
        msw: { handlers: handlersFor([degradedAgent], [], []) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByRole("button", { name: /^Deployed \(1\)/i });
        await canvas.findByRole("button", { name: /^Undeployed \(0\)/i });
        await canvas.findByText("DegradedAgent");
        await canvas.findByText("Degraded");
    },
};

export const Undeployed: Story = {
    parameters: {
        msw: { handlers: handlersFor(mockAgents, mockRemoteAgents) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const undeployedButton = await canvas.findByRole("button", { name: /^Undeployed \(\d+\)/i });
        await userEvent.click(undeployedButton);

        const notDeployed = await canvas.findAllByText(/Not Deployed/i);
        expect(notDeployed.length).toBeGreaterThanOrEqual(1);
    },
};

export const SearchClearsToEmpty: Story = {
    parameters: {
        msw: { handlers: handlersFor(mockAgents, mockRemoteAgents) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const search = await canvas.findByTestId("agentManagementSearchInput");
        fireEvent.change(search, { target: { value: "ZZZZZZ" } });

        await canvas.findByText("No Agents Found");
    },
};

export const Empty: Story = {
    parameters: {
        msw: { handlers: handlersFor([], [], []) },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByRole("button", { name: /^Deployed \(0\)/i });
        await canvas.findByRole("button", { name: /^Undeployed \(0\)/i });
        await canvas.findByText("No Agents Found");
    },
};

export const FetchError: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/agents", () => HttpResponse.json({ message: "Internal server error", errorId: "stub" }, { status: 500 })),
                http.get("*/api/v2/platform/toolsets", () => HttpResponse.json(paginatedResponse(mockToolsets.map(toolsetToV2)))),
                http.get("*/api/v2/platform/connectors", () => HttpResponse.json(paginatedResponse(mockConnectors.map(connectorToV2)))),
                http.get("*/api/v1/agentCards", () => HttpResponse.json(paginatedResponse(mockAgentCards)))
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText(/^Could not load agents/);
    },
};
