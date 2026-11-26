// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { UserContextValue } from "@/providers/contexts/UserContext";

describe("remote-agent hooks capability gating", () => {
    let useRemoteAgents: typeof import("./hooks").useRemoteAgents;
    let useRemoteAgentCards: typeof import("./hooks").useRemoteAgentCards;
    let getAllRemoteAgents: ReturnType<typeof vi.fn>;
    let CAPABILITIES: typeof import("@/providers/contexts/UserContext").CAPABILITIES;
    let UserContext: typeof import("@/providers/contexts/UserContext").UserContext;

    beforeEach(async () => {
        vi.resetModules();
        getAllRemoteAgents = vi.fn(async () => []);
        vi.doMock("./versioned", async importOriginal => {
            const { remoteAgentsApi } = await importOriginal<typeof import("./versioned")>();
            return { remoteAgentsApi: { ...remoteAgentsApi, getAll: getAllRemoteAgents } };
        });
        ({ CAPABILITIES, UserContext } = await import("@/providers/contexts/UserContext"));
        ({ useRemoteAgents, useRemoteAgentCards } = await import("./hooks"));
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

    it("useRemoteAgents does NOT fetch without agent_builder:*:read (default user)", async () => {
        const { result } = renderHook(() => useRemoteAgents(), { wrapper: wrapper(ctx(false)) });
        await new Promise(resolve => setTimeout(resolve, 20));
        expect(getAllRemoteAgents).not.toHaveBeenCalled();
        expect(result.current.fetchStatus).toBe("idle");
    });

    it("useRemoteAgentCards does NOT fetch without agent_builder:*:read (the chat-load path)", async () => {
        const { result } = renderHook(() => useRemoteAgentCards(), { wrapper: wrapper(ctx(false)) });
        await new Promise(resolve => setTimeout(resolve, 20));
        expect(getAllRemoteAgents).not.toHaveBeenCalled();
        expect(result.current.fetchStatus).toBe("idle");
    });

    it("fetches when the user has agent_builder:*:read", async () => {
        renderHook(() => useRemoteAgents(), { wrapper: wrapper(ctx(true)) });
        await waitFor(() => expect(getAllRemoteAgents).toHaveBeenCalled());
    });

    it("does NOT fetch when no UserProvider is mounted (fail-closed)", async () => {
        const { result } = renderHook(() => useRemoteAgents(), { wrapper: wrapper(null) });
        await new Promise(resolve => setTimeout(resolve, 20));
        expect(getAllRemoteAgents).not.toHaveBeenCalled();
        expect(result.current.fetchStatus).toBe("idle");
    });
});
