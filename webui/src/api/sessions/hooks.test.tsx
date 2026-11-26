// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { useInfiniteSessions, useRecentSessions } from "@/api/sessions/hooks";
import { sessionKeys } from "@/api/sessions/keys";
import * as sessionService from "@/api/sessions/service";

import { AuthContext, type AuthContextValue } from "@/providers/contexts/AuthContext";

import type { Session } from "@/types";

function makeAuthValue(userInfo: Record<string, unknown> | null): AuthContextValue {
    return {
        isAuthenticated: true,
        useAuthorization: true,
        login: async () => {},
        logout: () => {},
        userInfo,
    };
}

function makeSession(overrides: Partial<Session> = {}): Session {
    return {
        id: "sess-x",
        name: "Session",
        userId: "alice",
        createdAt: "2024-01-01T00:00:00Z",
        updatedAt: "2024-01-01T00:00:00Z",
        ...overrides,
    };
}

describe("useRecentSessions / useInfiniteSessions — auth user switch", () => {
    let queryClient: QueryClient;
    let recentSpy: ReturnType<typeof vi.spyOn>;
    let infiniteSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    });

    afterEach(() => {
        vi.restoreAllMocks();
        queryClient.clear();
    });

    function makeWrapper(authValue: { current: AuthContextValue }) {
        return function Wrapper({ children }: { children: React.ReactNode }) {
            return (
                <QueryClientProvider client={queryClient}>
                    <AuthContext.Provider value={authValue.current}>{children}</AuthContext.Provider>
                </QueryClientProvider>
            );
        };
    }

    test("useRecentSessions refetches under a new key when auth user switches", async () => {
        const authRef = { current: makeAuthValue({ username: "alice" }) };
        recentSpy = vi.spyOn(sessionService, "getRecentSessions").mockImplementation(async () => {
            const u = authRef.current.userInfo?.username as string | undefined;
            if (u === "alice") return [makeSession({ id: "alice-sess", name: "Alice's chat", userId: "alice" })];
            if (u === "bob") return [makeSession({ id: "bob-sess", name: "Bob's chat", userId: "bob" })];
            return [];
        });

        const wrapper = makeWrapper(authRef);

        const { result, rerender } = renderHook(() => useRecentSessions(10), { wrapper });

        await waitFor(() => expect(result.current.data).toBeDefined());
        expect(result.current.data?.[0]?.id).toBe("alice-sess");
        expect(recentSpy).toHaveBeenCalledTimes(1);

        authRef.current = makeAuthValue({ username: "bob" });
        rerender();

        await waitFor(() => expect(result.current.data?.[0]?.id).toBe("bob-sess"));
        expect(recentSpy).toHaveBeenCalledTimes(2);

        const aliceCached = queryClient.getQueryData(["sessions", "list", "recent", "alice", 10, null]) as Session[] | undefined;
        const bobCached = queryClient.getQueryData(["sessions", "list", "recent", "bob", 10, null]) as Session[] | undefined;
        expect(aliceCached?.[0]?.id).toBe("alice-sess");
        expect(bobCached?.[0]?.id).toBe("bob-sess");
    });

    test("useInfiniteSessions refetches under a new key when auth user switches", async () => {
        const authRef = { current: makeAuthValue({ username: "alice" }) };
        infiniteSpy = vi.spyOn(sessionService, "getPaginatedSessions").mockImplementation(async () => {
            const u = authRef.current.userInfo?.username as string | undefined;
            const session = u === "bob" ? makeSession({ id: "bob-sess", userId: "bob" }) : makeSession({ id: "alice-sess", userId: "alice" });
            return {
                data: [session],
                meta: { pagination: { pageNumber: 1, count: 1, pageSize: 20, nextPage: null, totalPages: 1 } },
            };
        });

        const wrapper = makeWrapper(authRef);

        const { result, rerender } = renderHook(() => useInfiniteSessions(20), { wrapper });

        await waitFor(() => expect(result.current.data).toBeDefined());
        expect(result.current.data?.pages?.[0]?.data?.[0]?.id).toBe("alice-sess");
        expect(infiniteSpy).toHaveBeenCalledTimes(1);

        authRef.current = makeAuthValue({ username: "bob" });
        rerender();

        await waitFor(() => expect(result.current.data?.pages?.[0]?.data?.[0]?.id).toBe("bob-sess"));
        expect(infiniteSpy).toHaveBeenCalledTimes(2);
    });
});

describe("session-list event coalescing", () => {
    let queryClient: QueryClient;
    let invalidateSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        vi.useFakeTimers();
        queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
        vi.spyOn(sessionService, "getRecentSessions").mockResolvedValue([]);
        vi.spyOn(sessionService, "getPaginatedSessions").mockResolvedValue({
            data: [],
            meta: { pagination: { pageNumber: 1, count: 0, pageSize: 20, nextPage: null, totalPages: 1 } },
        });
    });

    afterEach(() => {
        vi.useRealTimers();
        vi.restoreAllMocks();
        queryClient.clear();
    });

    function makeWrapper() {
        const authValue = makeAuthValue({ username: "alice" });
        return function Wrapper({ children }: { children: React.ReactNode }) {
            return (
                <QueryClientProvider client={queryClient}>
                    <AuthContext.Provider value={authValue}>{children}</AuthContext.Provider>
                </QueryClientProvider>
            );
        };
    }

    test("a burst of session events collapses to a leading + one trailing invalidation", async () => {
        renderHook(() => useRecentSessions(10), { wrapper: makeWrapper() });

        act(() => {
            globalThis.dispatchEvent(new CustomEvent("session-updated"));
        });
        expect(invalidateSpy).toHaveBeenCalledTimes(1);

        act(() => {
            globalThis.dispatchEvent(new CustomEvent("session-title-updated"));
            globalThis.dispatchEvent(new CustomEvent("new-chat-session"));
        });
        expect(invalidateSpy).toHaveBeenCalledTimes(1);

        await act(async () => {
            await vi.advanceTimersByTimeAsync(1500);
        });
        expect(invalidateSpy).toHaveBeenCalledTimes(2);

        await act(async () => {
            await vi.advanceTimersByTimeAsync(3000);
        });
        expect(invalidateSpy).toHaveBeenCalledTimes(2);
    });

    test("a lone event schedules no trailing invalidation", async () => {
        renderHook(() => useRecentSessions(10), { wrapper: makeWrapper() });

        act(() => {
            globalThis.dispatchEvent(new CustomEvent("session-updated"));
        });
        await act(async () => {
            await vi.advanceTimersByTimeAsync(3000);
        });
        expect(invalidateSpy).toHaveBeenCalledTimes(1);
    });

    test("unmount cancels the pending coalescing window", async () => {
        const { unmount } = renderHook(() => useRecentSessions(10), { wrapper: makeWrapper() });

        act(() => {
            globalThis.dispatchEvent(new CustomEvent("session-updated"));
            globalThis.dispatchEvent(new CustomEvent("session-updated"));
        });
        expect(invalidateSpy).toHaveBeenCalledTimes(1);

        unmount();
        await act(async () => {
            await vi.advanceTimersByTimeAsync(3000);
        });
        expect(invalidateSpy).toHaveBeenCalledTimes(1);
    });

    test("useInfiniteSessions invalidates the infinite family, not the lists prefix", () => {
        renderHook(() => useInfiniteSessions(20), { wrapper: makeWrapper() });

        act(() => {
            globalThis.dispatchEvent(new CustomEvent("session-updated"));
        });

        expect(invalidateSpy).toHaveBeenCalledTimes(1);
        expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: sessionKeys.infinites("alice") });
    });

    test("an unobserved infinite variant is marked stale so it refetches on remount", async () => {
        const paginatedSpy = vi.mocked(sessionService.getPaginatedSessions);
        const wrapper = makeWrapper();

        const chatTab = renderHook(() => useInfiniteSessions(20, "chat"), { wrapper });
        await act(async () => {
            await vi.advanceTimersByTimeAsync(0);
        });
        expect(paginatedSpy).toHaveBeenCalledTimes(1);
        chatTab.unmount();

        const schedulerTab = renderHook(() => useInfiniteSessions(20, "scheduler"), { wrapper });
        await act(async () => {
            await vi.advanceTimersByTimeAsync(0);
        });
        expect(paginatedSpy).toHaveBeenCalledTimes(2);
        act(() => {
            globalThis.dispatchEvent(new CustomEvent("new-chat-session"));
        });
        await act(async () => {
            await vi.advanceTimersByTimeAsync(0);
        });
        schedulerTab.unmount();

        const fetchesBeforeRemount = paginatedSpy.mock.calls.length;
        renderHook(() => useInfiniteSessions(20, "chat"), { wrapper });
        await act(async () => {
            await vi.advanceTimersByTimeAsync(0);
        });
        expect(paginatedSpy.mock.calls.length).toBeGreaterThan(fetchesBeforeRemount);
    });
});
