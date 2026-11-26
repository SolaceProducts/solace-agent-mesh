// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { GatewaySidePanel } from "@/features/entrypoints/GatewaySidePanel";

import { mockGateways } from "@/testing/data";
import { toNetworkingDetailsV2 } from "@/testing/data/entrypointsV2";
import { withDefaults } from "@/testing/helpers";
import { DOC_LINKS } from "@/utils";

const meta = {
    title: "Entrypoints/GatewaySidePanel",
    component: GatewaySidePanel,
    parameters: {
        docs: {
            description: {
                component: "The main gateway side panel component that displays the gateway's details.",
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
} satisfies Meta<typeof GatewaySidePanel>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    args: {
        gateway: mockGateways[0],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByText("Slack Gateway - Deployed & In Sync")).toBeInTheDocument();

        await canvas.findByTestId("openGateway");
    },
};

export const DeployedInSync: Story = {
    args: {
        gateway: mockGateways[0],
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const gateway = args.gateway!;

        expect(canvas.getByTitle(gateway.name)).toBeInTheDocument();
        expect(canvas.getByText(gateway.description as string)).toBeInTheDocument();

        expect(canvas.getByText("Entrypoint Details")).toBeInTheDocument();

        expect(canvas.getByText("Status")).toBeInTheDocument();

        expect(canvas.getByTestId("openGateway")).toBeInTheDocument();

        if (gateway.id) {
            expect(canvas.getByText(gateway.id)).toBeInTheDocument();
        }
    },
};

export const DeployedOutOfSync: Story = {
    args: {
        gateway: mockGateways[1],
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const gateway = args.gateway!;

        expect(canvas.getByText(gateway.name)).toBeInTheDocument();

        const deployUpdateButtons = canvas.getByText("Preview and Deploy");
        expect(deployUpdateButtons).toBeInTheDocument();

        expect(canvas.getByTestId("openGateway")).toBeInTheDocument();
        expect(canvas.getByTestId("previewUpdates")).toBeInTheDocument();
    },
};

export const NotDeployed: Story = {
    args: {
        gateway: mockGateways[2],
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const gateway = args.gateway!;

        expect(canvas.getByText(gateway.name)).toBeInTheDocument();

        expect(canvas.getByText("Status")).toBeInTheDocument();

        expect(canvas.getByTestId("openGateway")).toBeInTheDocument();
        expect(canvas.getByTestId("deployGateway")).toBeInTheDocument();

        if (gateway.id) {
            expect(canvas.getByText(gateway.id)).toBeInTheDocument();
        }
    },
};

export const DeployedOutOfSyncAndFailed: Story = {
    args: {
        gateway: mockGateways[5],
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const gateway = args.gateway!;

        expect(canvas.getByText(gateway.name)).toBeInTheDocument();

        expect(canvas.getByText(/A previous deployment failed/)).toBeInTheDocument();
        expect(canvas.getByText(/undeployed changes/)).toBeInTheDocument();

        expect(canvas.getByTestId("openGateway")).toBeInTheDocument();
        expect(canvas.getByTestId("previewUpdates")).toBeInTheDocument();
        expect(canvas.queryByTestId("deleteGateway")).not.toBeInTheDocument();
    },
};

export const NotDeployedAndFailed: Story = {
    args: {
        gateway: mockGateways[6],
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const gateway = args.gateway!;

        expect(canvas.getByText(gateway.name)).toBeInTheDocument();

        expect(canvas.getByText(/A previous deployment failed/)).toBeInTheDocument();

        expect(canvas.getByTestId("openGateway")).toBeInTheDocument();
        expect(canvas.getByTestId("deployGateway")).toBeInTheDocument();
        expect(canvas.queryByTestId("deleteGateway")).not.toBeInTheDocument();
    },
};

export const StuckDisconnected: Story = {
    args: {
        gateway: mockGateways[8],
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const gateway = args.gateway!;

        expect(canvas.getByText(gateway.name)).toBeInTheDocument();
        expect(canvas.getByTestId("openGateway")).toBeInTheDocument();
        expect(canvas.getByTestId("deployGateway")).toBeInTheDocument();
        expect(canvas.queryByTestId("deleteGateway")).not.toBeInTheDocument();
        expect(canvas.queryByTestId("redeployGateway")).not.toBeInTheDocument();
    },
};

export const DisconnectedOutOfSync: Story = {
    args: {
        gateway: mockGateways[10],
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const gateway = args.gateway!;

        expect(canvas.getByText(gateway.name)).toBeInTheDocument();
        expect(canvas.getByText(/undeployed changes/)).toBeInTheDocument();
        expect(canvas.getByTestId("openGateway")).toBeInTheDocument();
        expect(canvas.getByTestId("previewUpdates")).toBeInTheDocument();
        expect(canvas.queryByTestId("deployGateway")).not.toBeInTheDocument();
        expect(canvas.queryByTestId("redeployGateway")).not.toBeInTheDocument();
    },
};

export const StuckDisconnectedOutOfSync: Story = {
    args: {
        gateway: mockGateways[10],
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findByTestId("messageBanner");
        expect(canvas.queryByTestId("deployGateway")).not.toBeInTheDocument();
        await canvas.findByTestId("previewUpdates");
        await canvas.findByTestId("openGateway");
    },
};

export const Undeployed: Story = {
    args: {
        gateway: mockGateways[13],
    },
    play: async ({ canvasElement, args }) => {
        const canvas = within(canvasElement);
        const gateway = args.gateway!;

        expect(canvas.getByText(gateway.name)).toBeInTheDocument();
        expect(canvas.getByTestId("openGateway")).toBeInTheDocument();
        expect(canvas.getByTestId("deployGateway")).toBeInTheDocument();
        expect(canvas.queryByTestId("messageBanner")).not.toBeInTheDocument();
    },
};

export const WhatsAppEndpointSection: Story = {
    args: {
        gateway: { ...mockGateways[2], id: "gateway-wa-1", name: "WhatsApp Entrypoint", type: "whatsapp" },
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/entrypoints/gateway-wa-1/networkingDetails", () =>
                    HttpResponse.json({
                        data: toNetworkingDetailsV2({
                            dnsName: "",
                            port: 0,
                            url: "https://wa.example.com/gw/gateway-wa-1",
                            webhookUrl: "https://wa.example.com/gw/gateway-wa-1/webhook",
                            apiPaths: [{ path: "/webhook", description: "WhatsApp Business Cloud API webhook endpoint" }],
                        }),
                    })
                )
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(canvas.getByText("WhatsApp Entrypoint")).toBeInTheDocument();

        expect(await canvas.findByText("Entrypoint Endpoint")).toBeInTheDocument();
        expect(canvas.getByText(/To complete setup, deploy the entrypoint to enable the webhook URL/i)).toBeInTheDocument();
        expect(await canvas.findByText("https://wa.example.com/gw/gateway-wa-1/webhook")).toBeInTheDocument();

        const docLink = canvas.getByRole("link", { name: /Learn about WhatsApp configuration/i });
        expect(docLink).toBeInTheDocument();
        expect(docLink).toHaveAttribute("target", "_blank");

        expect(canvas.queryByRole("link", { name: /Learn about Teams configuration/i })).not.toBeInTheDocument();
    },
};

export const WhatsAppEndpointSectionDeployed: Story = {
    args: {
        gateway: { ...mockGateways[0], id: "gateway-wa-2", name: "WhatsApp Entrypoint Deployed", type: "whatsapp" },
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/entrypoints/gateway-wa-2/networkingDetails", () =>
                    HttpResponse.json({
                        data: toNetworkingDetailsV2({
                            dnsName: "",
                            port: 0,
                            url: "https://wa.example.com/gw/gateway-wa-2",
                            webhookUrl: "https://wa.example.com/gw/gateway-wa-2/webhook",
                            apiPaths: [{ path: "/webhook", description: "WhatsApp Business Cloud API webhook endpoint" }],
                        }),
                    })
                )
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Entrypoint Endpoint")).toBeInTheDocument();
        expect(await canvas.findByText("https://wa.example.com/gw/gateway-wa-2/webhook")).toBeInTheDocument();
        expect(canvas.queryByText(/To complete setup, deploy the entrypoint/i)).not.toBeInTheDocument();
    },
};

export const TeamsEndpointSection: Story = {
    args: {
        gateway: { ...mockGateways[2], id: "gateway-teams-1", name: "Teams Entrypoint", type: "teams" },
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/entrypoints/gateway-teams-1/networkingDetails", () =>
                    HttpResponse.json({
                        data: toNetworkingDetailsV2({
                            dnsName: "",
                            port: 0,
                            url: "https://teams.example.com/gw/gateway-teams-1",
                            webhookUrl: "https://teams.example.com/gw/gateway-teams-1/api/messages",
                            apiPaths: [{ path: "/api/messages", description: "Bot Framework messaging endpoint" }],
                        }),
                    })
                )
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Entrypoint Endpoint")).toBeInTheDocument();
        expect(await canvas.findByText("https://teams.example.com/gw/gateway-teams-1/api/messages")).toBeInTheDocument();
        expect(canvas.getByRole("link", { name: /Learn about Teams configuration/i })).toBeInTheDocument();
        expect(canvas.queryByText(/paste it in Meta's WhatsApp configuration/i)).not.toBeInTheDocument();
    },
};

export const DisconnectedOutOfSyncNotTransitioning: Story = {
    args: {
        gateway: {
            ...mockGateways[10],
            deploymentStatus: "deployed",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const previewButton = await canvas.findByTestId("previewUpdates");

        expect(previewButton).toHaveTextContent("Preview and Deploy");

        expect(previewButton).not.toBeDisabled();

        expect(previewButton).toHaveAttribute("title", "Preview and Deploy");
    },
};

export const GoogleChatEndpointSection: Story = {
    args: {
        gateway: { ...mockGateways[0], id: "gateway-gc-1", name: "Google Chat Entrypoint", type: "google_chat" },
    },
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/entrypoints/gateway-gc-1/networkingDetails", () =>
                    HttpResponse.json({
                        data: toNetworkingDetailsV2({
                            dnsName: "",
                            port: 0,
                            url: "https://chat.example.com/gw/hr-assistant",
                            webhookUrl: "https://chat.example.com/gw/hr-assistant/events",
                            apiPaths: [{ path: "/events", description: "Google Chat HTTPS event endpoint" }],
                        }),
                    })
                )
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("https://chat.example.com/gw/hr-assistant/events")).toBeInTheDocument();
        expect(canvas.getByRole("link", { name: /Learn about Google Chat configuration/i })).toHaveAttribute("href", DOC_LINKS.buildingEntrypointsGoogleChat);
        expect(canvas.queryByRole("link", { name: /Learn about WhatsApp configuration/i })).not.toBeInTheDocument();
    },
};
