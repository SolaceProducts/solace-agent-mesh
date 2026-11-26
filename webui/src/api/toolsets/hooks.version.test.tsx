// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";

import { CAPABILITIES, UserContext, type UserContextValue } from "@/providers/contexts/UserContext";

import { useBuiltinToolsets, useCustomToolsets, useToolsetBuildTarget } from "./hooks";
import { toolsetKeys } from "./keys";
import { toolsetsApi } from "./versioned";

const canReadToolsets: UserContextValue = {
    userHasCapability: cap => cap === CAPABILITIES.readToolsets,
    CAPABILITIES,
    capabilitiesLoaded: true,
    scopes: [],
    hasScope: () => false,
    isSuperAdmin: false,
};

function wrapper(client: QueryClient) {
    return ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={client}>
            <UserContext.Provider value={canReadToolsets}>{children}</UserContext.Provider>
        </QueryClientProvider>
    );
}

const buildTarget = { os: "linux", arch: "amd64", source: "str-init" as const };

const cases = [
    { hook: "useCustomToolsets", use: useCustomToolsets, method: "getCustom", key: toolsetKeys.custom, result: [] },
    { hook: "useBuiltinToolsets", use: useBuiltinToolsets, method: "getBuiltin", key: toolsetKeys.builtin, result: [] },
    { hook: "useToolsetBuildTarget", use: useToolsetBuildTarget, method: "getBuildTarget", key: toolsetKeys.buildTarget, result: buildTarget },
] as const;

describe("toolset hooks", () => {
    afterEach(() => vi.restoreAllMocks());

    test.each(cases)("$hook calls the toolsets API and caches under its key", async ({ use, method, key, result }) => {
        const called = vi.spyOn(toolsetsApi, method).mockResolvedValue(result as never);
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

        const { result: hook } = renderHook(() => use(), { wrapper: wrapper(client) });

        await waitFor(() => expect(hook.current.isSuccess).toBe(true));
        expect(called).toHaveBeenCalledTimes(1);
        expect(client.getQueryData(key())).toEqual(result);
    });
});
