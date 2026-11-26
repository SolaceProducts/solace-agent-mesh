// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, fn, screen, within } from "storybook/test";

import { ConnectorEditDialog } from "@/features/builder/ConnectorEditDialog";

import { mockConnectors, mockConnectorsSchemaMap, mockGetConnectorSchema } from "@/testing/data";
import { connectorToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

const connectorsListHandlers = withDefaults(http.get("*/api/v2/platform/connectors", () => HttpResponse.json(paginatedResponse(mockConnectors.map(connectorToV2)))));

const connectorJson = JSON.stringify(
    {
        name: "PostgreSQL Connector",
        description: "A connector for the production PostgreSQL database.",
        type: "sql",
        subtype: "postgres",
        values: {
            host: "db.internal.example.com",
            port: 5432,
            name: "Production DB",
            username: "app_user",
            password: "<<__SAM_REQUIRED__>>",
        },
    },
    null,
    2
);

const meta = {
    title: "Builder/ConnectorEditDialog",
    component: ConnectorEditDialog,
    parameters: {
        docs: {
            description: {
                component: "Modal opened from the builder save flow when a connector still has required credential placeholders. Reuses the ConnectorEdit form and highlights the unfilled fields.",
            },
        },
        msw: { handlers: connectorsListHandlers },
        chatContext: {
            sessionId: "mock-session-id",
            connectorSchemasMap: mockConnectorsSchemaMap,
            getConnectorSchema: mockGetConnectorSchema,
        },
    },
} satisfies Meta<typeof ConnectorEditDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        isOpen: true,
        connectorJson,
        onClose: fn(),
        onSave: fn(),
    },
    play: async () => {
        const dialog = within(await screen.findByRole("dialog"));
        expect(await dialog.findByText("PostgreSQL Connector")).toBeInTheDocument();
        expect(await dialog.findByText("Connector")).toBeInTheDocument();
    },
};
