// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { UserContextValue } from "@/providers/contexts/UserContext";

describe("useConnectors capability gating", () => {
    let useConnectors: typeof import("./hooks").useConnectors;
    let getAllConnectors: ReturnType<typeof vi.fn>;
    let CAPABILITIES: typeof import("@/providers/contexts/UserContext").CAPABILITIES;
    let UserContext: typeof import("@/providers/contexts/UserContext").UserContext;

    beforeEach(async () => {
        vi.resetModules();
        getAllConnectors = vi.fn(async () => []);
        vi.doMock("./versioned", async importOriginal => {
            const { connectorsApi } = await importOriginal<typeof import("./versioned")>();
            return { connectorsApi: { ...connectorsApi, getAll: getAllConnectors } };
        });
        ({ CAPABILITIES, UserContext } = await import("@/providers/contexts/UserContext"));
        ({ useConnectors } = await import("./hooks"));
    });

    afterEach(() => {
        vi.doUnmock("./versioned");
    });

    const ctx = (granted: boolean): UserContextValue => ({
        userHasCapability: cap => granted && cap === CAPABILITIES.readConnectors,
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

    it("does NOT fetch when the user lacks connector:*:read (default user)", async () => {
        const { result } = renderHook(() => useConnectors(), { wrapper: wrapper(ctx(false)) });
        await new Promise(resolve => setTimeout(resolve, 20));
        expect(getAllConnectors).not.toHaveBeenCalled();
        expect(result.current.fetchStatus).toBe("idle");
    });

    it("fetches when the user has connector:*:read", async () => {
        renderHook(() => useConnectors(), { wrapper: wrapper(ctx(true)) });
        await waitFor(() => expect(getAllConnectors).toHaveBeenCalled());
    });

    it("does NOT fetch when no UserProvider is mounted (fail-closed)", async () => {
        const { result } = renderHook(() => useConnectors(), { wrapper: wrapper(null) });
        await new Promise(resolve => setTimeout(resolve, 20));
        expect(getAllConnectors).not.toHaveBeenCalled();
        expect(result.current.fetchStatus).toBe("idle");
    });
});
