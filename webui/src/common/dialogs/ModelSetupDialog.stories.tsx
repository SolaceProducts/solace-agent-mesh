// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { expect, userEvent, within } from "storybook/test";

import { toSupportedModelV2 } from "@/testing/data/modelsV2";
import { getPaginatedMeta, withDefaults } from "@/testing/helpers";

import { ModelSetupDialog } from "./ModelSetupDialog";

const supportedModels = [
    { id: "gpt-4o", label: "gpt-4o" },
    { id: "gpt-4o-mini", label: "gpt-4o-mini" },
];

const listAndProviderHandlers = withDefaults(
    http.put("*/api/v2/platform/providers/:provider/models", ({ params }) => HttpResponse.json({ data: supportedModels.map(toSupportedModelV2(String(params.provider))), meta: getPaginatedMeta(supportedModels.length) }))
);

const meta = {
    title: "Common/ModelSetupDialog",
    component: ModelSetupDialog,
    parameters: {
        docs: {
            description: {
                component:
                    "First-time setup dialog shown when no default LLM models are configured. Admins get a single-step form that configures the general and planning defaults from one model + connection (validated before saving); non-admins get a read-only message. The full fill → validate → save flow is covered in ModelSetupForm stories (the modal wrapper poisons a second ComboBox-driving story per file).",
            },
        },
    },
    decorators: [
        Story => {
            const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
            return (
                <QueryClientProvider client={client}>
                    <Story />
                </QueryClientProvider>
            );
        },
    ],
} satisfies Meta<typeof ModelSetupDialog>;

export default meta;
type Story = StoryObj<typeof meta>;

export const AdminVariant: Story = {
    args: {
        open: true,
        onOpenChange: () => {},
        hasWritePermissions: true,
    },
    parameters: { msw: { handlers: listAndProviderHandlers } },
    play: async () => {
        const dialog = await within(document.body).findByRole("dialog");
        const content = within(dialog);

        expect(content.getByText("Configure Your AI Models")).toBeInTheDocument();
        expect(await content.findByText("Model Provider")).toBeInTheDocument();
        expect(content.getByRole("button", { name: /Set Up Default Models/i })).toBeInTheDocument();
        expect(content.getByRole("button", { name: /^Cancel$/i })).toBeInTheDocument();

        expect(content.queryByText("Default Model")).not.toBeInTheDocument();
        await userEvent.click(content.getAllByRole("combobox")[0]);
        await userEvent.click(await within(document.body).findByText("OpenAI"));
        expect(await content.findByText("Default Model")).toBeInTheDocument();

        const form = dialog.querySelector("form");
        expect(form).not.toBeNull();
        expect(getComputedStyle(form!).overflowY).toBe("auto");
        expect(dialog.getBoundingClientRect().height).toBeLessThanOrEqual(window.innerHeight * 0.8 + 1);
    },
};

export const NonAdminVariant: Story = {
    args: {
        open: true,
        onOpenChange: () => {},
        hasWritePermissions: false,
    },
    play: async () => {
        const dialog = await within(document.body).findByRole("dialog");
        const content = within(dialog);

        expect(content.getByText("No Default LLM Models Available")).toBeInTheDocument();
        expect(content.getByText(/Contact an administrator/)).toBeInTheDocument();
        expect(content.getByRole("button", { name: /^Close$/i })).toBeInTheDocument();

        expect(content.queryByRole("button", { name: /Set Up Default Models/i })).not.toBeInTheDocument();
        expect(content.queryByText("Default Model")).not.toBeInTheDocument();

        const header = dialog.querySelector('[data-slot="dialog-header"]');
        expect(header).not.toBeNull();
        expect(getComputedStyle(header!).overflowY).toBe("auto");
        expect(dialog.getBoundingClientRect().height).toBeLessThanOrEqual(window.innerHeight * 0.8 + 1);
    },
};
