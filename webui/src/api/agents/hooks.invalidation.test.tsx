// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { UserContextValue } from "@/providers/contexts/UserContext";

describe("agent writes refresh the lists that embed agent bindings", () => {
    let useCreateAgentMutation: typeof import("./hooks").useCreateAgentMutation;
    let useBuiltinToolsets: typeof import("@/api/toolsets/hooks").useBuiltinToolsets;
    let useCustomToolsets: typeof import("@/api/toolsets/hooks").useCustomToolsets;
    let getBuiltinToolsets: ReturnType<typeof vi.fn>;
    let getCustomToolsets: ReturnType<typeof vi.fn>;
    let createAgent: ReturnType<typeof vi.fn>;
    let CAPABILITIES: typeof import("@/providers/contexts/UserContext").CAPABILITIES;
    let UserContext: typeof import("@/providers/contexts/UserContext").UserContext;

    beforeEach(async () => {
        vi.resetModules();
        getBuiltinToolsets = vi.fn(async () => []);
        getCustomToolsets = vi.fn(async () => []);
        createAgent = vi.fn(async () => ({ id: "agent-1" }));
        const toolsets = { getBuiltin: getBuiltinToolsets, getCustom: getCustomToolsets, getBuildTarget: vi.fn() };
        vi.doMock("@/api/toolsets/versioned", () => ({ toolsetsApi: toolsets }));
        vi.doMock("./versioned", async importOriginal => {
            const { agentsApi } = await importOriginal<typeof import("./versioned")>();
            return { agentsApi: { ...agentsApi, create: createAgent, getAll: vi.fn(async () => []) } };
        });
        ({ CAPABILITIES, UserContext } = await import("@/providers/contexts/UserContext"));
        ({ useBuiltinToolsets, useCustomToolsets } = await import("@/api/toolsets/hooks"));
        ({ useCreateAgentMutation } = await import("./hooks"));
    });

    afterEach(() => {
        vi.doUnmock("@/api/toolsets/versioned");
        vi.doUnmock("./versioned");
        vi.restoreAllMocks();
    });

    const ctx = (): UserContextValue => ({
        userHasCapability: cap => cap === CAPABILITIES.readToolsets,
        CAPABILITIES,
        capabilitiesLoaded: true,
        scopes: [],
        hasScope: () => false,
        isSuperAdmin: false,
    });

    const wrapper = (qc: QueryClient) =>
        function Wrapper({ children }: { children: ReactNode }) {
            return (
                <QueryClientProvider client={qc}>
                    <UserContext.Provider value={ctx()}>{children}</UserContext.Provider>
                </QueryClientProvider>
            );
        };

    it("refetches both toolset lists after a successful create", async () => {
        const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
        const { result } = renderHook(() => ({ builtin: useBuiltinToolsets(), custom: useCustomToolsets(), create: useCreateAgentMutation() }), { wrapper: wrapper(qc) });

        await waitFor(() => expect(getBuiltinToolsets).toHaveBeenCalledTimes(1));
        await waitFor(() => expect(getCustomToolsets).toHaveBeenCalledTimes(1));

        result.current.create.mutate({ name: "DocsFetcher" });

        await waitFor(() => expect(result.current.create.isSuccess).toBe(true));
        await waitFor(() => expect(getBuiltinToolsets).toHaveBeenCalledTimes(2));
        await waitFor(() => expect(getCustomToolsets).toHaveBeenCalledTimes(2));
    });

    it("does not refetch when the create fails", async () => {
        createAgent.mockRejectedValueOnce(new Error("boom"));
        const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
        const { result } = renderHook(() => ({ builtin: useBuiltinToolsets(), custom: useCustomToolsets(), create: useCreateAgentMutation() }), { wrapper: wrapper(qc) });

        await waitFor(() => expect(getBuiltinToolsets).toHaveBeenCalledTimes(1));

        result.current.create.mutate({ name: "DocsFetcher" });

        await waitFor(() => expect(result.current.create.isError).toBe(true));
        expect(getBuiltinToolsets).toHaveBeenCalledTimes(1);
        expect(getCustomToolsets).toHaveBeenCalledTimes(1);
    });
});
