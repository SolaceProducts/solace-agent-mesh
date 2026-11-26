// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, test, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { CreateModelDialog } from "./CreateModelDialog";

const MODEL_V2 = {
    id: "new-model",
    type: "model",
    alias: "decider",
    provider: "openai",
    modelName: "gpt-4o",
    apiBase: null,
    authType: "api_key",
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
const emptyPage = { data: [], meta: { pagination: { pageNumber: 1, pageSize: 100, count: 0, totalPages: 1, nextPage: null } } };

afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

describe("CreateModelDialog", () => {
    test("creates the model through v2 and reports its id", async () => {
        const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
            const url = String(input);
            const method = (init?.method ?? "GET").toUpperCase();
            let body: unknown = emptyPage;
            if (method === "POST" && url.endsWith("/models")) body = { data: MODEL_V2 };
            return new Response(JSON.stringify(body), { status: method === "POST" && url.endsWith("/models") ? 201 : 200, headers: { "Content-Type": "application/json" } });
        });
        vi.stubGlobal("fetch", fetchMock);
        const onCreated = vi.fn();
        const user = userEvent.setup();

        renderWithProviders(<CreateModelDialog open onOpenChange={vi.fn()} onCreated={onCreated} />);

        await user.type(await waitFor(() => document.querySelector('input[name="alias"]') as HTMLInputElement), "decider");
        await user.type(document.querySelector('textarea[name="description"]') as HTMLTextAreaElement, "d");
        await user.click(screen.getAllByRole("combobox")[0]);
        await user.click(await screen.findByText("OpenAI"));
        await waitFor(() => expect(document.querySelector('input[name="apiKey"]')).toBeTruthy());
        await user.type(document.querySelector('input[name="apiKey"]') as HTMLInputElement, "sk-test");
        await user.type(await screen.findByPlaceholderText(/Type or select a model/i), "gpt-4o");
        await user.click(screen.getByRole("button", { name: "Create" }));

        await waitFor(() => expect(onCreated).toHaveBeenCalledWith("new-model"));
        const posts = fetchMock.mock.calls.filter(call => (call[1]?.method ?? "GET").toUpperCase() === "POST").map(call => String(call[0]));
        expect(posts).toEqual([expect.stringMatching(/\/api\/v2\/platform\/models$/)]);
    });
});
