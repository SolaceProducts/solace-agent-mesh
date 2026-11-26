// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import * as matchers from "@testing-library/jest-dom/matchers";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent, { type UserEvent } from "@testing-library/user-event";
import { describe, expect, type Mock, test, vi } from "vitest";

import type { ModelConfig } from "@/api/models/types";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import { mockModelConfigs } from "@/testing/data/models";
import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ModelEdit } from "./ModelEdit";
import { buildModelPayload, CHAT_PROVIDERS, type ModelFormData } from "./modelProviderUtils";

expect.extend(matchers);

const ALL_CAPABILITIES = { userHasCapability: () => true, CAPABILITIES };

function geminiModel(): ModelConfig {
    return {
        ...mockModelConfigs[0],
        id: "gemini-general",
        alias: "general",
        provider: "google_ai_studio",
        modelName: "gemini/gemini-2.5-pro",
        apiBase: "https://gemini-proxy.example.com",
        authType: "apikey",
        authConfig: { type: "apikey" },
        modelParams: { max_tokens: 8192, cache_strategy: "1h", top_p: 0.9, extra_headers: { "x-tenant-id": "example-tenant" } },
        maxInputTokens: 1_000_000,
        pricing: { input: "1.25", output: "10", cacheRead: null, cacheWrite: null },
    };
}

function bedrockModel(): ModelConfig {
    return {
        ...mockModelConfigs[0],
        id: "bedrock-general",
        alias: "general",
        provider: "bedrock",
        modelName: "anthropic.claude-3-5-sonnet-20240620-v1:0",
        apiBase: null,
        authType: "aws_iam",
        authConfig: { type: "aws_iam", aws_region_name: "us-east-1" },
        modelParams: {},
    };
}

function renderModelEdit(modelToEdit: ModelConfig | null): { onSave: Mock; user: UserEvent } {
    const onSave = vi.fn().mockResolvedValue(undefined);
    renderWithProviders(<ModelEdit isNew={modelToEdit === null} modelToEdit={modelToEdit} onSave={onSave} availableProviders={CHAT_PROVIDERS} modelsByProvider={{ openai: [{ id: "gpt-4o", label: "gpt-4o" }] }} />, {
        userContextValues: ALL_CAPABILITIES,
    });
    return { onSave, user: userEvent.setup() };
}

async function selectProvider(user: UserEvent, label: string) {
    await user.click(screen.getAllByRole("combobox")[0]);
    await user.click(await screen.findByText(label));
}

function apiKeyInput(): HTMLInputElement {
    return document.querySelector('input[name="apiKey"]') as HTMLInputElement;
}

async function pickGpt4oAndSave(user: UserEvent) {
    await user.click(screen.getByPlaceholderText(/Type or select a model/i));
    await user.click(await screen.findByText("gpt-4o"));
    fireEvent.submit(document.getElementById("model-form") as HTMLFormElement);
}

async function savedPayload(onSave: Mock): Promise<ReturnType<typeof buildModelPayload>> {
    await waitFor(() => expect(onSave).toHaveBeenCalled());
    const [formData, dirtyFields] = onSave.mock.calls[0] as [ModelFormData, Partial<Record<string, boolean>>];
    return buildModelPayload(formData, dirtyFields);
}

async function switchGeminiToOpenAIAndSave(): Promise<ReturnType<typeof buildModelPayload>> {
    const { onSave, user } = renderModelEdit(geminiModel());
    await selectProvider(user, "OpenAI");
    await user.type(apiKeyInput(), "sk-test");
    await pickGpt4oAndSave(user);
    return savedPayload(onSave);
}

describe("ModelEdit provider change", () => {
    test("keeps the model parameters, headers and input-token limit", async () => {
        const payload = await switchGeminiToOpenAIAndSave();

        expect(payload.modelParams).toEqual({ max_tokens: 8192, cache_strategy: "1h", top_p: 0.9, extra_headers: { "x-tenant-id": "example-tenant" } });
        expect(payload.maxInputTokens).toBe(1_000_000);
    });

    test("clears the model, API base, credentials and pricing", async () => {
        const { onSave, user } = renderModelEdit(geminiModel());
        await selectProvider(user, "OpenAI");

        expect(screen.getByPlaceholderText(/Type or select a model/i)).toHaveValue("");

        await user.type(apiKeyInput(), "sk-test");
        await pickGpt4oAndSave(user);
        const payload = await savedPayload(onSave);
        expect(payload.provider).toBe("openai");
        expect(payload.modelName).toBe("gpt-4o");
        expect(payload.apiBase).toBe("");
        expect(payload.authConfig).toEqual({ type: "apikey", api_key: "sk-test" });
        expect(payload.pricing).toEqual({ input: null, output: null, cacheRead: null, cacheWrite: null });
    });

    test("starts the new provider on its own auth type", async () => {
        const { onSave, user } = renderModelEdit(bedrockModel());
        await selectProvider(user, "OpenAI");

        await user.type(apiKeyInput(), "sk-test");
        await pickGpt4oAndSave(user);

        const payload = await savedPayload(onSave);
        expect(payload.authType).toBe("apikey");
        expect(payload.authConfig).toEqual({ type: "apikey", api_key: "sk-test" });
    });

    test("clears a key typed for the previous provider", async () => {
        const { user } = renderModelEdit(null);
        await selectProvider(user, "OpenAI");
        await user.type(apiKeyInput(), "sk-typed-for-openai");

        await selectProvider(user, "Anthropic");

        expect(apiKeyInput()).toHaveValue("");
    });

    test("asks for the new provider's key, since the saved one is the old provider's", async () => {
        const { onSave, user } = renderModelEdit(geminiModel());
        await selectProvider(user, "OpenAI");

        await pickGpt4oAndSave(user);

        expect(await screen.findByText("API Key is required")).toBeInTheDocument();
        expect(onSave).not.toHaveBeenCalled();
    });
});
