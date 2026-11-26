// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "../client";

import { useCreatePromptShares, usePromptShares, useReplacePromptShares } from "./hooks";
import { promptShareKeys } from "./keys";

const fakeEnvelope = {
    data: [
        {
            id: "share-1",
            userEmail: "editor@example.com",
            accessLevel: "resource_editor",
            sharedByEmail: "owner@example.com",
            createdAt: "2026-05-29T08:00:00Z",
            updatedAt: "2026-05-29T08:00:00Z",
        },
    ],
    meta: { pagination: { pageNumber: 1, pageSize: 1, totalPages: 1, count: 1, nextPage: null } },
};

let getSpy: ReturnType<typeof vi.spyOn>;
let postSpy: ReturnType<typeof vi.spyOn>;
let putSpy: ReturnType<typeof vi.spyOn>;

beforeEach(() => {
    getSpy = vi.spyOn(api.webui, "get");
    postSpy = vi.spyOn(api.webui, "post");
    putSpy = vi.spyOn(api.webui, "put");
});

afterEach(() => {
    vi.restoreAllMocks();
});

function buildClient(): QueryClient {
    return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
}

function wrapper(client: QueryClient) {
    return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("usePromptShares", () => {
    test("fetches shares for the given group and returns the envelope", async () => {
        getSpy.mockResolvedValue(fakeEnvelope);

        const { result } = renderHook(() => usePromptShares("group-abc"), { wrapper: wrapper(buildClient()) });

        await waitFor(() => expect(result.current.isSuccess).toBe(true));
        expect(getSpy).toHaveBeenCalledWith("/api/v1/prompts/groups/group-abc/shares");
        expect(result.current.data).toEqual(fakeEnvelope);
    });

    test("skips the fetch when no group id is provided", () => {
        renderHook(() => usePromptShares(""), { wrapper: wrapper(buildClient()) });
        expect(getSpy).not.toHaveBeenCalled();
    });
});

describe("useCreatePromptShares", () => {
    test("calls POST and invalidates the group's shares cache on success", async () => {
        postSpy.mockResolvedValue({ created: fakeEnvelope.data, updated: [], totalProcessed: 1 });
        const client = buildClient();
        const invalidateSpy = vi.spyOn(client, "invalidateQueries");

        const { result } = renderHook(() => useCreatePromptShares(), { wrapper: wrapper(client) });

        await act(async () => {
            await result.current.mutateAsync({
                promptGroupId: "group-abc",
                body: { shares: [{ userEmail: "editor@example.com", accessLevel: "resource_editor" }] },
            });
        });

        expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: promptShareKeys.forGroup("group-abc") });
    });
});

describe("useReplacePromptShares", () => {
    test("calls PUT and invalidates the group's shares cache on success", async () => {
        putSpy.mockResolvedValue(undefined);
        const client = buildClient();
        const invalidateSpy = vi.spyOn(client, "invalidateQueries");

        const { result } = renderHook(() => useReplacePromptShares(), { wrapper: wrapper(client) });

        await act(async () => {
            await result.current.mutateAsync({
                promptGroupId: "group-abc",
                body: { shares: [{ userEmail: "editor@example.com", accessLevel: "resource_viewer" }] },
            });
        });

        expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: promptShareKeys.forGroup("group-abc") });
    });
});
