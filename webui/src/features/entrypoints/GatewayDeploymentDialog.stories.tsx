// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, fn, screen, userEvent, waitFor, within } from "storybook/test";

import { mockGateways } from "@/testing/data";
import { DOC_LINKS } from "@/utils";

import { GatewayDeploymentDialog } from "./GatewayDeploymentDialog";

const meta = {
    title: "Entrypoints/GatewayDeploymentDialog",
    component: GatewayDeploymentDialog,
    parameters: {
        docs: {
            description: {
                component: "Confirmation dialog for gateway deployment actions (deploy, undeploy, update).",
            },
        },
    },
    args: {
        onConfirm: fn(),
        onClose: fn(),
    },
} satisfies Meta<typeof GatewayDeploymentDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const DeployTeamsGateway: Story = {
    args: {
        open: true,
        gatewayName: "Teams Gateway",
        gateway: mockGateways.find(g => g.type === "teams")!,
        action: "deploy",
    },
    play: async () => {
        await waitFor(async () => {
            const dialog = await screen.findByRole("dialog");
            expect(dialog).toBeInTheDocument();
        });

        expect(screen.getByText("Deploy Teams Gateway")).toBeInTheDocument();

        expect(screen.getByText(/Deployment takes approximately 5 minutes/i)).toBeInTheDocument();
        expect(screen.getByText(/After deployment, navigate to the entrypoint details to copy the entrypoint endpoint and configure it in Microsoft Teams/i)).toBeInTheDocument();

        expect(screen.getByRole("link", { name: /Learn more/i })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Deploy" })).toBeInTheDocument();
    },
};

export const DeployWhatsAppGateway: Story = {
    args: {
        open: true,
        gatewayName: "WhatsApp Entrypoint",
        gateway: { ...mockGateways[2], type: "whatsapp" },
        action: "deploy",
    },
    play: async () => {
        await waitFor(async () => {
            const dialog = await screen.findByRole("dialog");
            expect(dialog).toBeInTheDocument();
        });

        expect(screen.getByText("Deploy WhatsApp Entrypoint")).toBeInTheDocument();

        expect(screen.getByText(/the webhook URL must be pointed to Meta to enable external access/i)).toBeInTheDocument();
        expect(screen.getByRole("link", { name: /Learn about WhatsApp configuration/i })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Deploy" })).toBeInTheDocument();
    },
};

export const DeployGoogleChatGateway: Story = {
    args: {
        open: true,
        gatewayName: "HR Assistant",
        gateway: { ...mockGateways[0], type: "google_chat", slug: "google-chat", deploymentStatus: "not_deployed", values: { endpoint_url: "https://host.com/gw/google-chat/events" } },
        action: "deploy",
    },
    play: async ({ args }) => {
        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).getByText("Deploy HR Assistant")).toBeInTheDocument();
        expect(within(dialog).getByText("Ensure the webhook URL is configured in Google Chat before deploying.")).toBeInTheDocument();
        expect(within(dialog).getByText("https://host.com/gw/google-chat/events")).toBeInTheDocument();
        expect(within(dialog).getByText("The entrypoint cannot receive messages until the webhook URL is configured in Google Chat.")).toBeInTheDocument();
        expect(within(dialog).getByRole("link", { name: /Learn about Google Chat configuration/ })).toHaveAttribute("href", DOC_LINKS.buildingEntrypointsGoogleChat);
        expect(within(dialog).queryByText(/refresh your entrypoints/)).not.toBeInTheDocument();
        expect(within(dialog).getByRole("button", { name: "Deploy" })).toBeInTheDocument();

        await userEvent.click(within(dialog).getByRole("button", { name: "Close" }));
        expect(args.onClose).toHaveBeenCalledTimes(1);
    },
};

export const DeploySlackGateway: Story = {
    args: {
        open: true,
        gatewayName: "Slack Gateway",
        gateway: mockGateways.find(g => g.type === "slack")!,
        action: "deploy",
    },
    play: async () => {
        await waitFor(async () => {
            const dialog = await screen.findByRole("dialog");
            expect(dialog).toBeInTheDocument();
        });

        expect(screen.getByText("Deploy Slack Gateway")).toBeInTheDocument();
        expect(screen.getByText(/Are you ready to deploy/i)).toBeInTheDocument();
        expect(screen.queryByText(/setup dialog/i)).not.toBeInTheDocument();
    },
};

export const UndeployWithChanges: Story = {
    args: {
        open: true,
        gatewayName: "Test Gateway",
        gateway: { ...mockGateways[1], syncStatus: "out_of_sync" },
        action: "undeploy",
    },
    play: async () => {
        await waitFor(async () => {
            const dialog = await screen.findByRole("dialog");
            expect(dialog).toBeInTheDocument();
        });

        expect(screen.getByText("Undeploy Test Gateway")).toBeInTheDocument();
        expect(screen.getByText(/Any deployed configuration will be lost/i)).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Undeploy" })).toBeInTheDocument();
    },
};

export const UndeployInSync: Story = {
    args: {
        open: true,
        gatewayName: "Test Gateway",
        gateway: { ...mockGateways[1], syncStatus: "in_sync" },
        action: "undeploy",
    },
    play: async () => {
        await waitFor(async () => {
            const dialog = await screen.findByRole("dialog");
            expect(dialog).toBeInTheDocument();
        });

        expect(screen.getByText("Undeploy Test Gateway")).toBeInTheDocument();
        expect(screen.queryByText(/Any deployed configuration will be lost/i)).not.toBeInTheDocument();
    },
};

export const DeployUpdates: Story = {
    args: {
        open: true,
        gatewayName: "Test Gateway",
        gateway: mockGateways[1],
        action: "update",
    },
    play: async () => {
        await waitFor(async () => {
            const dialog = await screen.findByRole("dialog");
            expect(dialog).toBeInTheDocument();
        });

        expect(screen.getByText("Deploy Updates for Test Gateway")).toBeInTheDocument();
        expect(screen.getByText(/update the running entrypoint with the latest changes/i)).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Deploy" })).toBeInTheDocument();
    },
};

export const ConfirmButtonClick: Story = {
    args: {
        open: true,
        gatewayName: "Test Gateway",
        gateway: mockGateways[1],
        action: "deploy",
    },
    play: async ({ args }) => {
        await waitFor(async () => {
            const dialog = await screen.findByRole("dialog");
            expect(dialog).toBeInTheDocument();
        });

        const deployButton = screen.getByRole("button", { name: "Deploy" });
        await userEvent.click(deployButton);

        expect(args.onConfirm).toHaveBeenCalledTimes(1);
    },
};

export const CancelButtonClick: Story = {
    args: {
        open: true,
        gatewayName: "Test Gateway",
        gateway: mockGateways[1],
        action: "deploy",
    },
    play: async ({ args }) => {
        await waitFor(async () => {
            const dialog = await screen.findByRole("dialog");
            expect(dialog).toBeInTheDocument();
        });

        const cancelButton = screen.getByRole("button", { name: "Cancel" });
        await userEvent.click(cancelButton);

        expect(args.onClose).toHaveBeenCalledTimes(1);
    },
};

export const RedeployStuckGateway: Story = {
    args: {
        open: true,
        gatewayName: "Stuck Gateway",
        gateway: { ...mockGateways[6], deploymentStatus: "deploy_failed", syncStatus: "out_of_sync" },
        action: "update",
    },
    play: async () => {
        await waitFor(async () => {
            const dialog = await screen.findByRole("dialog");
            expect(dialog).toBeInTheDocument();
        });

        expect(screen.getByText(/Redeploy Stuck Gateway|Deploy Updates for Stuck Gateway/i)).toBeInTheDocument();
    },
};
