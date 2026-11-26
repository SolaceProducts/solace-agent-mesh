// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { UserContextValue } from "@/providers/contexts/UserContext";

describe("useAllArtifacts capability gating", () => {
    let useAllArtifacts: typeof import("./hooks").useAllArtifacts;
    let getAllArtifacts: ReturnType<typeof vi.fn>;
    let CAPABILITIES: typeof import("@/providers/contexts/UserContext").CAPABILITIES;
    let UserContext: typeof import("@/providers/contexts/UserContext").UserContext;

    beforeEach(async () => {
        vi.resetModules();
        getAllArtifacts = vi.fn(async () => ({ artifacts: [], totalCount: 0, hasMore: false, nextPage: null }));
        vi.doMock("./service", () => ({ getAllArtifacts }));
        ({ CAPABILITIES, UserContext } = await import("@/providers/contexts/UserContext"));
        ({ useAllArtifacts } = await import("./hooks"));
    });

    afterEach(() => {
        vi.doUnmock("./service");
    });

    const ctx = (granted: boolean): UserContextValue => ({
        userHasCapability: cap => granted && cap === CAPABILITIES.readArtifacts,
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

    it("does NOT fetch when the user lacks readArtifacts", async () => {
        const { result } = renderHook(() => useAllArtifacts(), { wrapper: wrapper(ctx(false)) });
        await new Promise(resolve => setTimeout(resolve, 20));
        expect(getAllArtifacts).not.toHaveBeenCalled();
        expect(result.current.fetchStatus).toBe("idle");
    });

    it("fetches when the user has readArtifacts", async () => {
        renderHook(() => useAllArtifacts(), { wrapper: wrapper(ctx(true)) });
        await waitFor(() => expect(getAllArtifacts).toHaveBeenCalled());
    });

    it("does NOT fetch when no UserProvider is mounted (fail-closed)", async () => {
        const { result } = renderHook(() => useAllArtifacts(), { wrapper: wrapper(null) });
        await new Promise(resolve => setTimeout(resolve, 20));
        expect(getAllArtifacts).not.toHaveBeenCalled();
        expect(result.current.fetchStatus).toBe("idle");
    });
});
