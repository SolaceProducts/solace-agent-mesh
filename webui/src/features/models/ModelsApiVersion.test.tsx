// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, test, vi } from "vitest";

import { renderWithProviders } from "@/testing/helpers/renderWithProviders";

import { ModelsView } from "./ModelsView";

const MODEL_ID = "model-under-test";

const MODEL_V2 = {
    id: MODEL_ID,
    type: "model",
    alias: "decider",
    provider: "openai",
    modelName: "gpt-4o",
    apiBase: null,
    authType: "api_key",
    authConfig: {},
    modelParams: {},
    maxInputTokens: null,
    description: null,
    createdBy: "u",
    updatedBy: "u",
    modelType: "chat",
    pricing: null,
    createdTime: null,
    updatedTime: null,
};

const DEPENDENT_V2 = { type: "modelDependent", key: "agent-1", name: "Support Bot", dependentType: "agent", deploymentStatus: "deployed" };

const onePage = (data: unknown[]) => ({ data, meta: { pagination: { pageNumber: 1, pageSize: 100, count: data.length, totalPages: 1, nextPage: null } } });

function stubFetch(bodyFor: (url: string) => unknown) {
    const fetchMock = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify(bodyFor(String(input))), { headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
}

const urlsOf = (fetchMock: ReturnType<typeof stubFetch>) => fetchMock.mock.calls.map(call => String(call[0]));

const urlsOfMethod = (fetchMock: ReturnType<typeof stubFetch>, method: string) => fetchMock.mock.calls.filter(call => (call[1]?.method ?? "GET").toUpperCase() === method).map(call => String(call[0]));

const PREFIX = "/api/v2/platform";

afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

describe("deleting a model", () => {
    test("lists its dependents and deletes it through v2 in one render", async () => {
        const fetchMock = stubFetch(url => (url.includes("/dependents") ? onePage([DEPENDENT_V2]) : onePage([MODEL_V2])));
        const user = userEvent.setup();

        renderWithProviders(<ModelsView />, {
            userContextValues: { userHasCapability: () => true },
        });

        await user.click(await screen.findByRole("button", { name: "Actions" }));
        await user.click(await screen.findByText("Delete"));

        const dialog = within(document.body);
        expect(await dialog.findByRole("button", { name: /Support Bot/ })).toBeInTheDocument();
        await user.type(dialog.getByTestId("modelDeleteConfirmInput"), "decider");
        await user.click(dialog.getByRole("button", { name: /^delete$/i }));

        await waitFor(() => expect(urlsOfMethod(fetchMock, "DELETE")).not.toHaveLength(0));
        const dependentsUrls = urlsOf(fetchMock).filter(url => url.includes("/dependents"));
        expect(dependentsUrls).not.toHaveLength(0);
        expect(dependentsUrls.every(url => url.includes(`${PREFIX}/models/${MODEL_ID}/dependents`))).toBe(true);
        expect(urlsOfMethod(fetchMock, "DELETE").every(url => url.includes(`${PREFIX}/models/${MODEL_ID}`))).toBe(true);
    });
});
