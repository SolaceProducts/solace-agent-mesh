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

import { mockModelConfigs } from "@/testing/data/models";
import { toModelV2, toSupportedModelV2 } from "@/testing/data/modelsV2";
import { v2Page } from "@/testing/helpers/platformResponses";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { ModelEditPage } from "./ModelEditPage";

const server = setupServer();

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const customModel: ModelConfig = {
    ...mockModelConfigs[0],
    id: "custom-model",
    alias: "general",
    provider: "custom",
    modelName: "openai/some-model",
    apiBase: "https://good.example.com",
};

function setupHandlers(stored: ModelConfig[], providerModels: { data: Array<{ id: string; label: string }> }) {
    server.use(
        http.get("*/api/v2/platform/models/:id", ({ params }) => {
            const model = stored.find(m => m.id === params.id);
            return model ? HttpResponse.json({ data: toModelV2(model) }) : HttpResponse.json({ message: "Not found", errorId: "test-not-found" }, { status: 404 });
        }),
        http.get("*/api/v2/platform/models", () => HttpResponse.json(v2Page(stored.map(toModelV2)))),
        http.put("*/api/v2/platform/providers/:provider/models", ({ params }) => HttpResponse.json(v2Page(providerModels.data.map(toSupportedModelV2(String(params.provider))))))
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

describe("ModelEditPage apiBase inline validation", () => {
    test("shows the format error on blur after bad input, hides it while typing, re-shows on next blur", async () => {
        setupHandlers([customModel], { data: [{ id: "openai/some-model", label: "openai/some-model" }] });
        renderPage("/models/custom-model/edit");

        const user = userEvent.setup();
        await waitFor(() => expect(document.querySelector('input[name="apiBase"]')).not.toBeNull());
        const field = document.querySelector('input[name="apiBase"]') as HTMLInputElement;

        await user.clear(field);
        await user.type(field, "fadsfafdasdfasdf");
        expect(screen.queryByText(/Must start with/i)).toBeNull();

        field.blur();
        await waitFor(() => expect(screen.getByText(/Must start with/i)).toBeInTheDocument());

        await user.type(field, "x");
        expect(screen.queryByText(/Must start with/i)).toBeNull();

        field.blur();
        await waitFor(() => expect(screen.getByText(/Must start with/i)).toBeInTheDocument());
    });

    test("clears the error on blur once the value becomes a well-formed URL", async () => {
        setupHandlers([customModel], { data: [{ id: "openai/some-model", label: "openai/some-model" }] });
        renderPage("/models/custom-model/edit");

        const user = userEvent.setup();
        await waitFor(() => expect(document.querySelector('input[name="apiBase"]')).not.toBeNull());
        const field = document.querySelector('input[name="apiBase"]') as HTMLInputElement;

        await user.clear(field);
        await user.type(field, "nope");
        field.blur();
        await waitFor(() => expect(screen.getByText(/Must start with/i)).toBeInTheDocument());

        await user.clear(field);
        await user.type(field, "https://api.example.com");
        field.blur();
        await waitFor(() => expect(screen.queryByText(/Must start with/i)).toBeNull());
    });

    test("the format hint is the structural shape, with no example URL", async () => {
        setupHandlers([customModel], { data: [{ id: "openai/some-model", label: "openai/some-model" }] });
        renderPage("/models/custom-model/edit");

        const user = userEvent.setup();
        await waitFor(() => expect(document.querySelector('input[name="apiBase"]')).not.toBeNull());
        const field = document.querySelector('input[name="apiBase"]') as HTMLInputElement;

        await user.clear(field);
        await user.type(field, "nope");
        field.blur();
        const hint = await screen.findByText(/Must start with/i);
        expect(hint).not.toHaveTextContent(/https:\/\/api\.example\.com/i);
        expect(hint).not.toHaveTextContent(/For example/i);
    });

    test("typing the first character of a URL into an empty field shows no error before blur", async () => {
        setupHandlers([customModel], { data: [{ id: "openai/some-model", label: "openai/some-model" }] });
        renderPage("/models/custom-model/edit");

        const user = userEvent.setup();
        await waitFor(() => expect(document.querySelector('input[name="apiBase"]')).not.toBeNull());
        const field = document.querySelector('input[name="apiBase"]') as HTMLInputElement;

        await user.clear(field);
        await user.type(field, "h");

        await expect(screen.findByText(/Must start with/i, {}, { timeout: 300 })).rejects.toThrow();
    });
});
