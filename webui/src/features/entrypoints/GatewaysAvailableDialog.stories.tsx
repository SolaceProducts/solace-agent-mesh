// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { screen, within } from "storybook/test";

import { GatewaysAvailableDialog } from "@/features/entrypoints/GatewaysAvailableDialog";

import { mockGatewaySchemas } from "@/testing/data";

const meta = {
    title: "Entrypoints/GatewaysAvailableDialog",
    component: GatewaysAvailableDialog,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "The dialog for the creation of gateways.",
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
} satisfies Meta<typeof GatewaysAvailableDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        chatContext: {
            gatewaySchemas: mockGatewaySchemas,
            navigate: (path: string) => {
                alert(`Navigate to: ${path}`);
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        await canvas.findByText("Create Entrypoint");
    },
};

export const OpenDialog: Story = {
    parameters: {
        chatContext: {
            gatewaySchemas: mockGatewaySchemas,
            navigate: (path: string) => {
                alert(`Navigate to: ${path}`);
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Create Entrypoint").then(button => button.click());

        const dialog = await screen.findByRole("dialog");
        const dialogWithin = within(dialog);
        await dialogWithin.findByText("Available Entrypoint Types");
        await dialogWithin.findByText("Slack");
        await dialogWithin.findByText("Event Mesh");
        await dialogWithin.findByText("WhatsApp");
    },
};

export const EmptySchemas: Story = {
    parameters: {
        chatContext: {
            gatewaySchemas: [],
            navigate: (path: string) => {
                alert(`Navigate to: ${path}`);
            },
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByText("Create Entrypoint").then(button => button.click());

        const dialog = await screen.findByRole("dialog");
        const dialogWithin = within(dialog);
        await dialogWithin.findByText("Available Entrypoint Types");
        await dialogWithin.findByText("No entrypoint types available");
    },
};
