// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { SharedWithMeItem } from "@/types/share";

const markShareViewedService = vi.fn();
let currentUserId = "alice";

async function loadHooks() {
    vi.resetModules();
    vi.doMock("./service", () => ({ markShareViewed: markShareViewedService }));
    vi.doMock("@/hooks/useCacheUserId", () => ({ useCacheUserId: () => currentUserId }));
    return {
        hooks: await import("./hooks"),
        shareKeys: (await import("./keys")).shareKeys,
    };
}

const item = (id: string, viewedAt: string | null = null): SharedWithMeItem => ({
    id,
    title: id,
    ownerEmail: "owner@example.com",
    accessLevel: "resource_viewer",
    sharedAt: "2024-01-01T00:00:00.000000+00:00",
    shareUrl: `https://example.com/${id}`,
    sessionId: null,
    viewedAt,
});

const VIEWED_AT = "2024-02-02T00:00:00.000000+00:00";

async function markViewed(client: QueryClient, shareId: string) {
    const { hooks } = await loadHooks();
    const wrapper = ({ children }: { children: React.ReactNode }) => React.createElement(QueryClientProvider, { client }, children);
    const { result } = renderHook(() => hooks.useMarkShareViewed(), { wrapper });
    result.current.mutate(shareId);
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
}

beforeEach(() => {
    vi.clearAllMocks();
    currentUserId = "alice";
    markShareViewedService.mockResolvedValue({ viewedAt: VIEWED_AT });
});

describe("useMarkShareViewed", () => {
    test("posts to the service and patches only the opened share", async () => {
        const { shareKeys } = await loadHooks();
        const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
        client.setQueryData(shareKeys.sharedWithMe("alice"), [item("s1"), item("s2")]);

        await markViewed(client, "s1");

        expect(markShareViewedService).toHaveBeenCalledWith("s1");
        const list = client.getQueryData<SharedWithMeItem[]>(shareKeys.sharedWithMe("alice"))!;
        expect(list.find(i => i.id === "s1")?.viewedAt).toBe(VIEWED_AT);
        expect(list.find(i => i.id === "s2")?.viewedAt).toBeNull();
    });

    test("does not touch another user's cached list", async () => {
        const { shareKeys } = await loadHooks();
        const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
        client.setQueryData(shareKeys.sharedWithMe("alice"), [item("s1")]);
        client.setQueryData(shareKeys.sharedWithMe("bob"), [item("s1")]);

        await markViewed(client, "s1");

        expect(client.getQueryData<SharedWithMeItem[]>(shareKeys.sharedWithMe("bob"))![0].viewedAt).toBeNull();
    });

    test("leaves an unpopulated cache alone instead of creating an empty entry", async () => {
        const { shareKeys } = await loadHooks();
        const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });

        await markViewed(client, "s1");

        expect(client.getQueryData(shareKeys.sharedWithMe("alice"))).toBeUndefined();
    });

    test("a rejected request does not throw or corrupt the cache", async () => {
        const { hooks, shareKeys } = await loadHooks();
        markShareViewedService.mockRejectedValue(new Error("not found"));
        const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
        client.setQueryData(shareKeys.sharedWithMe("alice"), [item("s1")]);

        const wrapper = ({ children }: { children: React.ReactNode }) => React.createElement(QueryClientProvider, { client }, children);
        const { result } = renderHook(() => hooks.useMarkShareViewed(), { wrapper });
        result.current.mutate("s1");

        await waitFor(() => expect(result.current.isError).toBe(true));
        expect(client.getQueryData<SharedWithMeItem[]>(shareKeys.sharedWithMe("alice"))![0].viewedAt).toBeNull();
    });
});
