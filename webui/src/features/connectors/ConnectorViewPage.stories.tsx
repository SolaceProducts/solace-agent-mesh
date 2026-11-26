// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { delay, http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { ConnectorViewPage } from "@/features/connectors/ConnectorViewPage";

import { mockAgents, mockConnectors, mockConnectorsSchemaMap, mockGetConnectorSchema } from "@/testing/data";
import { agentToV2, connectorToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

const handlers = withDefaults(
    http.get("*/api/v2/platform/connectors", () => HttpResponse.json(paginatedResponse(mockConnectors.map(connectorToV2)))),
    http.get("*/api/v2/platform/connectors/:id", ({ params }) => {
        const connector = mockConnectors.find(c => c.id === params.id);
        if (!connector) {
            return HttpResponse.json({ message: "connector not found", errorId: "mock-error-id" }, { status: 404 });
        }
        return HttpResponse.json({ data: connectorToV2(connector) });
    }),
    http.get("*/api/v2/platform/agents", ({ request }) => {
        const url = new URL(request.url);
        const connectorId = url.searchParams.get("connectorId");

        const data = connectorId === "mock-connector-1" ? mockAgents : [];
        return HttpResponse.json(paginatedResponse(data.map(agentToV2)));
    })
);

const loadingHandlers = withDefaults(
    http.get("*/api/v2/platform/connectors/:id", async () => {
        await delay("infinite");
        return HttpResponse.json({});
    })
);

const meta = {
    title: "Connectors/ConnectorViewPage",
    component: ConnectorViewPage,
    parameters: {
        docs: {
            description: {
                component: "The connector view page component that displays the connector details.",
            },
        },
        msw: { handlers },
    },
} satisfies Meta<typeof ConnectorViewPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            connectors: mockConnectors,
            connectorSchemasMap: mockConnectorsSchemaMap,
            getConnectorSchema: mockGetConnectorSchema,
            useLoaderData: () => ({ connectorId: "mock-connector-2" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByTestId("closeButton");
        await canvas.findByTestId("editConnectorButton");
        await expect(await canvas.findByTestId("connector-review-host")).toHaveTextContent("localhost");
        await expect(await canvas.findByTestId("connector-review-port")).toHaveTextContent("3306");
    },
};

export const Configuration: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            connectors: mockConnectors,
            connectorSchemasMap: mockConnectorsSchemaMap,
            getConnectorSchema: mockGetConnectorSchema,
            useLoaderData: () => ({ connectorId: "mock-connector-1" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByTestId("closeButton");
        await canvas.findByTestId("editConnectorButton");
    },
};

export const AgentUsage: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            connectors: mockConnectors,
            connectorSchemasMap: mockConnectorsSchemaMap,
            getConnectorSchema: mockGetConnectorSchema,
            useLoaderData: () => ({ connectorId: "mock-connector-1" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const agentsTab = await canvas.findByRole("tab", { name: /Agent Usage/i });
        agentsTab.click();

        for (const agent of mockAgents) {
            await canvas.findByTestId(`agentLink-${agent.id}`);
        }
    },
};

export const MaxLength: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            connectors: mockConnectors,
            connectorSchemasMap: mockConnectorsSchemaMap,
            getConnectorSchema: mockGetConnectorSchema,
            useLoaderData: () => ({ connectorId: "mock-connector-3" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByTestId("closeButton");
        await canvas.findByTestId("editConnectorButton");
    },
};

export const Loading: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            connectors: [],
            connectorSchemasMap: mockConnectorsSchemaMap,
            getConnectorSchema: mockGetConnectorSchema,
            useLoaderData: () => ({ connectorId: "mock-connector-1" }),
        },
        configContext: {
            persistenceEnabled: true,
        },
        msw: { handlers: loadingHandlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Loading Connector...");
    },
};
