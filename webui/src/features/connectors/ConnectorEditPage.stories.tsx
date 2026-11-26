// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { ConnectorEditPage } from "@/features/connectors/ConnectorEditPage";

import { mockConnectors, mockConnectorsSchemaMap, mockGetConnectorSchema } from "@/testing/data";
import { connectorToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

const connectorsListHandlers = withDefaults(http.get("*/api/v2/platform/connectors", () => HttpResponse.json(paginatedResponse(mockConnectors.map(connectorToV2)))));

const meta = {
    title: "Connectors/ConnectorEditPage",
    component: ConnectorEditPage,
    parameters: {
        docs: {
            description: {
                component: "The connector edit page component that displays the connector details and handles user edits.",
            },
        },
        msw: { handlers: connectorsListHandlers },
    },
} satisfies Meta<typeof ConnectorEditPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            connectorSchemasMap: mockConnectorsSchemaMap,
            getConnectorSchema: mockGetConnectorSchema,
            useLoaderData: () => ({ isNew: true }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByRole("button", { name: "Go To Connectors" });
    },
};

export const CreateMYSQL: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            connectorSchemasMap: mockConnectorsSchemaMap,
            getConnectorSchema: mockGetConnectorSchema,
            useLoaderData: () => ({ type: "sql", subtype: "mysql", isNew: true }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByTestId("saveConnectorButton");
        await canvas.findByTestId("connectorCancel");
    },
};

export const EditPOSTGRESQL: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            connectorSchemasMap: mockConnectorsSchemaMap,
            getConnectorSchema: mockGetConnectorSchema,
            connectorToEdit: mockConnectors[0],
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByTestId("saveConnectorButton");
        await canvas.findByTestId("connectorCancel");
    },
};

export const EditPOSTGRESQLValidation: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            connectorSchemasMap: mockConnectorsSchemaMap,
            getConnectorSchema: mockGetConnectorSchema,
            connectorToEdit: { ...mockConnectors[0], name: "P" },
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.queryByText("Name must be at least 3 characters")).toBeNull();
        await canvas.findByTestId("saveConnectorButton").then(button => button.click());

        expect(await canvas.findByText("Name must be at least 3 characters")).toBeInTheDocument();
    },
};

export const CreateSlackValidationEmptySubmit: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            connectorSchemasMap: mockConnectorsSchemaMap,
            getConnectorSchema: mockGetConnectorSchema,
            useLoaderData: () => ({ type: "slack", subtype: "bot", isNew: true }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("saveConnectorButton");

        await canvas.findByTestId("saveConnectorButton").then(button => button.click());

        expect(await canvas.findByText("Name must be at least 3 characters")).toBeInTheDocument();
        expect(await canvas.findByText("Value must be at least 10 characters")).toBeInTheDocument();

        expect(await canvas.findByText("Minimum length is 10 characters")).toBeInTheDocument();
    },
};

export const CreateAPIConnector: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            connectorSchemasMap: mockConnectorsSchemaMap,
            getConnectorSchema: mockGetConnectorSchema,
            useLoaderData: () => ({ type: "api", subtype: "openapi", isNew: true }),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("OpenAPI Specification")).toBeInTheDocument();

        const fileInput = canvasElement.querySelector('input[type="file"]');
        expect(fileInput).toBeInTheDocument();
    },
};
