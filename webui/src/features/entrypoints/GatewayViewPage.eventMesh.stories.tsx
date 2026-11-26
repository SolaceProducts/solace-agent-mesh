// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, fn, userEvent, waitFor, within } from "storybook/test";

import { GatewayViewPage } from "@/features/entrypoints/GatewayViewPage";

import { mockGatewayDeployments, mockGatewaySchemas } from "@/testing/data";
import { mockEntrypointV2, toEntrypointDeploymentV2 } from "@/testing/data/entrypointsV2";
import { getPaginatedMeta, withDefaults } from "@/testing/helpers";

const handlers = withDefaults(
    http.get("*/api/v2/platform/entrypoints/gateway-6", () => HttpResponse.json({ data: mockEntrypointV2("gateway-6") })),
    http.get("*/api/v2/platform/entrypointDeployments", () => {
        return HttpResponse.json({
            data: mockGatewayDeployments.map(toEntrypointDeploymentV2),
            meta: getPaginatedMeta(mockGatewayDeployments.length),
        });
    })
);

const meta = {
    title: "Entrypoints/GatewayViewPage/Event Mesh",
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
            useLoaderData: () => ({ gatewayId: "gateway-6" }),
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

        const gatewayNameElements = await canvas.findAllByText("Event Mesh Gateway - Deployed & Out of Sync, Deploy Failed");
        await expect(gatewayNameElements[0]).toBeInTheDocument();

        const deployButton = canvas.getByTestId("deployGatewayButton");
        expect(deployButton).toBeInTheDocument();
        expect(deployButton).toHaveTextContent("Redeploy");
        expect(canvas.getByTestId("editGatewayButton")).toBeInTheDocument();

        expect(canvas.getByRole("tab", { name: "Configuration" })).toBeInTheDocument();
        expect(canvas.getByRole("tab", { name: "Deployment History" })).toBeInTheDocument();

        expect(canvas.getByText("Entrypoint Details")).toBeInTheDocument();
        expect(canvas.getByText("Event mesh gateway that failed to deploy for testing error states")).toBeInTheDocument();
    },
};

export const DeploymentTab: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const gatewayNameElements = await canvas.findAllByText("Event Mesh Gateway - Deployed & Out of Sync, Deploy Failed");
        await expect(gatewayNameElements[0]).toBeInTheDocument();

        const deploymentTab = await canvas.findByRole("tab", { name: /Deployment/i });
        await userEvent.click(deploymentTab);

        await waitFor(() => {
            expect(canvasElement.querySelectorAll('[data-slot="table-row"]')).toHaveLength(mockGatewayDeployments.length + 1);
        });
    },
};

export const DisconnectedDeploy: Story = {
    parameters: {
        ...meta.parameters,
        msw: {
            handlers: withDefaults(http.get("*/api/v2/platform/entrypoints/gateway-9", () => HttpResponse.json({ data: mockEntrypointV2("gateway-9") }))),
        },
        chatContext: {
            ...meta.parameters.chatContext,
            useLoaderData: () => ({ gatewayId: "gateway-9" }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const deployButton = await canvas.findByTestId("deployGatewayButton");
        expect(deployButton).toHaveTextContent("Deploy");
    },
};

export const NotDeployed: Story = {
    parameters: {
        ...meta.parameters,
        msw: {
            handlers: withDefaults(http.get("*/api/v2/platform/entrypoints/gateway-3", () => HttpResponse.json({ data: mockEntrypointV2("gateway-3") }))),
        },
        chatContext: {
            ...meta.parameters.chatContext,
            useLoaderData: () => ({ gatewayId: "gateway-3" }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const deployButton = await canvas.findByTestId("deployGatewayButton");
        expect(deployButton).toHaveTextContent("Deploy");
        expect(deployButton).toBeEnabled();
        const statusElements = await canvas.findAllByText(/Not Deployed/);
        expect(statusElements.length).toBeGreaterThan(0);

        expect(await canvas.findByTestId("moreActionsButton")).toBeInTheDocument();
    },
};

const deleteEntrypointSpy = fn();
const deleteNavigateSpy = fn();

export const DeleteEntrypointConfirmation: Story = {
    parameters: {
        ...meta.parameters,
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/entrypoints/gateway-14", () => HttpResponse.json({ data: mockEntrypointV2("gateway-14") })),
                http.delete("*/api/v2/platform/entrypoints/gateway-14", () => {
                    deleteEntrypointSpy();
                    return new HttpResponse(null, { status: 204 });
                })
            ),
        },
        chatContext: {
            ...meta.parameters.chatContext,
            useLoaderData: () => ({ gatewayId: "gateway-14" }),
            navigate: deleteNavigateSpy,
            addNotification: () => {},
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const body = within(document.body);

        await userEvent.click(await canvas.findByTestId("moreActionsButton"));
        await userEvent.click(await body.findByRole("menuitem", { name: "Delete" }));

        const dialog = within(await body.findByRole("dialog"));
        await dialog.findByText(/delete the entrypoint/i);
        await userEvent.click(dialog.getByRole("button", { name: "Delete" }));

        await waitFor(() => expect(deleteEntrypointSpy).toHaveBeenCalled());
        await waitFor(() => expect(deleteNavigateSpy).toHaveBeenCalledWith("/entrypoints"));
    },
};

export const DeployedActive: Story = {
    parameters: {
        ...meta.parameters,
        msw: {
            handlers: withDefaults(http.get("*/api/v2/platform/entrypoints/gateway-1", () => HttpResponse.json({ data: mockEntrypointV2("gateway-1") }))),
        },
        chatContext: {
            ...meta.parameters.chatContext,
            useLoaderData: () => ({ gatewayId: "gateway-1" }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const deployButton = await canvas.findByTestId("deployGatewayButton");
        expect(deployButton).toHaveTextContent("Undeploy");
        expect(deployButton).toBeEnabled();
        const deployedElements = await canvas.findAllByText(/Deployed/);
        expect(deployedElements.length).toBeGreaterThan(0);

        expect(canvas.queryByTestId("moreActionsButton")).not.toBeInTheDocument();
    },
};

export const DeployFailed: Story = {
    parameters: {
        ...meta.parameters,
        msw: {
            handlers: withDefaults(http.get("*/api/v2/platform/entrypoints/gateway-7", () => HttpResponse.json({ data: mockEntrypointV2("gateway-7") }))),
        },
        chatContext: {
            ...meta.parameters.chatContext,
            useLoaderData: () => ({ gatewayId: "gateway-7" }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const deployButton = await canvas.findByTestId("deployGatewayButton");
        expect(deployButton).toHaveTextContent("Redeploy");
        expect(deployButton).toBeEnabled();
        const failedElements = await canvas.findAllByText(/Deployment Failed/);
        expect(failedElements.length).toBeGreaterThan(0);
    },
};

export const DisconnectedOutOfSync: Story = {
    parameters: {
        ...meta.parameters,
        msw: {
            handlers: withDefaults(http.get("*/api/v2/platform/entrypoints/gateway-11", () => HttpResponse.json({ data: mockEntrypointV2("gateway-11") }))),
        },
        chatContext: {
            ...meta.parameters.chatContext,
            useLoaderData: () => ({ gatewayId: "gateway-11" }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const deployButton = await canvas.findByTestId("deployGatewayButton");
        expect(deployButton).toHaveTextContent("Deploy Updates");
        const deployedElements = await canvas.findAllByText(/Deployed/);
        expect(deployedElements.length).toBeGreaterThan(0);
    },
};

export const UndeployedView: Story = {
    parameters: {
        ...meta.parameters,
        msw: {
            handlers: withDefaults(http.get("*/api/v2/platform/entrypoints/gateway-14", () => HttpResponse.json({ data: mockEntrypointV2("gateway-14") }))),
        },
        chatContext: {
            ...meta.parameters.chatContext,
            useLoaderData: () => ({ gatewayId: "gateway-14" }),
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const deployButton = await canvas.findByTestId("deployGatewayButton");
        expect(deployButton).toHaveTextContent("Deploy");
        expect(deployButton).toBeEnabled();
        const statusElements = await canvas.findAllByText(/Not Deployed/);
        expect(statusElements.length).toBeGreaterThan(0);
    },
};
