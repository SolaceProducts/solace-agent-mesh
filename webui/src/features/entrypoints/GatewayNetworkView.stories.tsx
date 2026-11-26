// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, userEvent, within } from "storybook/test";

import { GatewayNetworkView } from "@/features/entrypoints/GatewayNetworkView";

const meta = {
    title: "Entrypoints/GatewayNetworkView",
    component: GatewayNetworkView,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Network configuration view for Teams gateways. Displays the auto-derived Entrypoint Endpoint (read-only) and the gateway's API paths.",
            },
        },
        chatContext: {
            addNotification: (message: string, type?: "success" | "info") => {
                console.log(`Notification (${type || "info"}): ${message}`);
            },
        },
    },
    args: {
        addNotification: fn(),
        gatewayType: "teams",
    },
} satisfies Meta<typeof GatewayNetworkView>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Loading: Story = {
    args: {
        networkingInfo: {
            networkingDetails: null,
            isLoading: true,
            error: null,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Loading...")).toBeInTheDocument();
    },
};

export const Error: Story = {
    args: {
        networkingInfo: {
            networkingDetails: null,
            isLoading: false,
            error: "Failed to load networking details",
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const errorBanner = await canvas.findByTestId("messageBanner");
        expect(errorBanner).toBeInTheDocument();
        expect(canvas.getByText("Failed to load networking details")).toBeInTheDocument();
    },
};

export const MessagingEndpoint: Story = {
    args: {
        networkingInfo: {
            networkingDetails: {
                dnsName: "teams-gateway.example.com",
                port: 3978,
                url: "https://teams-gateway.example.com:3978",
                publicUrl: null,
                apiPaths: [
                    { path: "/api/messages", description: "Bot Framework messaging endpoint" },
                    { path: "/health", description: "Health check endpoint" },
                ],
            },
            isLoading: false,
            error: null,
        },
    },
    play: async ({ args, canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Network Details")).toBeInTheDocument();

        expect(canvas.getByText("Entrypoint Endpoint")).toBeInTheDocument();
        const messagingEndpoints = canvas.getAllByText("https://teams-gateway.example.com:3978/api/messages");
        expect(messagingEndpoints.length).toBeGreaterThan(0);

        expect(canvas.queryByRole("button", { name: /Edit/i })).not.toBeInTheDocument();

        expect(canvas.getByText("API Paths")).toBeInTheDocument();
        expect(canvas.getByText("/api/messages")).toBeInTheDocument();
        expect(canvas.getByText("/health")).toBeInTheDocument();
        expect(canvas.getByText("Bot Framework messaging endpoint")).toBeInTheDocument();

        const docLink = canvas.getByRole("link", { name: /Learn about Teams configuration/i });
        expect(docLink).toBeInTheDocument();
        expect(docLink).toHaveAttribute("target", "_blank");

        Object.defineProperty(navigator, "clipboard", {
            value: { writeText: fn(() => Promise.resolve()) },
            writable: true,
            configurable: true,
        });
        await userEvent.click(canvas.getAllByRole("button", { name: /Copy URL/i })[0]);
        expect(args.addNotification).toHaveBeenCalledWith("Entrypoint endpoint copied to clipboard", "success");
    },
};

export const WhatsAppWebhook: Story = {
    args: {
        gatewayType: "whatsapp",
        networkingInfo: {
            networkingDetails: {
                dnsName: "wa-gateway.example.com",
                port: 443,
                url: "https://wa-gateway.example.com",
                publicUrl: null,
                apiPaths: [{ path: "/webhook", description: "WhatsApp Business Cloud API webhook endpoint" }],
            },
            isLoading: false,
            error: null,
        },
    },
    play: async ({ args, canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Network Details")).toBeInTheDocument();

        expect(canvas.getByText("Entrypoint Endpoint")).toBeInTheDocument();
        const webhookEndpoints = canvas.getAllByText("https://wa-gateway.example.com/webhook");
        expect(webhookEndpoints.length).toBeGreaterThan(0);

        const docLink = canvas.getByRole("link", { name: /Learn more about WhatsApp configuration/i });
        expect(docLink).toHaveAttribute("target", "_blank");
        expect(canvas.queryByRole("link", { name: /Learn about Teams configuration/i })).not.toBeInTheDocument();
        expect(canvas.queryByText(/Meta app's WhatsApp webhook settings/i)).not.toBeInTheDocument();

        expect(canvas.getByText("API Paths")).toBeInTheDocument();
        expect(canvas.getByText("/webhook")).toBeInTheDocument();

        Object.defineProperty(navigator, "clipboard", {
            value: { writeText: fn(() => Promise.resolve()) },
            writable: true,
            configurable: true,
        });
        await userEvent.click(canvas.getAllByRole("button", { name: /Copy URL/i })[0]);
        expect(args.addNotification).toHaveBeenCalledWith("Entrypoint endpoint copied to clipboard", "success");
    },
};

export const WhatsAppWebhookFromBackendUrl: Story = {
    args: {
        gatewayType: "whatsapp",
        networkingInfo: {
            networkingDetails: {
                dnsName: "wa-gateway.example.com",
                port: 443,
                url: "https://wa-gateway.example.com/gw/wa-1",
                webhookUrl: "https://wa-gateway.example.com/gw/wa-1/webhook",
                publicUrl: null,
                apiPaths: [
                    { path: "/health", description: "Health check endpoint" },
                    { path: "/webhook", description: "WhatsApp Business Cloud API webhook endpoint" },
                ],
            },
            isLoading: false,
            error: null,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText("Network Details")).toBeInTheDocument();

        expect(canvas.getAllByText("https://wa-gateway.example.com/gw/wa-1/webhook")).toHaveLength(2);
        expect(canvas.getAllByText("https://wa-gateway.example.com/gw/wa-1/health")).toHaveLength(1);
    },
};

export const NoNetworkingInfo: Story = {
    args: {
        networkingInfo: {
            networkingDetails: null,
            isLoading: false,
            error: null,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(canvas.queryByText("Network Details")).not.toBeInTheDocument();
    },
};
