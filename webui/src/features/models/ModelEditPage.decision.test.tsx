// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterAll, afterEach, beforeAll, describe, expect, test } from "vitest";

import type { ModelConfig } from "@/api/models";

import { anthropicModelConfig, typesafeDecisionModelConfig } from "@/testing/data/models";
import { savedModelV2, toModelV2, toSupportedModelV2 } from "@/testing/data/modelsV2";
import { paginatedResponse } from "@/testing/helpers/handlers";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { ModelEditPage } from "./ModelEditPage";

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const DECISION_FLAG = "workflow_decision_node";
const EVAL_DECISION_FLAG = "eval_decision_evaluator";

const openaiDecisionModelConfig: ModelConfig = { ...typesafeDecisionModelConfig, id: "openai-decision-id", alias: "luna", provider: "openai", modelName: "gpt-6-luna" };

interface Captured {
    createBody: Record<string, unknown> | null;
    testBody: Record<string, unknown> | null;
    patchBody: Record<string, unknown> | null;
    suggestionsBody?: Record<string, unknown> | null;
}

function setupHandlers(captured: Captured, stored: ModelConfig[] = []) {
    server.use(
        http.get("*/api/v2/platform/models/:id", ({ params }) => {
            const model = stored.find(m => m.id === params.id);
            return model ? HttpResponse.json({ data: toModelV2(model) }) : HttpResponse.json({ message: "Not found", errorId: "test-not-found" }, { status: 404 });
        }),
        http.put("*/api/v2/platform/providers/:provider/models", async ({ params, request }) => {
            const body = (await request.json()) as Record<string, unknown>;
            captured.suggestionsBody = body;
            let data = [{ id: "claude-3-5-sonnet", label: "Claude 3.5 Sonnet" }];
            if (params.provider === "typesafe") data = [{ id: "jev-1.13.0", label: "jev-1.13.0" }];
            if (params.provider === "openai" && body.modelType === "decision") data = [{ id: "gpt-6-luna", label: "gpt-6-luna" }];
            return HttpResponse.json(paginatedResponse(data.map(toSupportedModelV2(String(params.provider)))));
        }),
        http.put("*/api/v2/platform/models/testConnection", async ({ request }) => {
            captured.testBody = (await request.json()) as Record<string, unknown>;
            return HttpResponse.json({ data: { success: true, message: null, errorClass: null } });
        }),
        http.post("*/api/v2/platform/models", async ({ request }) => {
            captured.createBody = (await request.json()) as Record<string, unknown>;
            return HttpResponse.json({ data: savedModelV2("new-model-id", captured.createBody) }, { status: 201 });
        }),
        http.patch("*/api/v2/platform/models/:id", async ({ request, params }) => {
            captured.patchBody = (await request.json()) as Record<string, unknown>;
            const model = stored.find(m => m.id === params.id);
            return HttpResponse.json({ data: savedModelV2(String(params.id), captured.patchBody, model) });
        })
    );
}

interface PageFlags {
    decisionNodes?: boolean;
    decisionEvaluators?: boolean;
}

function renderPage(path: string, { decisionNodes = false, decisionEvaluators = false }: PageFlags = {}) {
    return render(
        <StoryProvider flagValues={{ [DECISION_FLAG]: decisionNodes, [EVAL_DECISION_FLAG]: decisionEvaluators }}>
            <MemoryRouter initialEntries={[path]}>
                <Routes>
                    <Route path="/models/new/edit" element={<ModelEditPage />} />
                    <Route path="/models/:id/edit" element={<ModelEditPage />} />
                    <Route path="/models" element={<div>Models List</div>} />
                </Routes>
            </MemoryRouter>
        </StoryProvider>
    );
}

async function openProviderList(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getAllByRole("combobox")[0]);
}

function cacheStrategySelect(): HTMLSelectElement {
    return Array.from(document.querySelectorAll<HTMLSelectElement>('select[aria-hidden="true"]')).find(sel => Array.from(sel.options).some(o => o.value === "1h")) as HTMLSelectElement;
}

describe("ModelEditPage - decision models", () => {
    test("does not offer TypeSafe while decision nodes are disabled", async () => {
        setupHandlers({ createBody: null, testBody: null, patchBody: null });
        renderPage("/models/new/edit");
        const user = userEvent.setup();

        await screen.findAllByText("Add Model");
        await openProviderList(user);

        expect(await screen.findByText("Anthropic")).toBeInTheDocument();
        expect(screen.queryByText("TypeSafe")).toBeNull();
        expect(screen.queryByText("Model Type")).toBeNull();
    });

    test("offers decision models while only decision evaluators are enabled", async () => {
        setupHandlers({ createBody: null, testBody: null, patchBody: null });
        renderPage("/models/new/edit", { decisionEvaluators: true });
        const user = userEvent.setup();

        await screen.findAllByText("Add Model");
        await user.click(screen.getByRole("radio", { name: /Decision model/ }));
        await openProviderList(user);

        expect(await screen.findByText("TypeSafe")).toBeInTheDocument();
        expect(screen.queryByText("Anthropic")).toBeNull();
    });

    test("asks for the model type first, offering only that type's providers and badging decision models Early Access", async () => {
        setupHandlers({ createBody: null, testBody: null, patchBody: null });
        server.use(
            http.get("*/api/v1/config/capabilities", () => HttpResponse.json(paginatedResponse([{ key: "workflow_decision_node", name: "Workflow Decision Nodes", description: "", status: "early_access", platform: "darwin", hasEnvOverride: false }])))
        );
        renderPage("/models/new/edit", { decisionNodes: true });
        const user = userEvent.setup();

        await screen.findAllByText("Add Model");
        const chatType = screen.getByRole("radio", { name: /Chat \(LLM\) model/ });
        const decisionType = screen.getByRole("radio", { name: /Decision model/ });
        expect(chatType).toBeChecked();
        expect(await within(decisionType.closest("label") as HTMLElement).findByText("EARLY ACCESS")).toBeInTheDocument();

        await openProviderList(user);
        expect(await screen.findByText("Anthropic")).toBeInTheDocument();
        expect(screen.queryByText("TypeSafe")).toBeNull();
        await user.keyboard("{Escape}");

        await user.click(decisionType);
        await openProviderList(user);
        const typesafe = (await screen.findAllByText("TypeSafe")).map(el => el.closest("button")).find(Boolean) as HTMLElement;
        expect(await within(typesafe).findByText("EARLY ACCESS")).toBeInTheDocument();
        expect(screen.queryByText("Anthropic")).toBeNull();
        expect(await screen.findByText("OpenAI")).toBeInTheDocument();
    });

    test("creates a priced TypeSafe model as a decision model without chat parameters or cache rates", async () => {
        const captured: Captured = { createBody: null, testBody: null, patchBody: null };
        setupHandlers(captured);
        renderPage("/models/new/edit", { decisionNodes: true });
        const user = userEvent.setup();

        await screen.findAllByText("Add Model");
        await user.type(document.querySelector('input[name="alias"]') as HTMLInputElement, "jev");
        await user.type(document.querySelector('textarea[name="description"]') as HTMLTextAreaElement, "Routes tickets");
        await user.click(screen.getByRole("radio", { name: /Decision model/ }));
        await openProviderList(user);
        await user.click(await screen.findByText("TypeSafe"));

        expect(screen.getByRole("radio", { name: /Decision model/ })).toBeChecked();
        expect(screen.queryByText("Advanced Settings")).toBeNull();
        expect(document.querySelector('input[name="maxInputTokens"]')).toBeNull();
        expect(document.querySelector('input[name="apiBase"]')?.getAttribute("placeholder")).toBe("https://api.typesafe.ai");

        await waitFor(() => expect(document.querySelector('input[name="apiKey"]')).toBeTruthy());
        await user.type(document.querySelector('input[name="apiKey"]') as HTMLInputElement, "ts-test-key");
        await user.click(await screen.findByPlaceholderText(/Type or select a model/i));
        await user.click(await screen.findByText("jev-1.13.0"));
        expect(screen.queryByText("Cached input tokens have different prices")).toBeNull();
        await user.type(screen.getByLabelText(/Input Price/), "2");
        await user.type(screen.getByLabelText(/Output Price/), "8");

        await user.click(screen.getByRole("button", { name: /Add/i }));
        await screen.findByText("Models List");

        expect(captured.testBody).toMatchObject({ provider: "typesafe", modelName: "jev-1.13.0", modelType: "decision" });
        expect(captured.createBody).toMatchObject({ provider: "typesafe", modelType: "decision", modelName: "jev-1.13.0", authConfig: { type: "apikey", api_key: "ts-test-key" } });
        expect(captured.createBody?.modelParams).toEqual({});
        expect(captured.createBody?.pricing).toEqual({ input: "2.00", output: "8.00", cacheRead: null, cacheWrite: null });
    });

    test("offers OpenAI for a decision model and creates one", async () => {
        const captured: Captured = { createBody: null, testBody: null, patchBody: null };
        setupHandlers(captured);
        renderPage("/models/new/edit", { decisionNodes: true });
        const user = userEvent.setup();

        await screen.findAllByText("Add Model");
        await user.type(document.querySelector('input[name="alias"]') as HTMLInputElement, "luna");
        await user.type(document.querySelector('textarea[name="description"]') as HTMLTextAreaElement, "Routes tickets");
        await user.click(screen.getByRole("radio", { name: /Decision model/ }));

        expect(screen.getAllByRole("combobox")[0]).toHaveTextContent(/^$|Select/);
        await openProviderList(user);
        expect(await screen.findByText("TypeSafe")).toBeInTheDocument();
        expect(await screen.findByText(/OpenAI's Decisions API/)).toBeInTheDocument();
        await user.click(screen.getByText("OpenAI"));

        expect(screen.queryByText("Advanced Settings")).toBeNull();
        expect(document.querySelector('input[name="apiBase"]')?.getAttribute("placeholder")).toBe("https://api.openai.com/v1");
        await waitFor(() => expect(document.querySelector('input[name="apiKey"]')).toBeTruthy());
        await user.type(document.querySelector('input[name="apiKey"]') as HTMLInputElement, "sk-decisions");
        await user.click(await screen.findByPlaceholderText(/Type or select a model/i));
        await user.click(await screen.findByText("gpt-6-luna"));
        expect(captured.suggestionsBody).toMatchObject({ modelType: "decision" });

        await user.click(screen.getByRole("button", { name: /Add/i }));
        await screen.findByText("Models List");

        expect(captured.testBody).toMatchObject({ provider: "openai", modelName: "gpt-6-luna", modelType: "decision" });
        expect(captured.createBody).toMatchObject({ provider: "openai", modelType: "decision", modelName: "gpt-6-luna", authConfig: { type: "apikey", api_key: "sk-decisions" } });
        expect(captured.createBody?.modelParams).toEqual({});
    });

    test("Test Connection asks for an OpenAI decision model, and never fetches its chat parameters", async () => {
        const captured: Captured = { createBody: null, testBody: null, patchBody: null };
        setupHandlers(captured);
        let paramsCalls = 0;
        server.use(
            http.put("*/api/v2/platform/providers/:provider/params", () => {
                paramsCalls++;
                return HttpResponse.json(paginatedResponse([{ type: "providerParam", key: "temperature" }]));
            })
        );
        renderPage("/models/new/edit", { decisionNodes: true });
        const user = userEvent.setup();

        await screen.findAllByText("Add Model");
        await user.click(screen.getByRole("radio", { name: /Decision model/ }));
        await openProviderList(user);
        await user.click(await screen.findByText("OpenAI"));
        await waitFor(() => expect(document.querySelector('input[name="apiKey"]')).toBeTruthy());
        await user.type(document.querySelector('input[name="apiKey"]') as HTMLInputElement, "sk-decisions");
        await user.click(await screen.findByPlaceholderText(/Type or select a model/i));
        await user.click(await screen.findByText("gpt-6-luna"));

        await user.click(screen.getByTestId("test-connection-button"));
        await screen.findByText("Connection test successful");
        expect(captured.testBody).toMatchObject({ provider: "openai", modelName: "gpt-6-luna", modelType: "decision" });

        await new Promise(resolve => setTimeout(resolve, 800));
        expect(paramsCalls).toBe(0);
    });

    test("a chat model's create request carries no model type", async () => {
        const captured: Captured = { createBody: null, testBody: null, patchBody: null };
        setupHandlers(captured);
        renderPage("/models/new/edit", { decisionNodes: true });
        const user = userEvent.setup();

        await screen.findAllByText("Add Model");
        await user.type(document.querySelector('input[name="alias"]') as HTMLInputElement, "chat-model");
        await user.type(document.querySelector('textarea[name="description"]') as HTMLTextAreaElement, "A chat model");
        await openProviderList(user);
        await user.click(await screen.findByText("Anthropic"));
        await waitFor(() => expect(document.querySelector('input[name="apiKey"]')).toBeTruthy());
        await user.type(document.querySelector('input[name="apiKey"]') as HTMLInputElement, "sk-ant-test-key");
        await user.click(await screen.findByPlaceholderText(/Type or select a model/i));
        await user.click(await screen.findByText("claude-3-5-sonnet"));

        await user.click(screen.getByRole("button", { name: /Add/i }));
        await screen.findByText("Models List");

        expect(captured.createBody).not.toHaveProperty("modelType");
    });

    test("an input-token limit entered for a chat model is not saved on a decision model, whose form has no such field", async () => {
        const captured: Captured = { createBody: null, testBody: null, patchBody: null };
        setupHandlers(captured);
        renderPage("/models/new/edit", { decisionNodes: true });
        const user = userEvent.setup();

        await screen.findAllByText("Add Model");
        await user.type(document.querySelector('input[name="alias"]') as HTMLInputElement, "jev");
        await user.type(document.querySelector('textarea[name="description"]') as HTMLTextAreaElement, "Routes tickets");
        await openProviderList(user);
        await user.click(await screen.findByText("Anthropic"));
        await waitFor(() => expect(document.querySelector('input[name="maxInputTokens"]')).toBeTruthy());
        await user.type(document.querySelector('input[name="maxInputTokens"]') as HTMLInputElement, "8000");

        await user.click(screen.getByRole("radio", { name: /Decision model/ }));
        await openProviderList(user);
        await user.click(await screen.findByText("TypeSafe"));
        await waitFor(() => expect(document.querySelector('input[name="apiKey"]')).toBeTruthy());
        await user.type(document.querySelector('input[name="apiKey"]') as HTMLInputElement, "ts-test-key");
        await user.click(await screen.findByPlaceholderText(/Type or select a model/i));
        await user.click(await screen.findByText("jev-1.13.0"));
        await user.click(screen.getByRole("button", { name: /Add/i }));
        await screen.findByText("Models List");

        expect(captured.createBody?.maxInputTokens).toBeNull();
    });

    test("a chat model's parameters do not survive a trip through the decision type", async () => {
        const captured: Captured = { createBody: null, testBody: null, patchBody: null };
        setupHandlers(captured);
        renderPage("/models/new/edit", { decisionNodes: true });
        const user = userEvent.setup();

        await screen.findAllByText("Add Model");
        await user.type(document.querySelector('input[name="alias"]') as HTMLInputElement, "chat-model");
        await user.type(document.querySelector('textarea[name="description"]') as HTMLTextAreaElement, "A chat model");
        await openProviderList(user);
        await user.click(await screen.findByText("Anthropic"));
        await user.click(await screen.findByText("Advanced Settings"));
        await user.type(document.querySelector('input[name="max_tokens"]') as HTMLInputElement, "8192");
        fireEvent.change(cacheStrategySelect(), { target: { value: "1h" } });

        await user.click(screen.getByRole("radio", { name: /Decision model/ }));
        await user.click(screen.getByRole("radio", { name: /Chat \(LLM\) model/ }));
        await openProviderList(user);
        await user.click(await screen.findByText("Anthropic"));
        await waitFor(() => expect(document.querySelector('input[name="apiKey"]')).toBeTruthy());
        await user.type(document.querySelector('input[name="apiKey"]') as HTMLInputElement, "sk-ant-test-key");
        await user.click(await screen.findByPlaceholderText(/Type or select a model/i));
        await user.click(await screen.findByText("claude-3-5-sonnet"));
        await user.click(screen.getByRole("button", { name: /Add/i }));
        await screen.findByText("Models List");

        expect(captured.createBody?.modelParams).toEqual({ cache_strategy: "5m" });
    });

    test("editing a decision model keeps its provider and never sends its type", async () => {
        const captured: Captured = { createBody: null, testBody: null, patchBody: null };
        setupHandlers(captured, [typesafeDecisionModelConfig]);
        renderPage(`/models/${typesafeDecisionModelConfig.id}/edit`, { decisionNodes: true });
        const user = userEvent.setup();

        await screen.findAllByText("Edit jev");
        expect(screen.getByTestId("modelTypeReadOnly")).toHaveTextContent("Decision model");
        expect(screen.queryByRole("radio", { name: /Chat \(LLM\) model/ })).toBeNull();
        expect(screen.getAllByRole("combobox")[0]).toBeDisabled();

        await user.click(screen.getByRole("button", { name: /Save/i }));
        await screen.findByText("Models List");

        expect(captured.patchBody).toMatchObject({ provider: "typesafe" });
        expect(captured.patchBody).not.toHaveProperty("modelType");
    });

    test("editing an OpenAI decision model keeps it a decision model", async () => {
        const captured: Captured = { createBody: null, testBody: null, patchBody: null };
        setupHandlers(captured, [openaiDecisionModelConfig]);
        renderPage(`/models/${openaiDecisionModelConfig.id}/edit`, { decisionNodes: true });
        const user = userEvent.setup();

        await screen.findAllByText("Edit luna");
        expect(screen.getByTestId("modelTypeReadOnly")).toHaveTextContent("Decision model");
        const providerSelect = screen.getAllByRole("combobox")[0];
        expect(providerSelect).toBeDisabled();
        await waitFor(() => expect(providerSelect).toHaveValue("OpenAI"));
        expect(screen.queryByText("Advanced Settings")).toBeNull();

        await user.click(screen.getByRole("button", { name: /Save/i }));
        await screen.findByText("Models List");

        expect(captured.testBody).toMatchObject({ provider: "openai", modelType: "decision", modelId: openaiDecisionModelConfig.id });
        expect(captured.patchBody).toMatchObject({ provider: "openai", modelName: "gpt-6-luna" });
        expect(captured.patchBody).not.toHaveProperty("modelType");
    });

    test("with decision nodes off, editing a chat model shows no model type", async () => {
        setupHandlers({ createBody: null, testBody: null, patchBody: null }, [anthropicModelConfig]);
        renderPage(`/models/${anthropicModelConfig.id}/edit`);

        await screen.findAllByText("Edit anthropic-model");
        expect(screen.queryByTestId("modelTypeReadOnly")).toBeNull();
        expect(screen.queryByText("Model Type")).toBeNull();
    });

    test("with decision nodes off, editing a decision model still shows its type", async () => {
        setupHandlers({ createBody: null, testBody: null, patchBody: null }, [typesafeDecisionModelConfig]);
        renderPage(`/models/${typesafeDecisionModelConfig.id}/edit`);

        await screen.findAllByText("Edit jev");
        expect(screen.getByTestId("modelTypeReadOnly")).toHaveTextContent("Decision model");
    });

    test("editing a chat model never offers a decision provider", async () => {
        setupHandlers({ createBody: null, testBody: null, patchBody: null }, [anthropicModelConfig]);
        renderPage(`/models/${anthropicModelConfig.id}/edit`, { decisionNodes: true });
        const user = userEvent.setup();

        await screen.findAllByText("Edit anthropic-model");
        expect(screen.getByTestId("modelTypeReadOnly")).toHaveTextContent("Chat (LLM) model");
        await openProviderList(user);

        expect(await screen.findByText("OpenAI")).toBeInTheDocument();
        expect(screen.queryByText("TypeSafe")).toBeNull();
    });
});
