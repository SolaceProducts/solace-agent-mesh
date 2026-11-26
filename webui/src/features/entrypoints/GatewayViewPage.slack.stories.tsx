// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, userEvent, within } from "storybook/test";

import { GatewayViewPage } from "@/features/entrypoints/GatewayViewPage";

import { mockGateways, mockGatewaySchemas } from "@/testing/data";
import { mockEntrypointV2, toEntrypointV2 } from "@/testing/data/entrypointsV2";
import { withDefaults } from "@/testing/helpers";

const handlers = withDefaults(
    http.get("*/api/v2/platform/entrypoints/gateway-1", () => HttpResponse.json({ data: mockEntrypointV2("gateway-1") })),
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
    title: "Entrypoints/GatewayViewPage/Slack",
    component: GatewayViewPage,
    parameters: {
        docs: {
            description: {
                component: "The gateway view page component that displays detailed information about a single gateway with tabs for configuration and deployment.",
            },
        },
        msw: {
            handlers: handlers,
        },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-1" }),
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

        const gatewayNameElements = await canvas.findAllByText("Slack Gateway - Deployed & In Sync");
        expect(gatewayNameElements[0]).toBeInTheDocument();

        expect(canvas.getByText("Entrypoints")).toBeInTheDocument();

        expect(canvas.getByTestId("deployGatewayButton")).toBeInTheDocument();
        expect(canvas.getByTestId("editGatewayButton")).toBeInTheDocument();

        expect(canvas.getByRole("tab", { name: "Configuration" })).toBeInTheDocument();
        expect(canvas.getByRole("tab", { name: "Deployment History" })).toBeInTheDocument();

        expect(canvas.getByText("Entrypoint Details")).toBeInTheDocument();
        expect(canvas.getByText("Gateway for connecting Slack workspace to the agent mesh")).toBeInTheDocument();
    },
};

export const DeploymentTab: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const gatewayNameElements = await canvas.findAllByText("Slack Gateway - Deployed & In Sync");
        expect(gatewayNameElements[0]).toBeInTheDocument();

        const deploymentTab = await canvas.findByRole("tab", { name: /Deployment/i });
        await userEvent.click(deploymentTab);

        expect(await canvas.findByText("No deployment history available")).toBeInTheDocument();
    },
};

export const HeaderButtonUndeployWhenRunning: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/entrypoints/gateway-1", () => {
                    return HttpResponse.json({
                        data: toEntrypointV2({
                            ...mockGateways[0],
                            runtimeStatus: { status: "running" },
                        }),
                    });
                }),
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
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Slack Gateway - Deployed & In Sync");

        const deployButton = await canvas.findByTestId("deployGatewayButton");

        expect(deployButton).toHaveTextContent("Undeploy");

        expect(deployButton).toHaveAttribute("title", "Undeploy");

        expect(deployButton).not.toBeDisabled();
    },
};

export const HeaderButtonDeployUpdatesWhenOutOfSync: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/entrypoints/gateway-1", () => {
                    return HttpResponse.json({
                        data: toEntrypointV2({
                            ...mockGateways[0],
                            syncStatus: "out_of_sync",
                        }),
                    });
                }),
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
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Slack Gateway - Deployed & In Sync");

        const deployButton = await canvas.findByTestId("deployGatewayButton");

        expect(deployButton).toHaveTextContent("Deploy Updates");
        expect(deployButton).toHaveAttribute("title", "Deploy Updates");
    },
};

export const HeaderButtonRedeployWhenFailed: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/entrypoints/gateway-1", () => {
                    return HttpResponse.json({
                        data: toEntrypointV2({
                            ...mockGateways[0],
                            deploymentStatus: "deploy_failed",
                        }),
                    });
                }),
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
            ),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Slack Gateway - Deployed & In Sync");

        const deployButton = await canvas.findByTestId("deployGatewayButton");

        expect(deployButton).toHaveTextContent("Redeploy");
        expect(deployButton).toHaveAttribute("title", "Redeploy");
    },
};

export const HeaderButtonDeployWhenNotDeployed: Story = {
    parameters: {
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/entrypoints/gateway-3", () => {
                    return HttpResponse.json({ data: toEntrypointV2(mockGateways[2]) });
                }),
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
            ),
        },
        chatContext: {
            useLoaderData: () => ({ gatewayId: "gateway-3" }),
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
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        await canvas.findAllByText("Event Mesh Gateway - Not Deployed");

        const deployButton = await canvas.findByTestId("deployGatewayButton");

        expect(deployButton).toHaveTextContent("Deploy");
        expect(deployButton).toHaveAttribute("title", "Deploy");
    },
};
