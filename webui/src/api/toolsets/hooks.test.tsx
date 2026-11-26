// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { UserContextValue } from "@/providers/contexts/UserContext";

import type { Toolset } from "@/types";

import { toolsetKeys } from "./keys";

function makeToolset(status: Toolset["discoveryStatus"]): Toolset {
    return {
        id: "toolset-1",
        name: "staging-ops",
        description: "test ops toolset",
        type: "custom",
        discoveryStatus: status,
        discoveryErrors: null,
        zipFilename: "staging-ops.zip",
        zipFileSize: 1024,
        files: [],
        createdBy: "test-user@example.com",
        updatedBy: "test-user@example.com",
        createdAt: "2026-07-06T20:19:29.000Z",
        updatedAt: "2026-07-06T20:19:29.000Z",
    };
}

describe("isToolsetProcessing", () => {
    let isToolsetProcessing: typeof import("./hooks").isToolsetProcessing;

    beforeEach(async () => {
        vi.resetModules();
        ({ isToolsetProcessing } = await import("./hooks"));
    });

    test.each([
        ["created", true],
        ["pending", true],
        ["ready", false],
        ["failed", false],
        ["removed", false],
    ] as const)("returns %s -> %s", (status, expected) => {
        expect(isToolsetProcessing(status)).toBe(expected);
    });

    test("returns false for undefined", () => {
        expect(isToolsetProcessing(undefined)).toBe(false);
    });
});

describe("useCustomToolsets polling", () => {
    type AsyncFn = ((...args: unknown[]) => Promise<unknown>) & {
        mockResolvedValue: (v: unknown) => AsyncFn;
        mockResolvedValueOnce: (v: unknown) => AsyncFn;
    };
    let getCustomToolsetsMock: AsyncFn;
    let useCustomToolsets: typeof import("./hooks").useCustomToolsets;
    let CAPABILITIES: typeof import("@/providers/contexts/UserContext").CAPABILITIES;
    let UserContext: typeof import("@/providers/contexts/UserContext").UserContext;

    beforeEach(async () => {
        vi.resetModules();
        vi.useFakeTimers();
        getCustomToolsetsMock = vi.fn() as unknown as AsyncFn;
        const toolsets = { getCustom: (...args: unknown[]) => getCustomToolsetsMock(...args), getBuiltin: vi.fn(), getBuildTarget: vi.fn() };
        vi.doMock("./versioned", () => ({ toolsetsApi: toolsets }));
        ({ CAPABILITIES, UserContext } = await import("@/providers/contexts/UserContext"));
        ({ useCustomToolsets } = await import("./hooks"));
    });

    afterEach(() => {
        vi.useRealTimers();
        vi.doUnmock("./versioned");
    });

    function grantedCtx(): UserContextValue {
        return {
            userHasCapability: cap => cap === CAPABILITIES.readToolsets,
            CAPABILITIES,
            capabilitiesLoaded: true,
            scopes: [],
            hasScope: () => false,
            isSuperAdmin: false,
        };
    }

    function buildQueryWrapper() {
        const client = new QueryClient({
            defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
        });
        return ({ children }: { children: ReactNode }) => (
            <QueryClientProvider client={client}>
                <UserContext.Provider value={grantedCtx()}>{children}</UserContext.Provider>
            </QueryClientProvider>
        );
    }

    test("keeps polling while any toolset in the list is processing and stops once all settle", async () => {
        const pendingList = [makeToolset("ready"), { ...makeToolset("pending"), id: "toolset-2" }];
        const settledList = [makeToolset("ready"), { ...makeToolset("ready"), id: "toolset-2" }];
        getCustomToolsetsMock.mockResolvedValueOnce(pendingList).mockResolvedValueOnce(pendingList).mockResolvedValue(settledList);

        const { result } = renderHook(() => useCustomToolsets(), { wrapper: buildQueryWrapper() });

        await vi.waitFor(() => expect(result.current.data).toHaveLength(2));
        expect(getCustomToolsetsMock).toHaveBeenCalledTimes(1);

        await vi.advanceTimersByTimeAsync(5_000);
        expect(getCustomToolsetsMock).toHaveBeenCalledTimes(2);

        await vi.advanceTimersByTimeAsync(5_000);
        await vi.waitFor(() => expect(result.current.data?.every(t => t.discoveryStatus === "ready")).toBe(true));
        expect(getCustomToolsetsMock).toHaveBeenCalledTimes(3);

        await vi.advanceTimersByTimeAsync(5_000);
        expect(getCustomToolsetsMock).toHaveBeenCalledTimes(3);
    });

    test("does not poll when every toolset is already terminal", async () => {
        getCustomToolsetsMock.mockResolvedValue([makeToolset("ready"), { ...makeToolset("failed"), id: "toolset-2" }]);

        const { result } = renderHook(() => useCustomToolsets(), { wrapper: buildQueryWrapper() });

        await vi.waitFor(() => expect(result.current.data).toHaveLength(2));
        expect(getCustomToolsetsMock).toHaveBeenCalledTimes(1);

        await vi.advanceTimersByTimeAsync(5_000);
        expect(getCustomToolsetsMock).toHaveBeenCalledTimes(1);
    });

    test("refetches on mount despite a fresh production-staleTime cache, so an out-of-band pending status is still caught", async () => {
        const client = new QueryClient({
            defaultOptions: { queries: { retry: false, staleTime: 5 * 60 * 1000 }, mutations: { retry: false } },
        });
        client.setQueryData(toolsetKeys.custom(), [makeToolset("ready")]);
        getCustomToolsetsMock.mockResolvedValue([makeToolset("ready"), { ...makeToolset("pending"), id: "toolset-2" }]);

        const wrapper = ({ children }: { children: ReactNode }) => (
            <QueryClientProvider client={client}>
                <UserContext.Provider value={grantedCtx()}>{children}</UserContext.Provider>
            </QueryClientProvider>
        );

        const { result } = renderHook(() => useCustomToolsets(), { wrapper });

        await vi.waitFor(() => expect(getCustomToolsetsMock).toHaveBeenCalledTimes(1));
        await vi.waitFor(() => expect(result.current.data).toHaveLength(2));

        await vi.advanceTimersByTimeAsync(5_000);
        expect(getCustomToolsetsMock).toHaveBeenCalledTimes(2);
    });
});
