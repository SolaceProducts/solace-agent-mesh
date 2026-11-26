// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { delay, http, HttpResponse } from "msw";
import { expect, screen, userEvent, within } from "storybook/test";

import { toSupportedModelV2 } from "@/testing/data/modelsV2";
import { getPaginatedMeta, withDefaults } from "@/testing/helpers";

import { ModelSetupDialog } from "./ModelSetupDialog";

const supportedModels = [
    { id: "claude-sonnet-4-6", label: "claude-sonnet-4-6" },
    { id: "azure-gpt-4o", label: "azure-gpt-4o" },
];

const CATALOGUE_DELAY_MS = 150;

const slowCatalogue = withDefaults(
    http.put("*/api/v2/platform/providers/:provider/models", async ({ params }) => {
        await delay(CATALOGUE_DELAY_MS);
        return HttpResponse.json({ data: supportedModels.map(toSupportedModelV2(String(params.provider))), meta: getPaginatedMeta(supportedModels.length) });
    })
);

const meta = {
    title: "Common/ModelSetupDialog Picker",
    component: ModelSetupDialog,
    parameters: {
        docs: {
            description: {
                component: "Regression guard for the first-run picker: a catalogue that takes a moment to arrive must still render into an open popover, on the first click.",
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

export const PickerSurvivesSlowCatalogue: Story = {
    args: { open: true, onOpenChange: () => {}, hasWritePermissions: true },
    parameters: { msw: { handlers: slowCatalogue } },
    play: async () => {
        const body = within(document.body);
        await body.findByText("Configure Your AI Models");

        await userEvent.click((await body.findAllByRole("combobox"))[0]);
        await userEvent.click(await body.findByText("OpenAI"));

        const apiKey = document.querySelector('input[name="apiKey"]') as HTMLInputElement;
        await userEvent.type(apiKey, "sk-placeholder-not-a-real-key");

        const picker = await body.findByPlaceholderText(/Type or select a model/i);

        await userEvent.click(picker);

        const option = await screen.findByText("claude-sonnet-4-6");
        await expect(option).toBeVisible();
        await expect(picker).toHaveAttribute("aria-expanded", "true");

        await userEvent.click(option);
        await expect(picker).toHaveValue("claude-sonnet-4-6");
        await expect(picker).toHaveAttribute("aria-expanded", "false");
    },
};
