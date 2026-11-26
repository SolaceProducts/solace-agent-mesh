// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { ConnectorsPage } from "@/features/connectors/ConnectorsPage";

import { createConnectors, mockConnectors, mockConnectorsSchemaMap, mockGetConnectorSchema } from "@/testing/data";
import { connectorToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";
import type { Connector } from "@/types";

const meta = {
    title: "Connectors/ConnectorsPage",
    component: ConnectorsPage,
    parameters: {
        docs: {
            description: {
                component: "The connectors page that lists all configured connectors.",
            },
        },
    },
} satisfies Meta<typeof ConnectorsPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            handleViewConnector: () => {},
            getConnectorSchema: mockGetConnectorSchema,
            connectorSchemasMap: mockConnectorsSchemaMap,
        },
        configContext: {
            persistenceEnabled: true,
        },
        msw: {
            handlers: withDefaults(http.get("*/api/v2/platform/connectors", () => HttpResponse.json(paginatedResponse(mockConnectors.map(connectorToV2), 20)))),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText(mockConnectors[0].name)).toBeInTheDocument();
    },
};

export const Connectors: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            connectorSchemasMap: mockConnectorsSchemaMap,
            getConnectorSchema: mockGetConnectorSchema,
            handleViewConnector: (connector: Connector) => {
                alert(`View Connector: ${connector.name}`);
            },
        },
        configContext: {
            persistenceEnabled: true,
        },
        msw: {
            handlers: withDefaults(http.get("*/api/v2/platform/connectors", () => HttpResponse.json(paginatedResponse(mockConnectors.map(connectorToV2), 20)))),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        for (const connector of mockConnectors) {
            expect(await canvas.findByText(connector.name)).toBeInTheDocument();
        }
    },
};

export const ConnectorsWithPagination: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            connectorSchemasMap: mockConnectorsSchemaMap,
            getConnectorSchema: mockGetConnectorSchema,
            handleViewConnector: (connector: Connector) => {
                alert(`View Connector: ${connector.name}`);
            },
        },
        configContext: {
            persistenceEnabled: true,
        },
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/connectors", ({ request }) => {
                    const url = new URL(request.url);
                    const pageNumber = Number(url.searchParams.get("pageNumber") ?? "1");
                    const pageSize = Number(url.searchParams.get("pageSize") ?? "20");
                    const all = createConnectors(50);
                    const start = (pageNumber - 1) * pageSize;
                    const slice = all.slice(start, start + pageSize);
                    const totalPages = Math.ceil(all.length / pageSize);
                    return HttpResponse.json({
                        data: slice.map(connectorToV2),
                        meta: {
                            pagination: {
                                pageNumber,
                                pageSize,
                                count: all.length,
                                nextPage: pageNumber < totalPages ? pageNumber + 1 : null,
                                totalPages,
                            },
                        },
                    });
                })
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("MySQL Connector 0");
        expect(await canvas.findByRole("navigation")).toBeInTheDocument();
    },
};
