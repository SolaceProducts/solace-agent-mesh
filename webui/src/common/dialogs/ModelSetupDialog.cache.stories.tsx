// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { expect, screen, userEvent, waitFor, within } from "storybook/test";

import { toSupportedModelV2 } from "@/testing/data/modelsV2";
import { getPaginatedMeta, withDefaults } from "@/testing/helpers";

import { ModelSetupDialog } from "./ModelSetupDialog";

const supportedModels = [{ id: "claude-sonnet-4-6", label: "claude-sonnet-4-6" }];

let probeCount = 0;

const emptyThenPopulated = withDefaults(
    http.put("*/api/v2/platform/providers/:provider/models", ({ params }) => {
        probeCount += 1;
        const data = probeCount === 1 ? [] : supportedModels.map(toSupportedModelV2(String(params.provider)));
        return HttpResponse.json({ data, meta: getPaginatedMeta(data.length) });
    })
);

const meta = {
    title: "Common/ModelSetupDialog Cache",
    component: ModelSetupDialog,
    parameters: {
        docs: {
            description: {
                component: "Regression guard: an empty catalogue must not be replayed from cache after the user edits a connection field and reverts it.",
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

export const RevertedEditReProbes: Story = {
    args: { open: true, onOpenChange: () => {}, hasWritePermissions: true },
    parameters: { msw: { handlers: emptyThenPopulated } },
    play: async () => {
        probeCount = 0;

        const body = within(document.body);
        await body.findByText("Configure Your AI Models");

        await userEvent.click((await body.findAllByRole("combobox"))[0]);
        await userEvent.click(await body.findByText("Custom"));
        await userEvent.click((await body.findAllByRole("combobox"))[1]);
        await userEvent.click(await body.findByRole("option", { name: /API Key/ }));

        const apiKey = document.querySelector('input[name="apiKey"]') as HTMLInputElement;
        await userEvent.type(apiKey, "sk-placeholder-not-a-real-key");
        const apiBase = document.querySelector('input[name="apiBase"]') as HTMLInputElement;
        const originalBase = "https://litellm.workshop.example.com";
        await userEvent.type(apiBase, originalBase);

        const picker = await body.findByPlaceholderText(/Type or select a model/i);

        await userEvent.click(picker);
        const emptyState = await screen.findByText("No models found");
        await waitFor(() => expect(emptyState).toBeVisible());
        await expect(probeCount).toBe(1);

        await userEvent.click(body.getByText("Configure Your AI Models"));

        await userEvent.type(apiBase, "/v1");
        await userEvent.clear(apiBase);
        await userEvent.type(apiBase, originalBase);

        await userEvent.click(picker);

        const freshModel = await screen.findByText("claude-sonnet-4-6");
        await waitFor(() => expect(freshModel).toBeVisible());
        await expect(probeCount).toBeGreaterThan(1);
    },
};
