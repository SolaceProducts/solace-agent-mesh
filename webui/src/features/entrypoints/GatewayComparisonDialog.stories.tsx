// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, screen, within } from "storybook/test";

import { GatewayComparisonDialog } from "@/features/entrypoints/GatewayComparisonDialog";

import { mockGateways, mockGatewaySchemas } from "@/testing/data";

const meta = {
    title: "Entrypoints/GatewayComparisonDialog",
    component: GatewayComparisonDialog,
    parameters: {
        docs: {
            description: {
                component: "A dialog component that displays a side-by-side comparison of deployed vs draft gateway configurations.",
            },
        },
    },
    args: {
        onClose: fn(),
    },
} satisfies Meta<typeof GatewayComparisonDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const SlackGatewayComparison: Story = {
    args: {
        open: true,
        deployedGateway: mockGateways[4].deployed,
        draftGateway: mockGateways[4],
        schemaInfo: mockGatewaySchemas.find(s => s.type === "slack")!,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");

        await expect(dialog).toBeInTheDocument();

        expect(within(dialog).getAllByText("Deployed")).toHaveLength(2);
        expect(within(dialog).getAllByText("Not Deployed")).toHaveLength(2);

        expect(within(dialog).getByTestId("closeButton")).toBeInTheDocument();
        expect(within(dialog).getByTestId("openGateway")).toBeInTheDocument();
        expect(within(dialog).getByTestId("deployUpdates")).toBeInTheDocument();

        expect(await within(dialog).findAllByText("Bot Token")).toHaveLength(2);
        expect(await within(dialog).findAllByText("App-Level Token")).toHaveLength(2);
    },
};

export const EventMeshGatewayComparison: Story = {
    args: {
        open: true,
        deployedGateway: {
            name: mockGateways[5].deployed?.name || mockGateways[5].name,
            description: mockGateways[5].deployed?.description || mockGateways[5].description,
            type: mockGateways[5].type,
            values: mockGateways[5].deployed?.values || {},
        },
        draftGateway: mockGateways[5],
        schemaInfo: mockGatewaySchemas.find(s => s.type === "event_mesh")!,
    },
    play: async () => {
        const dialog = await screen.findByRole("dialog");

        expect(dialog).toBeInTheDocument();

        expect(await within(dialog).findAllByText("Data Plane Broker URL")).toHaveLength(2);
        expect(await within(dialog).findAllByText("VPN Name")).toHaveLength(2);
        expect(await within(dialog).findAllByText("Event Rules")).toHaveLength(2);
    },
};
