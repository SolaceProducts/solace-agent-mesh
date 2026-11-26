// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useAzureWorkloadIdentity, useCreateModel, useDeleteModel, useModelById, useModelConfigStatus, useUpdateModel } from "./hooks";
import { modelKeys } from "./keys";
import { modelsApi } from "./versioned";

const makeWrapper =
    (client: QueryClient) =>
    ({ children }: { children: React.ReactNode }) =>
        React.createElement(QueryClientProvider, { client }, children);

const expectListAndStatusInvalidated = (spy: { mock: { calls: unknown[][] } }) => {
    expect(spy).toHaveBeenCalledWith({ queryKey: modelKeys.lists() });
    expect(spy).toHaveBeenCalledWith({ queryKey: modelKeys.status() });
};

afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

const stubFetch = (body: unknown) => {
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
};
const urlsOf = (fetchMock: ReturnType<typeof stubFetch>) => fetchMock.mock.calls.map(call => String(call[0]));

const MODEL = {
    id: "m1",
    type: "model",
    alias: "a",
    provider: "openai",
    modelName: "gpt-4o",
    modelType: "chat",
    apiBase: null,
    authType: "api_key",
    authConfig: {},
    modelParams: {},
    maxInputTokens: null,
    pricing: null,
    description: null,
    createdBy: "u",
    updatedBy: "u",
    createdTime: null,
    updatedTime: null,
};

describe("model mutation cache invalidation", () => {
    it("useUpdateModel invalidates the detail(id) query on success", async () => {
        const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
        const spy = vi.spyOn(client, "invalidateQueries");
        const update = vi.spyOn(modelsApi, "update").mockResolvedValue({} as never);

        const { result } = renderHook(() => useUpdateModel(), { wrapper: makeWrapper(client) });
        await result.current.mutateAsync({ id: "m1", data: {} as never });

        await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: modelKeys.detail("m1") }));
        expectListAndStatusInvalidated(spy);
        expect(update).toHaveBeenCalledWith("m1", {});
    });

    it("useDeleteModel invalidates the detail(id) query on success", async () => {
        const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
        const spy = vi.spyOn(client, "invalidateQueries");
        const remove = vi.spyOn(modelsApi, "delete").mockResolvedValue(undefined);

        const { result } = renderHook(() => useDeleteModel(), { wrapper: makeWrapper(client) });
        await result.current.mutateAsync("m2");

        await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: modelKeys.detail("m2") }));
        expectListAndStatusInvalidated(spy);
        expect(remove).toHaveBeenCalledWith("m2");
    });

    it("useCreateModel invalidates the lists and status queries on success", async () => {
        const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
        const spy = vi.spyOn(client, "invalidateQueries");
        const create = vi.spyOn(modelsApi, "create").mockResolvedValue({} as never);

        const { result } = renderHook(() => useCreateModel(), { wrapper: makeWrapper(client) });
        await result.current.mutateAsync({ alias: "a" } as never);

        await waitFor(() => expectListAndStatusInvalidated(spy));
        expect(create).toHaveBeenCalledWith({ alias: "a" });
    });

    it("useModelById reads the model from /api/v2", async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        const fetchMock = stubFetch({ data: MODEL });

        const { result } = renderHook(() => useModelById("m1"), { wrapper: makeWrapper(client) });

        await waitFor(() => expect(result.current.isSuccess).toBe(true));
        expect(urlsOf(fetchMock)).toEqual(["/api/v2/platform/models/m1"]);
    });

    it("useModelConfigStatus reads the status from /api/v2", async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        const fetchMock = stubFetch({ data: { configured: true } });

        const { result } = renderHook(() => useModelConfigStatus(), { wrapper: makeWrapper(client) });

        await waitFor(() => expect(result.current.data).toEqual({ configured: true }));
        expect(urlsOf(fetchMock).length).toBeGreaterThan(0);
        expect(urlsOf(fetchMock).every(url => url.startsWith("/api/v2/platform/"))).toBe(true);
    });
});

describe("useAzureWorkloadIdentity", () => {
    const SUBJECT = { issuer: "https://oidc.example.com/id/abc", namespace: "sam", serviceAccount: "solace-agent-mesh", audiences: ["api://AzureADTokenExchange"] };

    it("reads the subject from /api/v2", async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        const fetchMock = stubFetch({ data: { type: "azureWorkloadIdentity", ...SUBJECT } });

        const { result } = renderHook(() => useAzureWorkloadIdentity(), { wrapper: makeWrapper(client) });

        await waitFor(() => expect(result.current.data).toEqual(SUBJECT));
        expect(urlsOf(fetchMock)).toEqual(["/api/v2/platform/models/azureWorkloadIdentity"]);
    });
});
