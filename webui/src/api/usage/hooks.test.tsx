// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";

import { v2Error, v2Page } from "@/testing/helpers/platformResponses";

import { useSelfUsage, useUsageDetail, useUsageList, useUsageSummary } from "./hooks";
import { usageKeys } from "./keys";

const platform = { get: vi.spyOn(api.platform, "get") };
afterEach(() => {
    platform.get.mockReset();
});

const failWith = (status: number) => async () => v2Error(status, `HTTP ${status}`);
const succeed = async (path: unknown) => Response.json(String(path).includes("pageNumber=") ? v2Page([]) : { data: {} });

describe("usage hooks retry", () => {
    const WINDOW = { period: "this_month" } as const;

    function render(client: QueryClient) {
        const wrapper = ({ children }: { children: ReactNode }) => (
            <OpenFeatureTestProvider flagValueMap={{}}>
                <QueryClientProvider client={client}>{children}</QueryClientProvider>
            </OpenFeatureTestProvider>
        );
        return renderHook(() => useUsageSummary(WINDOW), { wrapper }).result;
    }

    beforeEach(() => {
        platform.get.mockReset();
    });

    test("fails a 4xx or a slow read after one request, retries another 5xx by the client's default", async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: 2, retryDelay: 0 } } });
        platform.get.mockImplementation(failWith(403));
        const forbidden = render(client);
        await waitFor(() => expect(forbidden.current.isError).toBe(true));
        expect(platform.get).toHaveBeenCalledTimes(1);

        client.clear();
        platform.get.mockReset().mockImplementation(failWith(503));
        const slow = render(client);
        await waitFor(() => expect(slow.current.isError).toBe(true));
        expect(platform.get).toHaveBeenCalledTimes(1);

        client.clear();
        platform.get.mockReset().mockImplementation(failWith(500));
        const failed = render(client);
        await waitFor(() => expect(failed.current.isError).toBe(true));
        expect(platform.get).toHaveBeenCalledTimes(3);
    });

    test("takes a key's own defaults over the client's", async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: 3, retryDelay: 0 } } });
        client.setQueryDefaults(usageKeys.all, { retry: 1, retryDelay: 0 });
        platform.get.mockImplementation(failWith(429));
        const limited = render(client);
        await waitFor(() => expect(limited.current.isError).toBe(true));
        expect(platform.get).toHaveBeenCalledTimes(2);
    });
});

describe("usage hooks freshness", () => {
    const WINDOW = { period: "this_month" } as const;

    beforeEach(() => {
        platform.get.mockReset().mockImplementation(succeed);
    });

    test.each<[string, () => { isSuccess: boolean; isFetching: boolean }]>([
        ["self", () => useSelfUsage(WINDOW)],
        ["summary", () => useUsageSummary(WINDOW)],
        ["list", () => useUsageList("users", { window: WINDOW, pageNumber: 1, pageSize: 25, sortBy: "cost", sortDir: "desc" })],
        ["detail", () => useUsageDetail("users", { id: "0190f1a2-3b4c-7d5e-8f60-718293a4b5c6" }, WINDOW)],
    ])("refetches %s usage on every mount despite the app's five-minute default", async (_name, hook) => {
        const client = new QueryClient({ defaultOptions: { queries: { staleTime: 1000 * 60 * 5 } } });
        const wrapper = ({ children }: { children: ReactNode }) => (
            <OpenFeatureTestProvider flagValueMap={{}}>
                <QueryClientProvider client={client}>{children}</QueryClientProvider>
            </OpenFeatureTestProvider>
        );

        const first = renderHook(hook, { wrapper });
        await waitFor(() => expect(first.result.current.isSuccess).toBe(true));
        first.unmount();

        const second = renderHook(hook, { wrapper });
        await waitFor(() => expect(platform.get).toHaveBeenCalledTimes(2));
        await waitFor(() => expect(second.result.current.isFetching).toBe(false));
    });
});
