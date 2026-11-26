// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta } from "@storybook/react-vite";
import { expect, screen, within } from "storybook/test";

import { PageLabel } from "./PageLabel";

const meta = {
    title: "Common/Layout/PageLabel",
    component: PageLabel,
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Standard page label. Renders required indicator (asterisk) when `required` is set.",
            },
        },
    },
} satisfies Meta<typeof PageLabel>;

export default meta;

export const Basic = {
    render: () => (
        <div className="space-y-6">
            <div>
                <h3 className="mb-3 text-sm font-semibold text-(--secondary-text-wMain)">Optional Label</h3>
                <PageLabel>Username</PageLabel>
            </div>
            <div>
                <h3 className="mb-3 text-sm font-semibold text-(--secondary-text-wMain)">Required Label (with asterisk)</h3>
                <PageLabel required>Password</PageLabel>
            </div>
        </div>
    ),
    play: async () => {
        await expect(screen.getByText("Username")).toBeInTheDocument();
        const requiredLabel = screen.getByText("Password");
        const labelContainer = requiredLabel.closest("div");
        const asterisk = within(labelContainer!).getByText("*");
        await expect(asterisk).toBeInTheDocument();
    },
};
