// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { GatewayTypeChip } from "./GatewayTypeChip";

const meta = {
    title: "Entrypoints/GatewayTypeChip",
    component: GatewayTypeChip,
    parameters: {
        docs: {
            description: {
                component: "Badge showing a gateway type's icon and label, used in the gateway list and detail views.",
            },
        },
    },
} satisfies Meta<typeof GatewayTypeChip>;

export default meta;
type Story = StoryObj<typeof meta>;

const ALL_TYPES = ["rest", "slack", "teams", "event_mesh", "email", "whatsapp", "google_chat", "mcp", "unrecognized"];

export const AllTypes: Story = {
    args: { type: "mcp" },
    render: () => (
        <div className="flex flex-col items-start gap-3 p-4">
            {ALL_TYPES.map(type => (
                <GatewayTypeChip key={type} type={type} size="20" />
            ))}
        </div>
    ),
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("MCP")).toBeInTheDocument();
    },
};

export const Mcp: Story = {
    args: { type: "mcp", size: "20" },
};
