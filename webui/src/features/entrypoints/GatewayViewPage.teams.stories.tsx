// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, userEvent, within } from "storybook/test";

import { GatewayViewPage } from "@/features/entrypoints/GatewayViewPage";

import { mockGatewaySchemas } from "@/testing/data";
import { mockEntrypointV2, toNetworkingDetailsV2 } from "@/testing/data/entrypointsV2";
import { withDefaults } from "@/testing/helpers";
import { DOC_LINKS } from "@/utils";

const handlers = withDefaults(
    http.get("*/api/v2/platform/entrypoints/gateway-2", () => HttpResponse.json({ data: mockEntrypointV2("gateway-2") })),
    http.get("*/api/v2/platform/entrypointDeployments", () => {
        return HttpResponse.json({
            data: [],
            meta: {
                pagination: {
                    pageNumber: 0,
                    count: 0,
                    pageSize: 0,
                    nextPage: null,
                    totalPages: 0,
                },
            },
        });
    })
);

const meta = {
    title: "Entrypoints/GatewayViewPage/Teams",
    component: GatewayViewPage,
    parameters: {
        docs: {
            description: {
                component: "The gateway view page component that displays detailed information about a single Teams gateway with tabs for configuration and deployment.",
            },
        },
        msw: {
            handlers: handlers,
        },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-2" }),
            gatewaySchemas: mockGatewaySchemas,
            navigate: (path: string) => {
                alert(`Navigate to: ${path}`);
            },
            addNotification: (message: string) => {
                alert(`Notification: ${message}`);
            },
            gatewayDeployError: null,
            setGatewayDeployError: () => {},
            deployGateway: async () => {},
        },
    },
} satisfies Meta<typeof GatewayViewPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const gatewayNameElements = await canvas.findAllByText("Teams Gateway - Deployed & Out of Sync");
        expect(gatewayNameElements[0]).toBeInTheDocument();

        expect(canvas.getByText("Entrypoints")).toBeInTheDocument();

        expect(canvas.getByTestId("deployGatewayButton")).toBeInTheDocument();
        expect(canvas.getByTestId("editGatewayButton")).toBeInTheDocument();

        expect(canvas.getByRole("tab", { name: "Configuration" })).toBeInTheDocument();
        expect(canvas.getByRole("tab", { name: "Network" })).toBeInTheDocument();
        expect(canvas.getByRole("tab", { name: "Deployment History" })).toBeInTheDocument();

        expect(canvas.getByText("Entrypoint Details")).toBeInTheDocument();
        expect(canvas.getByText("Microsoft Teams integration gateway for enterprise communication")).toBeInTheDocument();
    },
};

export const NetworkingDetailsError: Story = {
    parameters: {
        ...meta.parameters,
        msw: {
            handlers: [
                http.get("*/api/v2/platform/entrypoints/gateway-2/networkingDetails", () => {
                    return HttpResponse.json({ message: "Internal server error" }, { status: 500 });
                }),
                ...handlers,
            ],
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Teams Gateway - Deployed & Out of Sync");

        const networkTab = canvas.getByRole("tab", { name: "Network" });
        await userEvent.click(networkTab);

        expect(await canvas.findByText("Network Details")).toBeInTheDocument();
        expect(await canvas.findByTestId("messageBanner")).toBeInTheDocument();
    },
};

export const WithNetworkingDetails: Story = {
    parameters: {
        ...meta.parameters,
        msw: {
            handlers: [
                http.get("*/api/v2/platform/entrypoints/gateway-2/networkingDetails", () => {
                    return HttpResponse.json({
                        data: toNetworkingDetailsV2({
                            dnsName: "teams-gateway.example.com",
                            port: 3978,
                            url: "https://teams-gateway.example.com:3978",
                            apiPaths: [
                                { path: "/api/messages", description: "Bot Framework messaging endpoint" },
                                { path: "/health", description: "Health check endpoint" },
                            ],
                        }),
                    });
                }),
                ...handlers,
            ],
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Teams Gateway - Deployed & Out of Sync");

        const networkTab = canvas.getByRole("tab", { name: "Network" });
        await userEvent.click(networkTab);

        expect(await canvas.findByText("Network Details")).toBeInTheDocument();

        expect(await canvas.findByText("Entrypoint Endpoint")).toBeInTheDocument();
        const messagingEndpoints = await canvas.findAllByText("https://teams-gateway.example.com:3978/api/messages");
        expect(messagingEndpoints.length).toBeGreaterThan(0);
        expect(messagingEndpoints[0]).toBeInTheDocument();

        expect(canvas.getByRole("button", { name: /Copy URL/i })).toBeInTheDocument();

        const docLink = canvas.getByRole("link", { name: /Learn about Teams configuration/i });
        expect(docLink).toBeInTheDocument();
        expect(docLink).toHaveAttribute("href", DOC_LINKS.buildingEntrypointsTeams);

        expect(await canvas.findByText("API Paths")).toBeInTheDocument();
        expect(await canvas.findByText("/api/messages")).toBeInTheDocument();
        expect(await canvas.findByText("/health")).toBeInTheDocument();
    },
};

export const ConfigurationTabNoNetworking: Story = {
    parameters: {
        ...meta.parameters,
        msw: {
            handlers: [
                http.get("*/api/v2/platform/entrypoints/gateway-2/networkingDetails", () => {
                    return HttpResponse.json({
                        data: toNetworkingDetailsV2({
                            dnsName: "teams-gateway.example.com",
                            port: 3978,
                            url: "https://teams-gateway.example.com:3978",
                            apiPaths: [],
                        }),
                    });
                }),
                ...handlers,
            ],
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Teams Gateway - Deployed & Out of Sync");

        expect(await canvas.findByText("Entrypoint Details")).toBeInTheDocument();

        expect(canvas.queryByText("Network Details")).not.toBeInTheDocument();
        expect(canvas.queryByText("Networking Details")).not.toBeInTheDocument();
        expect(canvas.queryByText("Entrypoint Endpoint")).not.toBeInTheDocument();
    },
};
