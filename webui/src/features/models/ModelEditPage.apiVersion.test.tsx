// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Route, Routes } from "react-router-dom";
import { afterEach, describe, expect, test, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ModelEditPage } from "./ModelEditPage";

const MODEL_ID = "model-under-test";
const MODEL_V2 = {
    id: MODEL_ID,
    type: "model",
    alias: "decider",
    provider: "openai",
    modelName: "gpt-4o",
    apiBase: null,
    authType: "apikey",
    authConfig: {},
    modelParams: {},
    maxInputTokens: null,
    description: "d",
    createdBy: "u",
    updatedBy: "u",
    modelType: "chat",
    pricing: null,
    createdTime: null,
    updatedTime: null,
};
const PREFIX = "/api/v2/platform";
const emptyPage = { data: [], meta: { pagination: { pageNumber: 1, pageSize: 100, count: 0, totalPages: 1, nextPage: null } } };

afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

describe("editing a model", () => {
    test("reads, probes and updates the model through v2", async () => {
        const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
            const url = String(input);
            const method = (init?.method ?? "GET").toUpperCase();
            let body: unknown;
            if (url.includes("/providers/")) body = emptyPage;
            else if (url.includes("testConnection")) body = { data: { success: true, message: null, errorClass: null } };
            else if (method === "PATCH" || method === "GET") body = { data: MODEL_V2 };
            else body = {};
            return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
        });
        vi.stubGlobal("fetch", fetchMock);
        const user = userEvent.setup();

        renderWithProviders(
            <Routes>
                <Route path="/models/:id/edit" element={<ModelEditPage />} />
                <Route path="/models" element={<div>Models List</div>} />
            </Routes>,
            { initialPath: `/models/${MODEL_ID}/edit` }
        );
        await screen.findAllByText("Edit decider");
        await user.click(screen.getByRole("button", { name: /Save/i }));

        const requests = () => fetchMock.mock.calls.map(call => ({ url: String(call[0]), method: (call[1]?.method ?? "GET").toUpperCase() }));
        await waitFor(() => expect(requests().some(r => r.method === "PATCH")).toBe(true));
        expect(
            requests()
                .find(r => r.method === "PATCH")
                ?.url.endsWith(`${PREFIX}/models/${MODEL_ID}`)
        ).toBe(true);
        expect(requests().some(r => r.method === "GET" && r.url.endsWith(`${PREFIX}/models/${MODEL_ID}`))).toBe(true);
        expect(requests().some(r => r.url.includes(`${PREFIX}/providers/openai/models`))).toBe(true);
    });
});
