// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { UserContextValue } from "@/providers/contexts/UserContext";

describe("toolset hooks capability gating", () => {
    let useBuiltinToolsets: typeof import("./hooks").useBuiltinToolsets;
    let useCustomToolsets: typeof import("./hooks").useCustomToolsets;
    let getBuiltinToolsets: ReturnType<typeof vi.fn>;
    let getCustomToolsets: ReturnType<typeof vi.fn>;
    let CAPABILITIES: typeof import("@/providers/contexts/UserContext").CAPABILITIES;
    let UserContext: typeof import("@/providers/contexts/UserContext").UserContext;

    beforeEach(async () => {
        vi.resetModules();
        getBuiltinToolsets = vi.fn(async () => []);
        getCustomToolsets = vi.fn(async () => []);
        const toolsets = { getBuiltin: getBuiltinToolsets, getCustom: getCustomToolsets, getBuildTarget: vi.fn() };
        vi.doMock("./versioned", () => ({ toolsetsApi: toolsets }));
        ({ CAPABILITIES, UserContext } = await import("@/providers/contexts/UserContext"));
        ({ useBuiltinToolsets, useCustomToolsets } = await import("./hooks"));
    });

    afterEach(() => {
        vi.doUnmock("./versioned");
    });

    const ctx = (granted: boolean): UserContextValue => ({
        userHasCapability: cap => granted && cap === CAPABILITIES.readToolsets,
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

    it("useBuiltinToolsets does NOT fetch without toolset:*:read", async () => {
        const { result } = renderHook(() => useBuiltinToolsets(), { wrapper: wrapper(ctx(false)) });
        await new Promise(resolve => setTimeout(resolve, 20));
        expect(getBuiltinToolsets).not.toHaveBeenCalled();
        expect(result.current.fetchStatus).toBe("idle");
    });

    it("useBuiltinToolsets fetches with toolset:*:read", async () => {
        renderHook(() => useBuiltinToolsets(), { wrapper: wrapper(ctx(true)) });
        await waitFor(() => expect(getBuiltinToolsets).toHaveBeenCalled());
    });

    it("useCustomToolsets does NOT fetch without toolset:*:read", async () => {
        const { result } = renderHook(() => useCustomToolsets(), { wrapper: wrapper(ctx(false)) });
        await new Promise(resolve => setTimeout(resolve, 20));
        expect(getCustomToolsets).not.toHaveBeenCalled();
        expect(result.current.fetchStatus).toBe("idle");
    });

    it("does NOT fetch when no UserProvider is mounted (fail-closed)", async () => {
        const { result } = renderHook(() => useBuiltinToolsets(), { wrapper: wrapper(null) });
        await new Promise(resolve => setTimeout(resolve, 20));
        expect(getBuiltinToolsets).not.toHaveBeenCalled();
        expect(result.current.fetchStatus).toBe("idle");
    });
});
