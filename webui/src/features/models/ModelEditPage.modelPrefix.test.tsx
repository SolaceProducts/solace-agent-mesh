// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterAll, afterEach, beforeAll, describe, expect, test } from "vitest";

import { REDACTED_SENTINEL } from "@/api/models";

import { anthropicModelConfig } from "@/testing/data/models";
import { savedModelV2, toModelV2, toSupportedModelV2 } from "@/testing/data/modelsV2";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { ModelEditPage } from "./ModelEditPage";

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const MODEL_PATH = "*/api/v2/platform/models/:id";
const GATEWAY_BASE = "https://gateway.example.com/llms/v1";

function serveModel(stored: Partial<typeof anthropicModelConfig>, listedModels: Array<{ id: string; label: string }> = []) {
    const model = { ...anthropicModelConfig, authConfig: { type: "apikey", api_key: REDACTED_SENTINEL }, ...stored };
    const saved: { body: Record<string, unknown> | null } = { body: null };
    server.use(
        http.get(MODEL_PATH, () => HttpResponse.json({ data: toModelV2(model) })),
        http.put("*/api/v2/platform/providers/:provider/models", () =>
            HttpResponse.json({ data: listedModels.map(toSupportedModelV2("custom")), meta: { pagination: { pageNumber: 1, pageSize: 100, count: listedModels.length, totalPages: 1, nextPage: null } } })
        ),
        http.put("*/api/v2/platform/models/testConnection", () => HttpResponse.json({ data: { success: true, message: null, errorClass: null } })),
        http.patch(MODEL_PATH, async ({ request }) => {
            saved.body = (await request.json()) as Record<string, unknown>;
            return HttpResponse.json({ data: savedModelV2(model.id, saved.body, model) });
        })
    );
    return saved;
}

async function renderEditPage() {
    render(
        <StoryProvider>
            <MemoryRouter initialEntries={[`/models/${anthropicModelConfig.id}/edit`]}>
                <Routes>
                    <Route path="/models/:id/edit" element={<ModelEditPage />} />
                    <Route path="/models" element={<div>Models List</div>} />
                </Routes>
            </MemoryRouter>
        </StoryProvider>
    );
    await screen.findAllByText("Edit anthropic-model");
    return userEvent.setup();
}

async function save(user: ReturnType<typeof userEvent.setup>, saved: { body: Record<string, unknown> | null }) {
    await user.click(screen.getByRole("button", { name: /Save/i }));
    await waitFor(() => expect(saved.body).not.toBeNull());
    return saved.body!.modelName;
}

const prefixField = () => screen.findByTestId("modelPrefixInput");
const modelNameInput = () => screen.findByPlaceholderText(/Type or select a model/i);

describe("ModelEditPage - model prefix", () => {
    test("a custom model shows its stored prefix apart from the name and saves them together", async () => {
        const saved = serveModel({ provider: "custom", modelName: "anthropic/claude-sonnet-4-6", apiBase: GATEWAY_BASE });
        const user = await renderEditPage();

        await waitFor(async () => expect(await prefixField()).toHaveValue("anthropic/"));
        expect(await modelNameInput()).toHaveValue("claude-sonnet-4-6");
        expect(screen.getByTestId("chatRequestUrl")).toHaveTextContent(`Requests go to: ${GATEWAY_BASE}/messages`);
        expect(screen.getByText(/Agent Mesh adds \/v1\/messages/)).toBeInTheDocument();
        expect(screen.queryByText(/Agent Mesh adds \/v1\/chat\/completions/)).not.toBeInTheDocument();

        expect(await save(user, saved)).toBe("anthropic/claude-sonnet-4-6");
    });

    test("a stored id whose first segment is a namespace opens whole under the prefix the runtime picks and saves back with that prefix", async () => {
        const saved = serveModel({ provider: "custom", modelName: "meta-llama/Llama-3.3-70B-Instruct", apiBase: GATEWAY_BASE });
        const user = await renderEditPage();

        await waitFor(async () => expect(await prefixField()).toHaveValue("openai/"));
        expect(await modelNameInput()).toHaveValue("meta-llama/Llama-3.3-70B-Instruct");

        expect(await save(user, saved)).toBe("openai/meta-llama/Llama-3.3-70B-Instruct");
    });

    test("picking a listed namespaced id from the dropdown saves its namespace once", async () => {
        const listedId = "meta-llama/Llama-3.1-8B-Instruct";
        const saved = serveModel({ provider: "custom", modelName: "meta-llama/Llama-3.3-70B-Instruct", apiBase: GATEWAY_BASE }, [{ id: listedId, label: listedId }]);
        const user = await renderEditPage();
        await waitFor(async () => expect(await prefixField()).toHaveValue("openai/"));

        await user.click(await modelNameInput());
        await user.click(await screen.findByRole("button", { name: listedId }));

        expect(await prefixField()).toHaveValue("openai/");
        expect(await save(user, saved)).toBe(`openai/${listedId}`);
    });

    test("a custom model stored without a prefix shows and saves the prefix it is called with, so saving keeps its API", async () => {
        const saved = serveModel({ provider: "custom", modelName: "us.anthropic.claude-opus-4-8", apiBase: GATEWAY_BASE });
        const user = await renderEditPage();

        await waitFor(async () => expect(await prefixField()).toHaveValue("anthropic/"));
        expect(await modelNameInput()).toHaveValue("us.anthropic.claude-opus-4-8");
        expect(screen.getByTestId("chatRequestUrl")).toHaveTextContent(`Requests go to: ${GATEWAY_BASE}/messages`);

        expect(await save(user, saved)).toBe("anthropic/us.anthropic.claude-opus-4-8");
    });

    test("a custom model stored without a prefix under a name the runtime sends to OpenAI saves openai/", async () => {
        const saved = serveModel({ provider: "custom", modelName: "gpt-oss-120b", apiBase: GATEWAY_BASE });
        const user = await renderEditPage();

        await waitFor(async () => expect(await prefixField()).toHaveValue("openai/"));
        expect(await save(user, saved)).toBe("openai/gpt-oss-120b");
    });

    test("entering another prefix for a custom model changes the request URL and the saved name", async () => {
        const saved = serveModel({ provider: "custom", modelName: "anthropic/claude-sonnet-4-6", apiBase: GATEWAY_BASE });
        const user = await renderEditPage();
        await waitFor(async () => expect(await prefixField()).toHaveValue("anthropic/"));

        await user.clear(await prefixField());
        await user.type(await prefixField(), "op");
        expect(screen.getByTestId("chatRequestUrl")).toHaveTextContent(`Requests go to: ${GATEWAY_BASE}/messages`);
        await user.type(await prefixField(), "enai/");

        await waitFor(() => expect(screen.getByTestId("chatRequestUrl")).toHaveTextContent(`Requests go to: ${GATEWAY_BASE}/chat/completions`));
        expect(screen.getByText(/Agent Mesh adds \/v1\/chat\/completions/)).toBeInTheDocument();
        expect(await save(user, saved)).toBe("openai/claude-sonnet-4-6");
    });

    test("a custom model takes any prefix the runtime routes by, typed in any case", async () => {
        const saved = serveModel({ provider: "custom", modelName: "anthropic/claude-sonnet-4-6", apiBase: GATEWAY_BASE });
        const user = await renderEditPage();
        await waitFor(async () => expect(await prefixField()).toHaveValue("anthropic/"));

        await user.clear(await prefixField());
        await user.type(await prefixField(), "Mistral");
        await user.tab();

        expect(await prefixField()).toHaveValue("Mistral/");
        expect(screen.queryByTestId("chatRequestUrl")).not.toBeInTheDocument();
        expect(await save(user, saved)).toBe("Mistral/claude-sonnet-4-6");
    });

    test("a custom model accepts a prefix Agent Mesh does not recognize and keeps its case", async () => {
        const saved = serveModel({ provider: "custom", modelName: "openai/my-model", apiBase: GATEWAY_BASE });
        const user = await renderEditPage();
        await waitFor(async () => expect(await prefixField()).toHaveValue("openai/"));

        await user.clear(await prefixField());
        await user.type(await prefixField(), "Qwen");
        await user.tab();

        expect(await prefixField()).toHaveValue("Qwen/");
        expect(screen.queryByText("Enter one word followed by /, such as openai/.")).not.toBeInTheDocument();
        expect(await save(user, saved)).toBe("Qwen/my-model");
    });

    test("a malformed prefix shows why beneath both fields and blocks the save", async () => {
        const saved = serveModel({ provider: "custom", modelName: "openai/my-model", apiBase: GATEWAY_BASE });
        const user = await renderEditPage();
        await waitFor(async () => expect(await prefixField()).toHaveValue("openai/"));

        await user.clear(await prefixField());
        await user.type(await prefixField(), "my prefix");
        await user.tab();

        await waitFor(() => expect(screen.getByTestId("modelNameMessages")).toHaveTextContent("Enter one word followed by /, such as openai/."));
        expect(await prefixField()).toHaveAttribute("aria-invalid", "true");
        expect(await prefixField()).toHaveAccessibleDescription(expect.stringContaining("Enter one word followed by /, such as openai/."));
        await user.click(screen.getByRole("button", { name: /Save/i }));
        await new Promise(resolve => setTimeout(resolve, 300));
        expect(saved.body).toBeNull();
    });

    test("a missing model name is reported beneath both fields", async () => {
        const saved = serveModel({ provider: "custom", modelName: "openai/my-model", apiBase: GATEWAY_BASE });
        const user = await renderEditPage();
        await waitFor(async () => expect(await modelNameInput()).toHaveValue("my-model"));

        await user.clear(await modelNameInput());
        await user.click(screen.getByRole("button", { name: /Save/i }));

        await waitFor(() => expect(screen.getByTestId("modelNameMessages")).toHaveTextContent("Model Name is required"));
        expect(saved.body).toBeNull();
    });

    test("a custom model keeps a stored prefix", async () => {
        const saved = serveModel({ provider: "custom", modelName: "groq/llama-3.1-70b", apiBase: GATEWAY_BASE });
        const user = await renderEditPage();

        await waitFor(async () => expect(await prefixField()).toHaveValue("groq/"));
        expect(await modelNameInput()).toHaveValue("llama-3.1-70b");

        expect(await save(user, saved)).toBe("groq/llama-3.1-70b");
    });

    test("another provider has no model prefix and saves the name as entered", async () => {
        const saved = serveModel({ provider: "anthropic", modelName: "claude-3-5-sonnet" });
        const user = await renderEditPage();

        await waitFor(async () => expect(await modelNameInput()).toHaveValue("claude-3-5-sonnet"));
        expect(screen.queryByTestId("modelPrefixInput")).not.toBeInTheDocument();
        expect(screen.queryByText("Model Prefix")).not.toBeInTheDocument();

        expect(await save(user, saved)).toBe("claude-3-5-sonnet");
    });

    test("a new custom model unlocks its prefix with its model name, starts on openai/ and saves a Claude name under it", async () => {
        const created: { body: Record<string, unknown> | null } = { body: null };
        serveModel({});
        server.use(
            http.post("*/api/v2/platform/models", async ({ request }) => {
                created.body = (await request.json()) as Record<string, unknown>;
                return HttpResponse.json({ data: savedModelV2("new-model-id", created.body) }, { status: 201 });
            })
        );
        render(
            <StoryProvider>
                <MemoryRouter initialEntries={["/models/new/edit"]}>
                    <Routes>
                        <Route path="/models/new/edit" element={<ModelEditPage />} />
                        <Route path="/models" element={<div>Models List</div>} />
                    </Routes>
                </MemoryRouter>
            </StoryProvider>
        );
        const user = userEvent.setup();
        await screen.findAllByText("Add Model");
        await user.type(document.querySelector('input[name="alias"]') as HTMLInputElement, "gateway-claude");
        await user.type(document.querySelector('textarea[name="description"]') as HTMLTextAreaElement, "Claude behind an OpenAI-compatible gateway");
        await user.click((await screen.findAllByRole("combobox"))[0]);
        await user.click(await screen.findByText("Custom"));
        await waitFor(() => expect(screen.getByText("Authentication Type")).toBeInTheDocument());
        await user.click(screen.getAllByRole("combobox")[1]);
        await user.click(await screen.findByRole("option", { name: /API Key/ }));
        await waitFor(() => expect(document.querySelector('input[name="apiKey"]')).toBeTruthy());
        await user.type(document.querySelector('input[name="apiKey"]') as HTMLInputElement, "sk-gateway");
        expect(await prefixField()).toBeDisabled();
        expect(screen.getByTestId("modelNameCombobox").querySelector("input")).toBeDisabled();
        expect(screen.getByTestId("modelNameMessages")).toHaveTextContent("Configure provider and authentication to select a model");
        await user.type(document.querySelector('input[name="apiBase"]') as HTMLInputElement, GATEWAY_BASE);

        await waitFor(async () => expect(await prefixField()).toBeEnabled());
        expect(screen.getByTestId("modelNameCombobox").querySelector("input")).toBeEnabled();
        expect(await prefixField()).toHaveValue("openai/");
        await user.type(await modelNameInput(), "us.anthropic.claude-opus-4-8");
        await user.click(screen.getByRole("button", { name: /Add/i }));

        await waitFor(() => expect(created.body).not.toBeNull());
        expect(created.body!.modelName).toBe("openai/us.anthropic.claude-opus-4-8");
    });

    test("another provider keeps a prefix in its model name, with no model prefix field", async () => {
        serveModel({ provider: "vertex_ai", authType: "gcp_service_account", authConfig: { type: "gcp_service_account" }, modelName: "anthropic/claude-3-haiku@20240307" });
        await renderEditPage();

        expect(await modelNameInput()).toHaveValue("anthropic/claude-3-haiku@20240307");
        expect(screen.queryByTestId("modelPrefixInput")).not.toBeInTheDocument();
    });
});
