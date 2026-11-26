// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/// <reference types="@testing-library/jest-dom" />
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";

import { useDeleteGatewayMutation } from "./hooks";
import { gatewayKeys } from "./keys";

let deleteSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    deleteSpy = vi.spyOn(api.platform, "delete");
});

afterEach(() => {
    deleteSpy.mockRestore();
});

function buildClient(): QueryClient {
    return new QueryClient({
        defaultOptions: {
            queries: { retry: false },
            mutations: { retry: false },
        },
    });
}

function wrapper(client: QueryClient) {
    return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useDeleteGatewayMutation", () => {
    test("DELETEs by id and invalidates the gateway lists on success", async () => {
        deleteSpy.mockResolvedValue(new Response(null, { status: 204 }));
        const client = buildClient();
        const invalidateSpy = vi.spyOn(client, "invalidateQueries");

        const { result } = renderHook(() => useDeleteGatewayMutation(), { wrapper: wrapper(client) });

        await act(async () => {
            await result.current.mutateAsync("gw-42");
        });

        await waitFor(() => expect(result.current.isSuccess).toBe(true));
        expect(deleteSpy).toHaveBeenCalledWith("/api/v2/platform/entrypoints/gw-42", expect.anything());
        expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: gatewayKeys.lists() });
    });

    test("does not invalidate caches when the delete fails", async () => {
        deleteSpy.mockRejectedValue(new Error("boom"));
        const client = buildClient();
        const invalidateSpy = vi.spyOn(client, "invalidateQueries");

        const { result } = renderHook(() => useDeleteGatewayMutation(), { wrapper: wrapper(client) });

        await act(async () => {
            await expect(result.current.mutateAsync("gw-99")).rejects.toThrow("boom");
        });
        expect(invalidateSpy).not.toHaveBeenCalled();
    });
});
