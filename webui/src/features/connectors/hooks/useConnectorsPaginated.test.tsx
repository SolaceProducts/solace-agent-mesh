// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import type { ReactNode } from "react";
import { afterAll, afterEach, beforeAll, describe, expect, test } from "vitest";

import { getPaginatedMeta } from "@/testing/helpers";

import { useConnectorsPaginated } from "./useConnectorsPaginated";

const requestedUrls: string[] = [];

const server = setupServer(
    http.get("*/api/v2/platform/connectors", ({ request }) => {
        requestedUrls.push(request.url);
        return HttpResponse.json({ data: [], meta: getPaginatedMeta(0) });
    })
);

beforeAll(() => server.listen());
afterEach(() => {
    server.resetHandlers();
    requestedUrls.length = 0;
});
afterAll(() => server.close());

function createWrapper() {
    const client = new QueryClient();
    return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

const nameParam = (url: string) => new URL(url).searchParams.get("name");
const pageParam = (url: string) => new URL(url).searchParams.get("pageNumber");

describe("useConnectorsPaginated", () => {
    test("debounces the search term into a trimmed name query param", async () => {
        const { result } = renderHook(() => useConnectorsPaginated({ enabled: true, pageSize: 20 }), { wrapper: createWrapper() });

        await waitFor(() => expect(requestedUrls.length).toBeGreaterThan(0));
        expect(requestedUrls.every(url => nameParam(url) === null)).toBe(true);

        act(() => result.current.setSearchTerm("  payments  "));

        await waitFor(() => expect(requestedUrls.some(url => nameParam(url) === "payments")).toBe(true));
    });

    test("steps back to the last page when a delete leaves the current page past the end", async () => {
        server.use(
            http.get("*/api/v2/platform/connectors", ({ request }) => {
                if (Number(pageParam(request.url)) >= 2) {
                    return HttpResponse.json({ data: [], meta: getPaginatedMeta(10, 10) });
                }
                return HttpResponse.json({ data: [{ id: "c1", name: "A", createdTime: null, updatedTime: null }], meta: getPaginatedMeta(11, 10) });
            })
        );

        const { result } = renderHook(() => useConnectorsPaginated({ enabled: true, pageSize: 10 }), { wrapper: createWrapper() });

        await waitFor(() => expect(result.current.totalPages).toBe(2));

        act(() => result.current.setPageNumber(2));

        await waitFor(() => expect(result.current.pageNumber).toBe(1));
        expect(result.current.connectors).toHaveLength(1);
        expect(result.current.error).toBeNull();
    });

    test("surfaces an error without moving the page", async () => {
        server.use(
            http.get("*/api/v2/platform/connectors", ({ request }) => {
                if (Number(pageParam(request.url)) >= 2) {
                    return new HttpResponse(null, { status: 500 });
                }
                return HttpResponse.json({ data: [{ id: "c1", name: "A", createdTime: null, updatedTime: null }], meta: getPaginatedMeta(11, 10) });
            })
        );

        const { result } = renderHook(() => useConnectorsPaginated({ enabled: true, pageSize: 10 }), { wrapper: createWrapper() });

        await waitFor(() => expect(result.current.totalPages).toBe(2));

        act(() => result.current.setPageNumber(2));

        await waitFor(() => expect(result.current.error).not.toBeNull());
        expect(result.current.pageNumber).toBe(2);
    });
});
