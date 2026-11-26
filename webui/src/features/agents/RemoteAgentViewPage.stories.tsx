// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, fn, userEvent, waitFor, within } from "storybook/test";

import { RemoteAgentViewPage } from "@/features/agents/RemoteAgentViewPage";

import { mockRemoteAgents, mockRemoteAgentsWithDuplicates } from "@/testing/data";
import { remoteAgentToV2 } from "@/testing/data/platformWireV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";
import type { RemoteAgent } from "@/types";

const createHandlers = (agentId: string, remoteAgentsList: RemoteAgent[] = mockRemoteAgents) =>
    withDefaults(
        http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json(paginatedResponse(remoteAgentsList.map(remoteAgentToV2)))),
        http.get(`*/api/v2/platform/remoteAgents/${agentId}`, () => {
            const agent = remoteAgentsList.find(a => a.id === agentId);
            return HttpResponse.json({ data: agent && remoteAgentToV2(agent) });
        })
    );

const meta = {
    title: "Agents/RemoteAgentViewPage",
    component: RemoteAgentViewPage,
} satisfies Meta<typeof RemoteAgentViewPage>;

export default meta;
type Story = StoryObj<typeof meta>;

export const ViewNotDeployed: Story = {
    parameters: {
        msw: { handlers: createHandlers(mockRemoteAgents[3].id) },
        chatContext: {
            useLoaderData: () => ({ remoteAgentId: mockRemoteAgents[3].id }),
            navigate: () => {},
            handleDeleteAgent: () => {},
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const deployButton = await canvas.findByTestId("deployRemoteAgentButton");
        expect(deployButton).toHaveTextContent("Deploy");
        expect(deployButton).toBeEnabled();

        await canvas.findByTestId("editRemoteAgentButton");
        await canvas.findByTestId("moreActionsButton");
    },
};

export const ViewDeployedActive: Story = {
    parameters: {
        msw: { handlers: createHandlers(mockRemoteAgents[0].id) },
        chatContext: {
            useLoaderData: () => ({ remoteAgentId: mockRemoteAgents[0].id }),
            navigate: () => {},
            handleDeleteAgent: () => {},
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const undeployButton = await canvas.findByTestId("undeployRemoteAgentButton");
        expect(undeployButton).toHaveTextContent("Undeploy");
        expect(undeployButton).toBeEnabled();

        await canvas.findByTestId("editRemoteAgentButton");
        expect(canvas.queryByTestId("deployRemoteAgentButton")).toBeNull();
        expect(canvas.queryByTestId("moreActionsButton")).toBeNull();
    },
};

export const ViewDeploy: Story = {
    parameters: {
        msw: { handlers: createHandlers(mockRemoteAgents[3].id) },
        chatContext: {
            useLoaderData: () => ({ remoteAgentId: mockRemoteAgents[3].id }),
            navigate: () => {},
            handleDeleteAgent: () => {},
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const deployButton = await canvas.findByTestId("deployRemoteAgentButton");
        expect(deployButton).toHaveTextContent("Deploy");
        expect(deployButton).toBeEnabled();
        await userEvent.click(deployButton);
    },
};

export const ViewOutOfSyncDeploy: Story = {
    parameters: {
        msw: { handlers: createHandlers(mockRemoteAgents[10].id) },
        chatContext: {
            useLoaderData: () => ({ remoteAgentId: mockRemoteAgents[10].id }),
            navigate: () => {},
            handleDeleteAgent: () => {},
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const deployButton = await canvas.findByTestId("deployRemoteAgentButton");
        expect(deployButton).toHaveTextContent("Deploy Updates");
        expect(deployButton).toBeEnabled();
        await userEvent.click(deployButton);
    },
};

export const ViewDeployedOutOfSync: Story = {
    parameters: {
        msw: { handlers: createHandlers(mockRemoteAgents[10].id) },
        chatContext: {
            useLoaderData: () => ({ remoteAgentId: mockRemoteAgents[10].id }),
            navigate: () => {},
            handleDeleteAgent: () => {},
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const deployButton = await canvas.findByTestId("deployRemoteAgentButton");
        expect(deployButton).toHaveTextContent("Deploy Updates");
        expect(deployButton).toBeEnabled();

        await canvas.findByText("There are undeployed changes for this agent. Please review and deploy these updates.");
        expect(canvas.queryByTestId("moreActionsButton")).toBeNull();
    },
};

export const ViewDeployFailed: Story = {
    parameters: {
        msw: { handlers: createHandlers(mockRemoteAgents[6].id) },
        chatContext: {
            useLoaderData: () => ({ remoteAgentId: mockRemoteAgents[6].id }),
            navigate: () => {},
            handleDeleteAgent: () => {},
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const deployButton = await canvas.findByTestId("deployRemoteAgentButton");
        expect(deployButton).toHaveTextContent("Redeploy");
        expect(deployButton).toBeEnabled();

        await canvas.findByTestId("moreActionsButton");
    },
};

export const ViewDeployFailedOutOfSync: Story = {
    parameters: {
        msw: { handlers: createHandlers(mockRemoteAgents[13].id) },
        chatContext: {
            useLoaderData: () => ({ remoteAgentId: mockRemoteAgents[13].id }),
            navigate: () => {},
            handleDeleteAgent: () => {},
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const deployButton = await canvas.findByTestId("deployRemoteAgentButton");
        expect(deployButton).toHaveTextContent("Redeploy");
        expect(deployButton).toBeEnabled();

        await canvas.findByTestId("moreActionsButton");

        await canvas.findByText("There are undeployed changes for this agent. Please review and deploy these updates.");
    },
};

export const DuplicateNames: Story = {
    parameters: {
        msw: { handlers: createHandlers(mockRemoteAgents[11].id, mockRemoteAgentsWithDuplicates) },
        chatContext: {
            userAgentCards: [],
            yamlAgentCards: [],
            useLoaderData: () => ({ remoteAgentId: mockRemoteAgents[11].id }),
            navigate: () => {},
            handleDeleteAgent: () => {},
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const undeployButton = await canvas.findByTestId("undeployRemoteAgentButton");
        expect(undeployButton).toHaveTextContent("Undeploy");
        expect(undeployButton).toBeEnabled();

        await canvas.findByTestId("editRemoteAgentButton");
        expect(canvas.queryByTestId("moreActionsButton")).toBeNull();

        await canvas.findByTestId("duplicate-name-warning-banner");
    },
};

const remoteDeleteAgentSpy = fn(async () => true);

export const DeleteAgentConfirmation: Story = {
    parameters: {
        msw: { handlers: createHandlers(mockRemoteAgents[6].id) },
        chatContext: {
            useLoaderData: () => ({ remoteAgentId: mockRemoteAgents[6].id }),
            navigate: () => {},
            handleDeleteAgent: remoteDeleteAgentSpy,
        },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);
        const body = within(document.body);

        await userEvent.click(await canvas.findByTestId("moreActionsButton"));
        await userEvent.click(await body.findByRole("menuitem", { name: "Delete" }));

        const dialog = within(await body.findByRole("dialog"));
        await dialog.findByText(/session history/i);

        const confirmButton = dialog.getByRole("button", { name: "Delete" });
        expect(confirmButton).toBeDisabled();
        await userEvent.type(dialog.getByRole("textbox"), "DELETE");
        await waitFor(() => expect(confirmButton).toBeEnabled());

        await userEvent.click(confirmButton);
        await waitFor(() => expect(remoteDeleteAgentSpy).toHaveBeenCalled());
        await waitFor(() => expect(body.queryByRole("dialog")).not.toBeInTheDocument());
    },
};
