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

import { ModelSetupDialog } from "@/common/dialogs/ModelSetupDialog";

import { toModelV2, toSupportedModelV2 } from "@/testing/data/modelsV2";
import { paginatedResponse } from "@/testing/helpers/handlers";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { ModelEditPage } from "./ModelEditPage";

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterEach(() => {
    server.resetHandlers();
    restoreFetch();
});
afterAll(() => server.close());

const LITELLM_BASE = "https://litellm.workshop.example.com";
const LITELLM_KEY = "sk-workshop-key";

let restoreFetch = () => {};

const SUPPORTED_MODELS_URL = /\/providers\/[^/]+\/models(\?|$)/;
const SUPPORTED_MODELS_PATH = "*/api/v2/platform/providers/:provider/models";

function trackModelRequests(): Array<Record<string, unknown>> {
    const bodies: Array<Record<string, unknown>> = [];
    const realFetch = globalThis.fetch;
    globalThis.fetch = ((...args: Parameters<typeof realFetch>) => {
        const url = String(args[0]);
        if (SUPPORTED_MODELS_URL.test(url)) {
            const body = args[1]?.body;
            if (typeof body === "string") bodies.push(JSON.parse(body) as Record<string, unknown>);
        }
        return realFetch(...args);
    }) as typeof realFetch;
    restoreFetch = () => {
        globalThis.fetch = realFetch;
        restoreFetch = () => {};
    };
    return bodies;
}

const modelListPage = (ids: string[]) => HttpResponse.json(paginatedResponse(ids.map(id => toSupportedModelV2("custom")({ id, label: id }))));

const upstreamUnavailable = () => HttpResponse.json({ message: "upstream unavailable", errorId: "test-502" }, { status: 502 });

function seedPlaceholderAliases() {
    server.use(
        http.get("*/api/v2/platform/models", () =>
            HttpResponse.json(
                paginatedResponse(
                    [
                        { id: "11111111-1111-1111-1111-111111111111", alias: "general" },
                        { id: "22222222-2222-2222-2222-222222222222", alias: "planning" },
                    ].map(({ id, alias }) =>
                        toModelV2({
                            id,
                            alias,
                            provider: null,
                            modelName: null,
                            apiBase: null,
                            authType: null,
                            authConfig: {},
                            modelParams: {},
                            maxInputTokens: null,
                            description: null,
                            createdBy: "",
                            updatedBy: "",
                            createdAt: "",
                            updatedAt: "",
                        })
                    )
                )
            )
        )
    );
}

const SURFACES = [
    {
        name: "models edit page",
        mount: () =>
            render(
                <StoryProvider>
                    <MemoryRouter initialEntries={["/models/new/edit"]}>
                        <Routes>
                            <Route path="/models/new/edit" element={<ModelEditPage />} />
                            <Route path="/models" element={<div>Models List</div>} />
                        </Routes>
                    </MemoryRouter>
                </StoryProvider>
            ),
        ready: async () => {
            await screen.findAllByText("Add Model");
        },
    },
    {
        name: "first-run model setup dialog",
        mount: () => {
            seedPlaceholderAliases();
            return render(
                <StoryProvider>
                    <ModelSetupDialog open onOpenChange={() => {}} hasWritePermissions />
                </StoryProvider>
            );
        },
        ready: async () => {
            await screen.findByText("Configure Your AI Models");
        },
    },
] as const;

async function fillLiteLLMConnection(user: ReturnType<typeof userEvent.setup>) {
    await user.click((await screen.findAllByRole("combobox"))[0]);
    await user.click(await screen.findByText("Custom"));

    await waitFor(() => expect(screen.getByText("Authentication Type")).toBeInTheDocument());
    await user.click(screen.getAllByRole("combobox")[1]);
    await user.click(await screen.findByRole("option", { name: /API Key/ }));

    await waitFor(() => expect(document.querySelector('input[name="apiKey"]')).toBeTruthy());
    await user.type(document.querySelector('input[name="apiKey"]') as HTMLInputElement, LITELLM_KEY);
    await user.type(apiBaseInput(), LITELLM_BASE);
}

function apiBaseInput() {
    return document.querySelector('input[name="apiBase"]') as HTMLInputElement;
}

function modelNameInput() {
    return screen.getByTestId("modelNameCombobox").querySelector("input") as HTMLInputElement;
}

describe.each(SURFACES)("model name dropdown — $name", ({ mount, ready }) => {
    test("a transient upstream failure is recoverable by reopening the dropdown", async () => {
        let upstreamHealthy = false;
        server.use(http.put(SUPPORTED_MODELS_PATH, () => (upstreamHealthy ? modelListPage(["gpt-4o-mini", "claude-sonnet-4"]) : upstreamUnavailable())));
        const requests = trackModelRequests();

        mount();
        const user = userEvent.setup();
        await ready();
        await fillLiteLLMConnection(user);

        await user.click(modelNameInput());
        await waitFor(() => expect(requests).toHaveLength(1));
        expect(screen.queryByText("gpt-4o-mini")).not.toBeInTheDocument();

        upstreamHealthy = true;
        await user.click(modelNameInput());

        expect(await screen.findByText("gpt-4o-mini")).toBeInTheDocument();
        expect(requests).toHaveLength(2);
    });

    test("an empty first list is recoverable by reopening the dropdown", async () => {
        let proxyHasModels = false;
        server.use(http.put(SUPPORTED_MODELS_PATH, () => (proxyHasModels ? modelListPage(["gpt-4o-mini"]) : modelListPage([]))));
        const requests = trackModelRequests();

        mount();
        const user = userEvent.setup();
        await ready();
        await fillLiteLLMConnection(user);

        await user.click(modelNameInput());
        await waitFor(() => expect(requests).toHaveLength(1));
        expect(await screen.findByText("No models found")).toBeInTheDocument();

        proxyHasModels = true;
        await user.click(modelNameInput());

        expect(await screen.findByText("gpt-4o-mini")).toBeInTheDocument();
        expect(requests).toHaveLength(2);
    });

    test("a stray space still lists every model", async () => {
        server.use(http.put(SUPPORTED_MODELS_PATH, () => modelListPage(["gpt-4o-mini", "claude-sonnet-4"])));

        mount();
        const user = userEvent.setup();
        await ready();
        await fillLiteLLMConnection(user);

        await user.click(modelNameInput());
        expect(await screen.findByText("gpt-4o-mini")).toBeInTheDocument();

        await user.type(modelNameInput(), " ");

        expect(screen.getByText("gpt-4o-mini")).toBeInTheDocument();
        expect(screen.getByText("claude-sonnet-4")).toBeInTheDocument();
        expect(screen.queryByText("No models found")).not.toBeInTheDocument();
    });

    test("the retry still carries the LiteLLM credentials", async () => {
        let upstreamHealthy = false;
        server.use(http.put(SUPPORTED_MODELS_PATH, () => (upstreamHealthy ? modelListPage(["gpt-4o-mini"]) : upstreamUnavailable())));
        const requests = trackModelRequests();

        mount();
        const user = userEvent.setup();
        await ready();
        await fillLiteLLMConnection(user);

        await user.click(modelNameInput());
        await waitFor(() => expect(requests).toHaveLength(1));

        upstreamHealthy = true;
        await user.click(modelNameInput());
        await waitFor(() => expect(requests).toHaveLength(2));

        const retry = requests[1];
        expect(retry.apiBase).toBe(LITELLM_BASE);
        expect((retry.authConfig as Record<string, unknown>).api_key).toBe(LITELLM_KEY);
    });

    test("the picker stays enabled and focused while its probe is in flight", async () => {
        server.use(
            http.put(SUPPORTED_MODELS_PATH, async () => {
                await new Promise(r => setTimeout(r, 80));
                return modelListPage(["claude-sonnet-4-6", "azure-gpt-4o"]);
            })
        );

        mount();
        const user = userEvent.setup();
        await ready();
        await fillLiteLLMConnection(user);

        const picker = modelNameInput();
        await user.click(picker);

        expect(await screen.findByText("Loading...")).toBeInTheDocument();
        expect(picker).toBeEnabled();
        expect(picker).toHaveFocus();

        expect(await screen.findByText("claude-sonnet-4-6")).toBeInTheDocument();
    });

    test("reverting a connection-field edit re-probes instead of replaying the cache", async () => {
        let proxyHasModels = false;
        server.use(http.put(SUPPORTED_MODELS_PATH, () => (proxyHasModels ? modelListPage(["gpt-4o-mini"]) : modelListPage([]))));
        const requests = trackModelRequests();

        mount();
        const user = userEvent.setup();
        await ready();
        await fillLiteLLMConnection(user);

        await user.click(modelNameInput());
        await waitFor(() => expect(requests).toHaveLength(1));
        expect(await screen.findByText("No models found")).toBeInTheDocument();
        await user.keyboard("{Escape}");

        proxyHasModels = true;
        await user.type(apiBaseInput(), "/v1");
        await user.clear(apiBaseInput());
        await user.type(apiBaseInput(), LITELLM_BASE);

        await user.click(modelNameInput());

        expect(await screen.findByText("gpt-4o-mini")).toBeInTheDocument();
    });
});
