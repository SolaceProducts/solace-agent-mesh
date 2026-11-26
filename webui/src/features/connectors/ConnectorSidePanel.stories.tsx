// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { ConnectorSidePanel } from "@/features/connectors/ConnectorSidePanel";

import { mockConnectors, mockConnectorsSchemaMap, mockGetConnectorSchema } from "@/testing/data";

const meta = {
    title: "Connectors/ConnectorSidePanel",
    component: ConnectorSidePanel,
    parameters: {
        docs: {
            description: {
                component: "The main connector side panel component that displays the connector's details.",
            },
        },
    },
    decorators: [
        Story => (
            <div className="ml-auto h-screen w-2/5 border-l">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof ConnectorSidePanel>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        connector: mockConnectors[0],
    },
    parameters: {
        chatContext: {
            connectorSchemasMap: mockConnectorsSchemaMap,
            getConnectorSchema: mockGetConnectorSchema,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByTestId("openConnectorButton");
    },
};

export const KnowledgeBase: Story = {
    args: {
        connector: mockConnectors[3],
    },
    parameters: {
        chatContext: {
            connectorSchemasMap: mockConnectorsSchemaMap,
            getConnectorSchema: mockGetConnectorSchema,
        },
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const connector = args.connector!;

        await expect(canvas.getByTitle(connector.name)).toBeInTheDocument();
        await expect(canvas.getByText(connector.description as string)).toBeInTheDocument();
        await expect(canvas.getByText("Amazon Bedrock")).toBeInTheDocument();

        await expect(canvas.getByText("Knowledge Base ID")).toBeInTheDocument();
        await expect(canvas.getByText(connector.values.kb_id as string)).toBeInTheDocument();

        await expect(canvas.getByText("AWS Region")).toBeInTheDocument();
        await expect(canvas.getByText(connector.values.region as string)).toBeInTheDocument();

        await expect(canvas.getByText("Authentication Scheme")).toBeInTheDocument();
        const schema = mockGetConnectorSchema(connector.type, connector.subtype);
        const authTypeField = schema?.fieldSchema?.auth_type;
        const authTypeValue = connector.values.auth_type as string;
        const authTypeOption = authTypeField?.options?.find(opt => opt.value === authTypeValue);
        const authTypeLabel = authTypeOption?.label || authTypeValue;

        const authTypeLabelElements = canvas.getAllByText(authTypeLabel);
        expect(authTypeLabelElements.length).toBeGreaterThanOrEqual(1);

        await expect(canvas.getByText(connector.values.aws_access_key_id as string)).toBeInTheDocument();

        await expect(canvas.getByText("AWS Secret Key")).toBeInTheDocument();
        await expect(canvas.getByText("••••••••")).toBeInTheDocument();

        await expect(canvas.getByText("Agent Usage")).toBeInTheDocument();
        await expect(canvas.getByText(connector.agentCount as number)).toBeInTheDocument();

        await expect(canvas.getByText(connector.id)).toBeInTheDocument();
    },
};
