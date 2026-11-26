// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { UserContextValue } from "@/providers/contexts/UserContext";

const h = vi.hoisted(() => ({
    api: { getAll: vi.fn(async () => [{ id: "listed" }]), get: vi.fn(async (id: string) => ({ id })), create: vi.fn(async () => ({ id: "created" })) },
}));

describe("connector hooks", () => {
    let client: QueryClient;
    let hooks: typeof import("./hooks");
    let connectorKeys: typeof import("./keys").connectorKeys;
    let UserContext: typeof import("@/providers/contexts/UserContext").UserContext;
    let userCtx: UserContextValue;
    const wrapper = ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={client}>
            <UserContext.Provider value={userCtx}>{children}</UserContext.Provider>
        </QueryClientProvider>
    );

    beforeEach(async () => {
        vi.resetModules();
        vi.doMock("./versioned", () => ({ connectorsApi: h.api }));
        const context = await import("@/providers/contexts/UserContext");
        ({ UserContext } = context);
        userCtx = { userHasCapability: cap => cap === context.CAPABILITIES.readConnectors, CAPABILITIES: context.CAPABILITIES, capabilitiesLoaded: true, scopes: [], hasScope: () => false, isSuperAdmin: false };
        hooks = await import("./hooks");
        ({ connectorKeys } = await import("./keys"));
        client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        Object.values(h.api).forEach(fn => fn.mockClear());
    });

    afterEach(() => {
        vi.doUnmock("./versioned");
    });

    it("lists through the connectors API under the list key", async () => {
        const { result } = renderHook(() => hooks.useConnectors(), { wrapper });
        await waitFor(() => expect(result.current.data).toEqual([{ id: "listed" }]));
        expect(client.getQueryCache().find({ queryKey: connectorKeys.list() })).toBeDefined();
    });

    it("reads a detail through the connectors API", async () => {
        const { result } = renderHook(() => hooks.useConnectorDetail("c-1"), { wrapper });
        await waitFor(() => expect(result.current.data).toEqual({ id: "c-1" }));
        expect(h.api.get).toHaveBeenCalledWith("c-1");
    });

    it("creates through the connectors API and invalidates the list", async () => {
        client.setQueryData(connectorKeys.list(), []);
        const { result } = renderHook(() => hooks.useCreateConnectorMutation(), { wrapper });

        await act(async () => {
            await result.current.mutateAsync({ name: "n", description: "d", type: "api" });
        });

        expect(h.api.create).toHaveBeenCalledOnce();
        expect(client.getQueryState(connectorKeys.list())?.isInvalidated).toBe(true);
    });
});
