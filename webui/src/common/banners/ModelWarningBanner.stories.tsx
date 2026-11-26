// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { http, HttpResponse } from "msw";
import { expect, within } from "storybook/test";

import { withDefaults } from "@/testing/helpers";

import { ModelWarningBanner } from "./ModelWarningBanner";

const meta = {
    title: "Common/ModelWarningBanner",
    component: ModelWarningBanner,
    decorators: [
        Story => (
            <div className="max-w-2xl">
                <Story />
            </div>
        ),
    ],
    parameters: {
        layout: "padded",
        docs: {
            description: {
                component: "Inline banner warning that no default LLM models are configured. Shows a 'Set Up Default Models' action for admins and an 'Ask your administrator' hint for everyone else.",
            },
        },
        msw: {
            handlers: withDefaults(
                http.get("*/api/v2/platform/modelStatus", () => {
                    return HttpResponse.json({ data: { configured: false } });
                })
            ),
        },
    },
} satisfies Meta<typeof ModelWarningBanner>;

export default meta;
type Story = StoryObj<typeof meta>;

export const Admin: Story = {
    parameters: {
        chatContext: { hasModelConfigWrite: true },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText(/No default model has been set up/)).toBeInTheDocument();
        expect(canvas.getByRole("button", { name: /Set Up Default Models/i })).toBeInTheDocument();
        expect(canvas.queryByText(/Ask your administrator/)).not.toBeInTheDocument();
    },
};

export const NonAdmin: Story = {
    parameters: {
        chatContext: { hasModelConfigWrite: false },
    },
    play: async ({ canvasElement }) => {
        const canvas = within(canvasElement);

        expect(await canvas.findByText(/No default model has been set up/)).toBeInTheDocument();
        expect(canvas.getByText(/Ask your administrator/)).toBeInTheDocument();
        expect(canvas.queryByRole("button", { name: /Set Up Default Models/i })).not.toBeInTheDocument();
    },
};
