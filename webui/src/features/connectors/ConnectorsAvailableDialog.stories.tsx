// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, screen, userEvent, within } from "storybook/test";

import { ConnectorsAvailableDialog } from "@/features/connectors/ConnectorsAvailableDialog";

import { mockConnectorSchemas } from "@/testing/data";

const meta = {
    title: "Connectors/ConnectorsAvailableDialog",
    component: ConnectorsAvailableDialog,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "The dialog for the creation of connectors.",
            },
        },
    },
    decorators: [
        Story => (
            <div className="flex">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof ConnectorsAvailableDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            connectorSchemas: mockConnectorSchemas,
            handleCreateConnector: fn(),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement, parameters }) => {
        const handleCreateConnector = parameters.chatContext.handleCreateConnector;
        const canvas = within(canvasElement);
        await userEvent.click(await canvas.findByTestId("createConnector"));

        const dialogWithin = within(await screen.findByRole("dialog"));
        await dialogWithin.findByText("Select Connector to Create");

        const appsCard = await dialogWithin.findByTestId("connector-type-sql-postgres");
        expect(within(appsCard).getByText("PostgreSQL")).toBeInTheDocument();
        expect(within(appsCard).getByText("SQL Database")).toBeInTheDocument();

        appsCard.focus();
        await userEvent.keyboard("{Enter}");
        expect(handleCreateConnector).toHaveBeenCalledWith(expect.objectContaining({ type: "sql", subtype: "postgres" }));

        await userEvent.click(dialogWithin.getByRole("tab", { name: /custom/i }));

        const customCard = await dialogWithin.findByTestId("connector-type-api-openapi");
        expect(within(customCard).getByText("OpenAPI")).toBeInTheDocument();
        expect(within(customCard).getByText("Allow agents to connect to any existing API with a spec.")).toBeInTheDocument();

        await userEvent.click(customCard);
        expect(handleCreateConnector).toHaveBeenCalledWith(expect.objectContaining({ type: "api", subtype: "openapi" }));
    },
};

export const Empty: Story = {
    parameters: {
        chatContext: {
            sessionId: "mock-session-id",
            connectorSchemas: [],
            handleCreateConnector: fn(),
        },
        configContext: {
            persistenceEnabled: true,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await userEvent.click(await canvas.findByTestId("createConnector"));

        const dialogWithin = within(await screen.findByRole("dialog"));
        await dialogWithin.findByText("Select Connector to Create");
        await dialogWithin.findByText("No App Connector Schemas Found");

        await userEvent.click(dialogWithin.getByRole("tab", { name: /custom/i }));
        await dialogWithin.findByText("No Custom Connector Schemas Found");
    },
};
