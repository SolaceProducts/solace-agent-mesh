// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, within } from "storybook/test";

import { GatewaysPage } from "@/features/entrypoints/GatewaysPage";

import { mockGateways } from "@/testing/data";
import { toEntrypointV2 } from "@/testing/data/entrypointsV2";
import { getPaginatedMeta, withDefaults } from "@/testing/helpers";

const gatewaysListHandlers = withDefaults(http.get("*/api/v2/platform/entrypoints", () => HttpResponse.json({ data: mockGateways.map(toEntrypointV2), meta: getPaginatedMeta(mockGateways.length) })));

const openMoreMenuForGateway = async (canvas: ReturnType<typeof within>, gatewayId: string) => {
    const nameCell = await canvas.findByTestId(`gateway-name-${gatewayId}`);
    const row = nameCell.closest("tr");
    if (!row) throw new Error(`row for ${gatewayId} not found`);
    const moreButton = within(row as HTMLElement).getByTestId("More");
    await userEvent.click(moreButton);
    return screen.findByRole("menu");
};

const meta = {
    title: "Entrypoints/GatewaysPage",
    component: GatewaysPage,
    parameters: {
        docs: {
            description: {
                component: "The gateways page that displays the list of gateways.",
            },
        },
    },
} satisfies Meta<typeof GatewaysPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    parameters: {
        msw: { handlers: gatewaysListHandlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        for (const gateway of mockGateways.slice(0, 10)) {
            expect(await canvas.findByTestId(`gateway-name-${gateway.id}`)).toBeInTheDocument();
        }
    },
};

export const Empty: Story = {
    parameters: {},
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        expect(await canvas.findByText("Connect external systems to your agent mesh")).toBeInTheDocument();
        expect(await canvas.findByTestId("createGateway")).toBeInTheDocument();
    },
};

export const FilteredToOne: Story = {
    parameters: {
        msw: { handlers: gatewaysListHandlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const searchInput = await canvas.findByTestId("gatewaySearchInput");
        await userEvent.type(searchInput, "Teams");

        expect(await canvas.findByText("Teams Gateway - Deployed & Out of Sync")).toBeInTheDocument();

        expect(canvas.queryByText(/Slack/i)).not.toBeInTheDocument();
        expect(canvas.queryByText(/Event Mesh/i)).not.toBeInTheDocument();
    },
};

export const FilteredToNone: Story = {
    parameters: {
        msw: { handlers: gatewaysListHandlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const searchInput = await canvas.findByTestId("gatewaySearchInput");
        await userEvent.type(searchInput, "Cheese");

        expect(await canvas.findByText("Try adjusting your search terms")).toBeInTheDocument();

        expect(await canvas.findByRole("button", { name: "Clear Filter" })).toBeInTheDocument();
    },
};

export const MenuDeployedOutOfSync: Story = {
    parameters: {
        msw: { handlers: gatewaysListHandlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const menu = await openMoreMenuForGateway(canvas, "gateway-2");
        expect(within(menu).getByText("Open Entrypoint")).toBeInTheDocument();
        expect(within(menu).getByText("Edit")).toBeInTheDocument();
        expect(within(menu).getByText("Deploy Updates")).toBeInTheDocument();
        expect(within(menu).getByText("Undeploy")).toBeInTheDocument();
        expect(within(menu).getByText("Delete")).toBeInTheDocument();

        expect(within(menu).queryByText("Deploy")).not.toBeInTheDocument();
    },
};

export const MenuNotDeployed: Story = {
    parameters: {
        msw: { handlers: gatewaysListHandlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const menu = await openMoreMenuForGateway(canvas, "gateway-3");
        expect(within(menu).getByText("Open Entrypoint")).toBeInTheDocument();
        expect(within(menu).getByText("Edit")).toBeInTheDocument();
        expect(within(menu).getByText("Deploy")).toBeInTheDocument();
        expect(within(menu).getByText("Delete")).toBeInTheDocument();

        expect(within(menu).queryByText("Deploy Updates")).not.toBeInTheDocument();
        expect(within(menu).queryByText("Undeploy")).not.toBeInTheDocument();
    },
};

export const MenuDeployedInSync: Story = {
    parameters: {
        msw: { handlers: gatewaysListHandlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const menu = await openMoreMenuForGateway(canvas, "gateway-1");
        expect(within(menu).getByText("Open Entrypoint")).toBeInTheDocument();
        expect(within(menu).getByText("Edit")).toBeInTheDocument();
        expect(within(menu).getByText("Undeploy")).toBeInTheDocument();
        expect(within(menu).getByText("Delete")).toBeInTheDocument();

        expect(within(menu).queryByText("Deploy")).not.toBeInTheDocument();
        expect(within(menu).queryByText("Deploy Updates")).not.toBeInTheDocument();
    },
};

export const DeleteDialogNotDeployed: Story = {
    parameters: {
        msw: { handlers: gatewaysListHandlers },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const menu = await openMoreMenuForGateway(canvas, "gateway-3");
        within(menu).getByText("Delete").click();

        expect(await screen.findByTestId("gatewayDeleteMessage")).toBeInTheDocument();
    },
};

export const DeleteDialogDeployed: Story = {
    parameters: {
        msw: { handlers: gatewaysListHandlers },
        userContext: {
            userCapabilities: ["createGateways", "deployGateways"],
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const menu = await openMoreMenuForGateway(canvas, "gateway-1");
        within(menu).getByText("Delete").click();

        expect(await screen.findByTestId("gatewayDeleteMessage")).toBeInTheDocument();
    },
};

export const MenuStuckDisconnected: Story = {
    parameters: {
        msw: { handlers: gatewaysListHandlers },
        userContext: {
            userCapabilities: ["createGateways", "deployGateways"],
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const menu = await openMoreMenuForGateway(canvas, "gateway-9");
        expect(within(menu).getByText("Deploy")).toBeInTheDocument();
        expect(within(menu).getByText("Delete")).toBeInTheDocument();
        expect(within(menu).queryByText("Redeploy")).not.toBeInTheDocument();
        expect(within(menu).queryByText("Undeploy")).not.toBeInTheDocument();
        expect(within(menu).queryByText("Deploy Updates")).not.toBeInTheDocument();
    },
};

export const MenuDisconnectedOutOfSync: Story = {
    parameters: {
        msw: { handlers: gatewaysListHandlers },
        userContext: {
            userCapabilities: ["createGateways", "deployGateways"],
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const menu = await openMoreMenuForGateway(canvas, "gateway-11");
        expect(within(menu).getByText("Deploy Updates")).toBeInTheDocument();
        expect(within(menu).getByText("Delete")).toBeInTheDocument();

        expect(within(menu).queryByText("Redeploy")).not.toBeInTheDocument();
    },
};
