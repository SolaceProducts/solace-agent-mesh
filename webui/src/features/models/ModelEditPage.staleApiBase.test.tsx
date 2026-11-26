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

import type { ModelConfig } from "@/api/models";

import { anthropicModelConfig } from "@/testing/data/models";
import { savedModelV2, toModelV2, toSupportedModelV2 } from "@/testing/data/modelsV2";
import { v2Page } from "@/testing/helpers/platformResponses";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { ModelEditPage } from "./ModelEditPage";

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const STALE_API_BASE = "https:lite-llm.example.com";

const staleAnthropic: ModelConfig = {
    ...anthropicModelConfig,
    id: "stale-anthropic",
    alias: "general",
    apiBase: STALE_API_BASE,
};

const MODEL_PATH = "*/api/v2/platform/models/:id";

function setupHandlers(stored: ModelConfig[], patchCapture?: (body: unknown) => void) {
    server.use(
        http.get(MODEL_PATH, ({ params }) => {
            const model = stored.find(m => m.id === params.id);
            return model ? HttpResponse.json({ data: toModelV2(model) }) : HttpResponse.json({ message: "Not found", errorId: "test-not-found" }, { status: 404 });
        }),
        http.get("*/api/v2/platform/models", () => HttpResponse.json(v2Page(stored.map(toModelV2)))),
        http.put("*/api/v2/platform/models/testConnection", () => HttpResponse.json({ data: { success: true, message: null, errorClass: null } })),
        http.patch(MODEL_PATH, async ({ request, params }) => {
            const body = await request.json();
            patchCapture?.(body);
            return HttpResponse.json({
                data: savedModelV2(
                    String(params.id),
                    body,
                    stored.find(m => m.id === params.id)
                ),
            });
        }),
        http.put("*/api/v2/platform/providers/:provider/models", ({ params }) => HttpResponse.json(v2Page([{ id: "claude-3-5-sonnet", label: "Claude 3.5 Sonnet" }].map(toSupportedModelV2(String(params.provider))))))
    );
}

function renderPage(path: string) {
    return render(
        <StoryProvider>
            <MemoryRouter initialEntries={[path]}>
                <Routes>
                    <Route path="/models/:id/edit" element={<ModelEditPage />} />
                </Routes>
            </MemoryRouter>
        </StoryProvider>
    );
}

describe("ModelEditPage stale apiBase surfaces as a field error", () => {
    test("shows the apiBase field in error state for a hidden-field provider carrying a stale value", async () => {
        setupHandlers([staleAnthropic]);
        renderPage("/models/stale-anthropic/edit");

        const field = (await screen.findByTestId("apiBase-input")) as HTMLInputElement;
        expect(field).toHaveValue(STALE_API_BASE);
        expect(field).toHaveAttribute("aria-invalid", "true");
        expect(screen.getByText(/Must start with/i)).toBeInTheDocument();
    });

    test("hides the apiBase field for a hidden-field provider with a well-formed stored apiBase", async () => {
        setupHandlers([{ ...staleAnthropic, apiBase: "https://api.anthropic.com" }]);
        renderPage("/models/stale-anthropic/edit");

        await waitFor(() => expect(screen.getAllByRole("combobox").length).toBeGreaterThan(0));
        expect(screen.queryByTestId("apiBase-input")).toBeNull();
    });

    test("hides the apiBase field for a hidden-field provider with no stored apiBase", async () => {
        setupHandlers([{ ...staleAnthropic, apiBase: null }]);
        renderPage("/models/stale-anthropic/edit");

        await waitFor(() => expect(screen.getAllByRole("combobox").length).toBeGreaterThan(0));
        expect(screen.queryByTestId("apiBase-input")).toBeNull();
    });

    test("latches the apiBase field visible once shown, so a correction typed into it is not unmounted mid-typing", async () => {
        setupHandlers([staleAnthropic]);
        renderPage("/models/stale-anthropic/edit");

        const user = userEvent.setup();
        const field = (await screen.findByTestId("apiBase-input")) as HTMLInputElement;

        await user.clear(field);
        await user.type(field, "https://api.anthropic.com");

        expect(screen.getByTestId("apiBase-input")).toBe(field);
        expect(field).toHaveValue("https://api.anthropic.com");
    });

    test("saving after typing a correction into the stale field sends the typed value, not an empty clear", async () => {
        let patchBody: Record<string, unknown> | null = null;
        setupHandlers([staleAnthropic], body => {
            patchBody = body as Record<string, unknown>;
        });
        renderPage("/models/stale-anthropic/edit");

        const user = userEvent.setup();
        const field = (await screen.findByTestId("apiBase-input")) as HTMLInputElement;
        await user.clear(field);
        await user.type(field, "https://api.anthropic.com");

        const saveButton = await screen.findByRole("button", { name: /save/i });
        await user.click(saveButton);

        await waitFor(() => expect(patchBody).not.toBeNull());
        expect(patchBody!.apiBase).toBe("https://api.anthropic.com");
    });

    test("clears apiBase when the user switches to another provider, so a custom proxy URL does not ride to the new one", async () => {
        const custom: ModelConfig = { ...staleAnthropic, id: "custom-with-proxy", provider: "custom", modelName: "openai/some-model", apiBase: "https://my-proxy.example.com" };
        setupHandlers([custom]);
        renderPage("/models/custom-with-proxy/edit");

        const field = (await screen.findByTestId("apiBase-input")) as HTMLInputElement;
        expect(field).toHaveValue("https://my-proxy.example.com");

        const user = userEvent.setup();
        const providerCombobox = (await screen.findAllByRole("combobox"))[0];
        await user.click(providerCombobox);
        const ollamaOption = await screen.findByText("Ollama");
        await user.click(ollamaOption);

        await waitFor(() => expect((screen.getByTestId("apiBase-input") as HTMLInputElement).value).toBe(""));
    });

    test("preserves a valid custom apiBase round-trip for a hidden-field provider (no uninvited clear)", async () => {
        let patchBody: Record<string, unknown> | null = null;
        const valid = "https://my-proxy.example.com";
        setupHandlers([{ ...staleAnthropic, apiBase: valid }], body => {
            patchBody = body as Record<string, unknown>;
        });
        renderPage("/models/stale-anthropic/edit");

        await waitFor(() => expect(screen.getAllByRole("combobox").length).toBeGreaterThan(0));
        expect(screen.queryByTestId("apiBase-input")).toBeNull();

        const user = userEvent.setup();
        const saveButton = await screen.findByRole("button", { name: /save/i });
        await user.click(saveButton);

        await waitFor(() => expect(patchBody).not.toBeNull());
        expect(patchBody!.apiBase).toBe(valid);
    });
});
