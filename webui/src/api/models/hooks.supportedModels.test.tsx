// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { useSupportedModels } from "./hooks";
import { modelsApi } from "./versioned";

const PARAMS = {
    provider: "custom",
    apiBase: "https://litellm.workshop.example.com",
    authConfig: { api_key: "sk-workshop-key" },
};

let fetchSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    fetchSpy = vi.spyOn(modelsApi, "getSupportedModels");
});

afterEach(() => {
    vi.restoreAllMocks();
});

function makeWrapper(client: QueryClient) {
    return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useSupportedModels — a probe must not be served from cache", () => {
    test("a fresh observer re-probes after an empty result", async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        const wrapper = makeWrapper(client);
        fetchSpy.mockResolvedValue([]);

        const first = renderHook(() => useSupportedModels(PARAMS), { wrapper });
        await waitFor(() => expect(first.result.current.isSuccess).toBe(true));
        expect(fetchSpy).toHaveBeenCalledTimes(1);

        first.unmount();
        fetchSpy.mockResolvedValue([{ id: "gpt-4o-mini", label: "gpt-4o-mini" }] as never);

        const second = renderHook(() => useSupportedModels(PARAMS), { wrapper });
        await waitFor(() => expect(second.result.current.data).toHaveLength(1));

        expect(fetchSpy).toHaveBeenCalledTimes(2);
    });

    test("a fresh observer re-probes after a failed result", async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        const wrapper = makeWrapper(client);
        fetchSpy.mockRejectedValue(new Error("upstream unavailable"));

        const first = renderHook(() => useSupportedModels(PARAMS), { wrapper });
        await waitFor(() => expect(first.result.current.isError).toBe(true));
        expect(fetchSpy).toHaveBeenCalledTimes(1);

        first.unmount();
        fetchSpy.mockResolvedValue([{ id: "gpt-4o-mini", label: "gpt-4o-mini" }] as never);

        const second = renderHook(() => useSupportedModels(PARAMS), { wrapper });
        await waitFor(() => expect(second.result.current.isSuccess).toBe(true));

        expect(fetchSpy).toHaveBeenCalledTimes(2);
    });
});

describe("useSupportedModels on /api/v2", () => {
    afterEach(() => vi.unstubAllGlobals());

    test("probes the provider's models route", async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        const body = { data: [{ type: "supportedModel", key: "gpt-4o", label: "GPT-4o", provider: "openai" }], meta: { pagination: { pageNumber: 1, pageSize: 100, count: 1, totalPages: 1, nextPage: null } } };
        const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } }));
        vi.stubGlobal("fetch", fetchMock);

        const { result } = renderHook(() => useSupportedModels({ provider: "openai", authConfig: { api_key: "sk" } }), { wrapper: makeWrapper(client) });

        await waitFor(() => expect(result.current.data).toEqual([{ id: "gpt-4o", label: "GPT-4o" }]));
        const urls = fetchMock.mock.calls.map(call => String(call[0]));
        expect(urls.every(url => url.startsWith("/api/v2/platform/providers/openai/models"))).toBe(true);
    });
});
