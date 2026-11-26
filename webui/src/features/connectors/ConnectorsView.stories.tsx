// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { ConnectorsView } from "@/features/connectors/ConnectorsView";

import { mockConnectors, mockGetConnectorSchema } from "@/testing/data";
import { connectorToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

const meta = {
    title: "Connectors/ConnectorsView",
    component: ConnectorsView,
    parameters: {
        docs: {
            description: {
                component: "The connectors view page component that displays the list of connectors.",
            },
        },
    },
} satisfies Meta<typeof ConnectorsView>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        chatContext: {
            handleViewConnector: () => {},
            getConnectorSchema: mockGetConnectorSchema,
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

export const Empty: Story = {
    parameters: {
        chatContext: {
            handleViewConnector: () => {},
            getConnectorSchema: mockGetConnectorSchema,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Connectors extend your agents with enterprise integrations and real-time data access.")).toBeInTheDocument();
        expect(await canvas.findByRole("button", { name: "See Available Connectors" })).toBeInTheDocument();
    },
};
