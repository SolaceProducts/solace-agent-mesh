// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";

import { CONSENT_CHANNEL_NAME, useProductAnalyticsConsent, useUpdateProductAnalyticsConsent } from "./hooks";
import { productAnalyticsKeys } from "./keys";
import type { ConsentResponse } from "./types";

let getSpy: ReturnType<typeof vi.spyOn>;
let putSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    vi.restoreAllMocks();
    getSpy = vi.spyOn(api.webui, "get");
    putSpy = vi.spyOn(api.webui, "put");
});

function buildClient() {
    return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
}

function wrapper(client: QueryClient) {
    return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useProductAnalyticsConsent", () => {
    test("reads the consent endpoint", async () => {
        getSpy.mockResolvedValue({ state: "granted" });
        const { result } = renderHook(() => useProductAnalyticsConsent(), { wrapper: wrapper(buildClient()) });

        await waitFor(() => expect(result.current.data).toEqual({ state: "granted" }));
        expect(getSpy).toHaveBeenCalledWith("/api/v1/productAnalytics/consent");
    });

    test("does not call the endpoint when disabled", () => {
        renderHook(() => useProductAnalyticsConsent({ enabled: false }), { wrapper: wrapper(buildClient()) });
        expect(getSpy).not.toHaveBeenCalled();
    });
});

describe("useUpdateProductAnalyticsConsent", () => {
    test("PUTs the decision and writes the response into the consent cache", async () => {
        putSpy.mockResolvedValue({ state: "granted" });
        const client = buildClient();

        const { result } = renderHook(() => useUpdateProductAnalyticsConsent(), { wrapper: wrapper(client) });
        await act(async () => {
            await result.current.mutateAsync(true);
        });

        expect(putSpy).toHaveBeenCalledWith("/api/v1/productAnalytics/consent", { granted: true });
        expect(client.getQueryData(productAnalyticsKeys.consent())).toEqual({ state: "granted" });
    });

    test("seeds the cache from the PUT response rather than depending on a refetch", async () => {
        getSpy.mockResolvedValueOnce({ state: "granted" }).mockRejectedValue(new Error("gateway down"));
        putSpy.mockResolvedValue({ state: "denied" });
        const client = buildClient();

        const { result } = renderHook(() => ({ read: useProductAnalyticsConsent(), update: useUpdateProductAnalyticsConsent() }), { wrapper: wrapper(client) });
        await waitFor(() => expect(result.current.read.data).toEqual({ state: "granted" }));

        await act(async () => {
            await result.current.update.mutateAsync(false);
        });

        await waitFor(() => expect(client.getQueryData(productAnalyticsKeys.consent())).toEqual({ state: "denied" }));
    });

    test("sends granted:false when the user opts out", async () => {
        putSpy.mockResolvedValue({ state: "denied" });
        const { result } = renderHook(() => useUpdateProductAnalyticsConsent(), { wrapper: wrapper(buildClient()) });

        await act(async () => {
            await result.current.mutateAsync(false);
        });
        expect(putSpy).toHaveBeenCalledWith("/api/v1/productAnalytics/consent", { granted: false });
    });

    test("broadcasts the decision to other tabs on success", async () => {
        putSpy.mockResolvedValue({ state: "denied" });
        const { result } = renderHook(() => useUpdateProductAnalyticsConsent(), { wrapper: wrapper(buildClient()) });

        const otherTab = new BroadcastChannel(CONSENT_CHANNEL_NAME);
        const received = new Promise<ConsentResponse>(resolve => {
            otherTab.addEventListener("message", event => resolve(event.data as ConsentResponse), { once: true });
        });

        await act(async () => {
            await result.current.mutateAsync(false);
        });

        await expect(received).resolves.toEqual({ state: "denied" });
        otherTab.close();
    });

    test("applies a decision broadcast from another tab without a refetch", async () => {
        getSpy.mockResolvedValue({ state: "granted" });
        const client = buildClient();
        const { result } = renderHook(() => useProductAnalyticsConsent(), { wrapper: wrapper(client) });
        await waitFor(() => expect(result.current.data).toEqual({ state: "granted" }));
        const readsBeforeBroadcast = getSpy.mock.calls.length;

        const otherTab = new BroadcastChannel(CONSENT_CHANNEL_NAME);
        act(() => {
            otherTab.postMessage({ state: "denied" } satisfies ConsentResponse);
        });

        await waitFor(() => expect(client.getQueryData(productAnalyticsKeys.consent())).toEqual({ state: "denied" }));
        expect(getSpy.mock.calls.length).toBe(readsBeforeBroadcast);
        otherTab.close();
    });

    test("a slow in-flight refetch does not clobber a fresher broadcast", async () => {
        getSpy.mockResolvedValueOnce({ state: "granted" });
        const client = buildClient();
        const { result } = renderHook(() => useProductAnalyticsConsent(), { wrapper: wrapper(client) });
        await waitFor(() => expect(result.current.data).toEqual({ state: "granted" }));

        let resolveRefetch: (value: ConsentResponse) => void = () => {};
        getSpy.mockReturnValueOnce(new Promise(resolve => (resolveRefetch = resolve)));

        let refetchDone: Promise<unknown> = Promise.resolve();
        act(() => {
            refetchDone = result.current.refetch();
        });

        const otherTab = new BroadcastChannel(CONSENT_CHANNEL_NAME);
        act(() => {
            otherTab.postMessage({ state: "denied" } satisfies ConsentResponse);
        });
        await waitFor(() => expect(client.getQueryData(productAnalyticsKeys.consent())).toEqual({ state: "denied" }));

        await act(async () => {
            resolveRefetch({ state: "granted" });
            await refetchDone;
        });

        expect(client.getQueryData(productAnalyticsKeys.consent())).toEqual({ state: "denied" });
        otherTab.close();
    });
});
