// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, test, vi } from "vitest";

import { useSessionContextUsage } from "./hooks";

const USAGE = { currentContextTokens: 1000, maxInputTokens: 4000, usagePercentage: 25, model: "gpt-4o" };
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
    maxInputTokens: 10000,
    pricing: null,
    description: null,
    createdBy: "u",
    updatedBy: "u",
    createdTime: null,
    updatedTime: null,
};

afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

describe("useSessionContextUsage", () => {
    test("takes the configured context limit from the platform model list", async () => {
        const fetchMock = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
            const url = String(input);
            const body = url.includes("/contextUsage") ? USAGE : { data: [MODEL], meta: { pagination: { pageNumber: 1, pageSize: 100, count: 1, totalPages: 1, nextPage: null } } };
            return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
        });
        vi.stubGlobal("fetch", fetchMock);
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;

        const { result } = renderHook(() => useSessionContextUsage("s1"), { wrapper });

        await waitFor(() => expect(result.current.data?.maxInputTokens).toBe(10000));
        const modelUrls = fetchMock.mock.calls.map(call => String(call[0])).filter(url => url.includes("/platform/models"));
        expect(modelUrls).not.toHaveLength(0);
        expect(modelUrls.every(url => url.startsWith("/api/v2/platform/models"))).toBe(true);
    });
});
