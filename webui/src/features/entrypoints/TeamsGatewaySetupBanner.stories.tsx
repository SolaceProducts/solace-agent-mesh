// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { expect, within } from "storybook/test";

import { DOC_LINKS } from "@/utils";

import { TeamsGatewaySetupBanner } from "./TeamsGatewaySetupBanner";

const meta = {
    title: "Entrypoints/TeamsGatewaySetupBanner",
    component: TeamsGatewaySetupBanner,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Informational banner displayed during Teams Gateway creation in cloud environments. Informs users about post-creation setup requirements.",
            },
        },
    },
    decorators: [
        Story => (
            <div className="w-200">
                <Story />
            </div>
        ),
    ],
} satisfies Meta<typeof TeamsGatewaySetupBanner>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Default: Story = {
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        const banner = canvas.getByTestId("teams-gateway-setup-banner");
        expect(banner).toBeInTheDocument();

        expect(canvas.getByText(/To complete your setup, configure your integration in Microsoft Teams/i)).toBeInTheDocument();
        expect(canvas.getByText(/An entrypoint endpoint will be generated after creation./i)).toBeInTheDocument();

        const docLink = canvas.getByRole("link", { name: /Learn more/i });
        expect(docLink).toBeInTheDocument();
        expect(docLink).toHaveAttribute("href", DOC_LINKS.buildingEntrypointsTeams);
        expect(docLink).toHaveAttribute("target", "_blank");
        expect(docLink).toHaveAttribute("rel", "noopener noreferrer");
    },
};

export const WithCustomClassName: Story = {
    args: {
        className: "mt-4",
    },
};
