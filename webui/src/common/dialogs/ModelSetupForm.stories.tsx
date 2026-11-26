// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Meta, StoryObj } from "@storybook/react-vite";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { http, HttpResponse } from "msw";
import { expect, fn, userEvent, waitFor, within } from "storybook/test";

import { savedModelV2, toSupportedModelV2 } from "@/testing/data/modelsV2";
import { paginatedResponse, withDefaults } from "@/testing/helpers";

import { ModelSetupForm } from "./ModelSetupForm";

const supportedModels = [
    { id: "gpt-4o", label: "gpt-4o" },
    { id: "gpt-4o-mini", label: "gpt-4o-mini" },
];

const baseHandlers = [
    http.get("*/api/v2/platform/models", () => HttpResponse.json(paginatedResponse([]))),
    http.put("*/api/v2/platform/providers/:provider/models", ({ params }) => HttpResponse.json(paginatedResponse(supportedModels.map(toSupportedModelV2(String(params.provider)))))),
];

const connectionPasses = http.put("*/api/v2/platform/models/testConnection", () => HttpResponse.json({ data: { success: true, message: "Connection successful.", errorClass: null } }));

const submitSuccessHandlers = withDefaults(
    ...baseHandlers,
    connectionPasses,
    http.post("*/api/v2/platform/models", async ({ request }) => {
        const body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ data: savedModelV2(`id-${String(body.alias)}`, body) }, { status: 201 });
    })
);

const seededDefaults = [
    { id: "general-row-id", alias: "general" },
    { id: "planning-row-id", alias: "planning" },
];

const patched: Array<{ id: string; body: Record<string, unknown> }> = [];

function updateExistingHandlers(rows: Array<{ id: string } & Record<string, unknown>>) {
    return withDefaults(
        http.get("*/api/v2/platform/models", () => HttpResponse.json(paginatedResponse(rows.map(row => savedModelV2(row.id, row))))),
        http.put("*/api/v2/platform/providers/:provider/models", ({ params }) => HttpResponse.json(paginatedResponse(supportedModels.map(toSupportedModelV2(String(params.provider)))))),
        connectionPasses,
        http.post("*/api/v2/platform/models", () => {
            return HttpResponse.json({ message: "Create should not be called when the default rows already exist", errorId: "unexpected-create" }, { status: 500 });
        }),
        http.patch("*/api/v2/platform/models/:id", async ({ request, params }) => {
            const body = (await request.json()) as Record<string, unknown>;
            patched.push({ id: String(params.id), body });
            return HttpResponse.json({ data: savedModelV2(String(params.id), body) });
        })
    );
}

const oneConfiguredDefault = [
    { id: "general-row-id", alias: "general", provider: "anthropic", modelName: "anthropic/claude-sonnet", pricing: { input: "3", output: "15", cacheRead: null, cacheWrite: null } },
    { id: "planning-row-id", alias: "planning" },
];

const testFailureHandlers = withDefaults(
    ...baseHandlers,
    http.put("*/api/v2/platform/models/testConnection", () => {
        return HttpResponse.json({ data: { success: false, message: "Authentication failed: Invalid API key provided", errorClass: "auth_credentials_invalid" } });
    }),
    http.post("*/api/v2/platform/models", () => {
        return HttpResponse.json({ message: "Model should not have been created after a failed connection test", errorId: "unexpected-create" }, { status: 500 });
    })
);

const planningSaveFailureHandlers = withDefaults(
    ...baseHandlers,
    connectionPasses,
    http.post("*/api/v2/platform/models", async ({ request }) => {
        const body = (await request.json()) as Record<string, unknown>;
        if (body.alias === "general") {
            return HttpResponse.json({ data: savedModelV2("id-general", body) }, { status: 201 });
        }
        return HttpResponse.json({ message: "Planning model save failed", errorId: "planning-save-failed" }, { status: 500 });
    })
);

const listLoadErrorHandlers = withDefaults(http.get("*/api/v2/platform/models", () => HttpResponse.json({ message: "Service unavailable", errorId: "list-load-failed" }, { status: 500 })));

async function fillOpenAIConnection(canvasElement: HTMLElement, content: ReturnType<typeof within>) {
    expect(await content.findByText("Model Provider")).toBeInTheDocument();

    await userEvent.click((await content.findAllByRole("combobox"))[0]);
    await userEvent.click(await within(document.body).findByText("OpenAI"));

    expect(await content.findByText("Default Model")).toBeInTheDocument();

    const apiKey = canvasElement.querySelector('input[name="apiKey"]') as HTMLInputElement;
    await userEvent.type(apiKey, "sk-test-key-123456");

    const modelCombo = await content.findByPlaceholderText(/Type or select a model/i);
    await userEvent.click(modelCombo);
    await userEvent.click(await within(document.body).findByText("gpt-4o"));
}

const meta = {
    title: "Common/ModelSetupForm",
    component: ModelSetupForm,
    parameters: {
        docs: {
            description: {
                component: "Admin first-time setup form: one model + connection configures both the general and planning defaults. The connection is validated before anything is written — a failed test blocks the save.",
            },
        },
    },
    decorators: [
        Story => {
            const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
            return (
                <QueryClientProvider client={client}>
                    <div style={{ maxWidth: 640 }}>
                        <Story />
                    </div>
                </QueryClientProvider>
            );
        },
    ],
} satisfies Meta<typeof ModelSetupForm>;

export default meta;
type Story = StoryObj<typeof meta>;

export const SubmitSuccess: Story = {
    args: { onSuccess: fn(), onClose: fn() },
    parameters: { msw: { handlers: submitSuccessHandlers } },
    play: async ({ args, canvasElement }) => {
        const content = within(canvasElement);

        await fillOpenAIConnection(canvasElement, content);
        await userEvent.click(content.getByRole("button", { name: /Set Up Default Models/i }));

        await waitFor(() => expect(args.onSuccess).toHaveBeenCalled());
        await expect(args.onClose).toHaveBeenCalled();
    },
};

export const UpdatesSeededDefaults: Story = {
    args: { onSuccess: fn(), onClose: fn() },
    parameters: { msw: { handlers: updateExistingHandlers(seededDefaults) } },
    play: async ({ args, canvasElement }) => {
        patched.length = 0;
        const content = within(canvasElement);

        await fillOpenAIConnection(canvasElement, content);
        await userEvent.click(content.getByRole("button", { name: /Set Up Default Models/i }));

        await waitFor(() => expect(args.onSuccess).toHaveBeenCalled());
        await expect(args.onClose).toHaveBeenCalled();
        await expect(patched.map(p => p.id).sort()).toEqual(["general-row-id", "planning-row-id"]);
        for (const { body } of patched) await expect(body).not.toHaveProperty("pricing");
    },
};

export const LeavesAConfiguredDefaultAlone: Story = {
    args: { onSuccess: fn(), onClose: fn() },
    parameters: { msw: { handlers: updateExistingHandlers(oneConfiguredDefault) } },
    play: async ({ args, canvasElement }) => {
        patched.length = 0;
        const content = within(canvasElement);

        await fillOpenAIConnection(canvasElement, content);
        await userEvent.click(content.getByRole("button", { name: /Set Up Default Models/i }));

        await waitFor(() => expect(args.onSuccess).toHaveBeenCalled());
        await expect(patched.map(p => p.id)).toEqual(["planning-row-id"]);
    },
};

const freeModelDefaults = [
    { id: "general-row-id", alias: "general", provider: "openai", modelName: "solace-free" },
    { id: "planning-row-id", alias: "planning", provider: "openai", modelName: "solace-free" },
];

export const ReplacesTheFreeModel: Story = {
    args: { onSuccess: fn(), onClose: fn(), replaces: model => model.modelName === "solace-free" },
    parameters: { msw: { handlers: updateExistingHandlers(freeModelDefaults) } },
    play: async ({ args, canvasElement }) => {
        patched.length = 0;
        const content = within(canvasElement);

        await fillOpenAIConnection(canvasElement, content);
        await userEvent.click(content.getByRole("button", { name: /Set Up Default Models/i }));

        await waitFor(() => expect(args.onSuccess).toHaveBeenCalled());
        await expect(patched.map(p => p.id).sort()).toEqual(["general-row-id", "planning-row-id"]);
        for (const { body } of patched) await expect(body).toMatchObject({ provider: "openai", modelName: expect.stringContaining("gpt-4o") });
    },
};

export const TestConnectionFails: Story = {
    args: { onSuccess: fn(), onClose: fn() },
    parameters: { msw: { handlers: testFailureHandlers } },
    play: async ({ args, canvasElement }) => {
        const content = within(canvasElement);

        await fillOpenAIConnection(canvasElement, content);
        await userEvent.click(content.getByRole("button", { name: /Set Up Default Models/i }));

        expect(await content.findByText(/Authentication failed/i)).toBeInTheDocument();
        await expect(args.onSuccess).not.toHaveBeenCalled();
        await expect(args.onClose).not.toHaveBeenCalled();
    },
};

export const PlanningSaveFails: Story = {
    args: { onSuccess: fn(), onClose: fn() },
    parameters: { msw: { handlers: planningSaveFailureHandlers } },
    play: async ({ args, canvasElement }) => {
        const content = within(canvasElement);

        await fillOpenAIConnection(canvasElement, content);
        await userEvent.click(content.getByRole("button", { name: /Set Up Default Models/i }));

        expect(await content.findByText(/Planning model save failed/i)).toBeInTheDocument();
        await expect(args.onSuccess).not.toHaveBeenCalled();
        await expect(args.onClose).not.toHaveBeenCalled();
    },
};

export const ExistingModelsLoadError: Story = {
    args: { onSuccess: fn(), onClose: fn() },
    parameters: { msw: { handlers: listLoadErrorHandlers } },
    play: async ({ canvasElement }) => {
        const content = within(canvasElement);

        expect(await content.findByText(/Couldn't load existing models/i)).toBeInTheDocument();
        await waitFor(() => expect(content.getByRole("button", { name: /Set Up Default Models/i })).toBeDisabled());
    },
};
