// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { UserContextValue } from "@/providers/contexts/UserContext";

describe("useAgents capability gating", () => {
    let useAgents: typeof import("./hooks").useAgents;
    let getAllAgents: ReturnType<typeof vi.fn>;
    let CAPABILITIES: typeof import("@/providers/contexts/UserContext").CAPABILITIES;
    let UserContext: typeof import("@/providers/contexts/UserContext").UserContext;

    beforeEach(async () => {
        vi.resetModules();
        getAllAgents = vi.fn(async () => []);
        vi.doMock("./versioned", async importOriginal => {
            const { agentsApi } = await importOriginal<typeof import("./versioned")>();
            return { agentsApi: { ...agentsApi, getAll: getAllAgents } };
        });
        ({ CAPABILITIES, UserContext } = await import("@/providers/contexts/UserContext"));
        ({ useAgents } = await import("./hooks"));
    });

    afterEach(() => {
        vi.doUnmock("./versioned");
    });

    const ctx = (granted: boolean): UserContextValue => ({
        userHasCapability: cap => granted && cap === CAPABILITIES.readAgents,
        CAPABILITIES,
        capabilitiesLoaded: true,
        scopes: [],
        hasScope: () => false,
        isSuperAdmin: false,
    });

    const wrapper = (value: UserContextValue | null) => {
        const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        return ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{value ? <UserContext.Provider value={value}>{children}</UserContext.Provider> : children}</QueryClientProvider>;
    };

    it("does NOT fetch when the user lacks agent_builder:*:read (default user)", async () => {
        const { result } = renderHook(() => useAgents(), { wrapper: wrapper(ctx(false)) });
        await new Promise(resolve => setTimeout(resolve, 20));
        expect(getAllAgents).not.toHaveBeenCalled();
        expect(result.current.fetchStatus).toBe("idle");
    });

    it("fetches when the user has agent_builder:*:read", async () => {
        renderHook(() => useAgents(), { wrapper: wrapper(ctx(true)) });
        await waitFor(() => expect(getAllAgents).toHaveBeenCalled());
    });

    it("does NOT fetch when no UserProvider is mounted (fail-closed)", async () => {
        const { result } = renderHook(() => useAgents(), { wrapper: wrapper(null) });
        await new Promise(resolve => setTimeout(resolve, 20));
        expect(getAllAgents).not.toHaveBeenCalled();
        expect(result.current.fetchStatus).toBe("idle");
    });

    it("re-reads a fresh cached list on mount when asked to", async () => {
        const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
        const shared = ({ children }: { children: ReactNode }) => (
            <QueryClientProvider client={qc}>
                <UserContext.Provider value={ctx(true)}>{children}</UserContext.Provider>
            </QueryClientProvider>
        );
        renderHook(() => useAgents(), { wrapper: shared });
        await waitFor(() => expect(getAllAgents).toHaveBeenCalledTimes(1));

        renderHook(() => useAgents(), { wrapper: shared });
        await new Promise(resolve => setTimeout(resolve, 20));
        expect(getAllAgents).toHaveBeenCalledTimes(1);

        renderHook(() => useAgents({ refetchOnMount: "always" }), { wrapper: shared });
        await waitFor(() => expect(getAllAgents).toHaveBeenCalledTimes(2));
    });

    it("decides whether to keep polling from the cached agents", async () => {
        const agents = [{ name: "a" }];
        getAllAgents.mockResolvedValue(agents);
        const keepPolling = vi.fn(() => false as const);
        renderHook(() => useAgents({ refetchInterval: keepPolling }), { wrapper: wrapper(ctx(true)) });
        await waitFor(() => expect(keepPolling).toHaveBeenCalledWith(agents));
    });

    it("polls on a fixed interval when given a number", async () => {
        renderHook(() => useAgents({ refetchInterval: 20 }), { wrapper: wrapper(ctx(true)) });
        await waitFor(() => expect(getAllAgents.mock.calls.length).toBeGreaterThanOrEqual(2));
    });
});
