// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import type { ReactNode } from "react";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import type { ShareLink } from "@/types/share";

import { useChatShareLink } from "./useChatShareLink";

const SESSION_ID = "session-1";
const SHARE_ID = "share-1";
const SHARE_URL = `https://example.com/#/share/${SHARE_ID}`;

const linkPayload: ShareLink = {
    id: SHARE_ID,
    sessionId: SESSION_ID,
    title: "Test",
    isPublic: false,
    requireAuthentication: false,
    allowedDomains: [],
    accessType: "authenticated",
    shareUrl: SHARE_URL,
    createdAt: "2024-01-01T00:00:00.000Z",
    snapshotTime: "2024-01-01T00:00:00.000Z",
};

let linkExists = false;
let postShouldFail = false;
let deleteShouldFail = false;
const deleteCalls: string[] = [];
const postBodies: Array<{ sessionId: string; body: unknown }> = [];

const server = setupServer(
    http.get("*/api/v1/share/link/:sessionId", () => {
        return linkExists ? HttpResponse.json(linkPayload) : new HttpResponse(null, { status: 404 });
    }),
    http.post("*/api/v1/share/:sessionId", async ({ request, params }) => {
        if (postShouldFail) return new HttpResponse(JSON.stringify({ message: "boom" }), { status: 500 });
        const body = await request.json();
        postBodies.push({ sessionId: String(params.sessionId), body });
        linkExists = true;
        return HttpResponse.json(linkPayload);
    }),
    http.delete("*/api/v1/share/:shareId", ({ params }) => {
        deleteCalls.push(String(params.shareId));
        if (deleteShouldFail) return new HttpResponse(JSON.stringify({ message: "boom" }), { status: 500 });
        linkExists = false;
        return new HttpResponse(null, { status: 204 });
    })
);

vi.mock("@/utils/clipboard", () => ({
    copyToClipboard: vi.fn(async () => true),
    copyDeferredToClipboard: vi.fn((textPromise: Promise<string>) => {
        textPromise.catch(() => {});
        return Promise.resolve(true);
    }),
}));

function wrapper(client: QueryClient) {
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

function setup(opts?: { onError?: (e: { title: string; message: string }) => void; onSuccess?: (m: string) => void }) {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const onError = opts?.onError ?? vi.fn();
    const onSuccess = opts?.onSuccess ?? vi.fn();
    const view = renderHook(() => useChatShareLink({ sessionId: SESSION_ID, isOpen: true, onError, onSuccess }), { wrapper: wrapper(client) });
    return { ...view, onError, onSuccess };
}

describe("useChatShareLink", () => {
    beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
    afterEach(() => server.resetHandlers());
    afterAll(() => server.close());

    beforeEach(() => {
        linkExists = false;
        postShouldFail = false;
        deleteShouldFail = false;
        deleteCalls.length = 0;
        postBodies.length = 0;
    });

    test("copyOrCreatePublicLink creates a link when none exists and reports success", async () => {
        const { result, onSuccess } = setup();
        await waitFor(() => expect(result.current.isLoading).toBe(false));

        await act(async () => {
            await result.current.copyOrCreatePublicLink();
        });

        expect(postBodies).toHaveLength(1);
        expect(postBodies[0]).toEqual({ sessionId: SESSION_ID, body: { requireAuthentication: false } });
        await waitFor(() => expect(result.current.isNewlyCreatedLink).toBe(true));
        await waitFor(() => expect(onSuccess).toHaveBeenCalledWith(expect.stringMatching(/created/i)));
    });

    test("copyOrCreatePublicLink copies the existing URL without re-creating the link", async () => {
        linkExists = true;
        const { result, onSuccess } = setup();
        await waitFor(() => expect(result.current.shareLink?.id).toBe(SHARE_ID));

        await act(async () => {
            await result.current.copyOrCreatePublicLink();
        });

        expect(postBodies).toHaveLength(0);
        expect(result.current.publicLinkCopied).toBe(true);
        expect(onSuccess).toHaveBeenCalledWith(expect.stringMatching(/copied/i));
    });

    test("copyOrCreatePublicLink fires onError when the create call fails", async () => {
        postShouldFail = true;
        const { result, onError } = setup();
        await waitFor(() => expect(result.current.isLoading).toBe(false));

        await act(async () => {
            await result.current.copyOrCreatePublicLink();
        });

        expect(onError).toHaveBeenCalledWith(expect.objectContaining({ title: "Failed to Create Public Link" }));
        expect(result.current.isNewlyCreatedLink).toBe(false);
    });

    test("deletePublicLink deletes the link via API and reports success", async () => {
        linkExists = true;
        const { result, onSuccess } = setup();
        await waitFor(() => expect(result.current.shareLink?.id).toBe(SHARE_ID));

        await act(async () => {
            await result.current.deletePublicLink();
        });

        expect(deleteCalls.length).toBeGreaterThan(0);
        expect(deleteCalls[0]).toBe(SHARE_ID);
        expect(linkExists).toBe(false);
        await waitFor(() => expect(onSuccess).toHaveBeenCalledWith("Link removed"));
    });

    test("ensureLinkForInvite creates an authenticated link when none exists", async () => {
        const { result } = setup();
        await waitFor(() => expect(result.current.isLoading).toBe(false));

        let returnedId: string | undefined;
        await act(async () => {
            const r = await result.current.ensureLinkForInvite();
            returnedId = r?.id;
        });

        expect(returnedId).toBe(SHARE_ID);
        expect(postBodies).toEqual([{ sessionId: SESSION_ID, body: { requireAuthentication: true } }]);
    });

    test("discardIfNew deletes a freshly-created link", async () => {
        const { result } = setup();
        await waitFor(() => expect(result.current.isLoading).toBe(false));

        await act(async () => {
            await result.current.copyOrCreatePublicLink();
        });
        deleteCalls.length = 0;

        await act(async () => {
            await result.current.discardIfNew();
        });

        expect(deleteCalls.length).toBeGreaterThan(0);
        expect(deleteCalls[0]).toBe(SHARE_ID);
    });

    test("ensureLinkForInvite fires onError when creation fails", async () => {
        postShouldFail = true;
        const { result, onError } = setup();
        await waitFor(() => expect(result.current.isLoading).toBe(false));

        let returnedId: string | undefined;
        await act(async () => {
            const r = await result.current.ensureLinkForInvite();
            returnedId = r?.id;
        });

        expect(returnedId).toBeUndefined();
        expect(onError).toHaveBeenCalledWith(expect.objectContaining({ title: "Failed to Save" }));
    });

    test("discardIfNew fires onError when the delete call fails", async () => {
        const { result, onError } = setup();
        await waitFor(() => expect(result.current.isLoading).toBe(false));

        await act(async () => {
            await result.current.copyOrCreatePublicLink();
        });
        deleteShouldFail = true;

        await act(async () => {
            await result.current.discardIfNew();
        });

        expect(onError).toHaveBeenCalledWith(expect.objectContaining({ title: "Failed to Delete Link" }));
    });

    test("clearNewlyCreated turns the next discardIfNew into a no-op", async () => {
        const { result } = setup();
        await waitFor(() => expect(result.current.isLoading).toBe(false));

        await act(async () => {
            await result.current.copyOrCreatePublicLink();
        });
        deleteCalls.length = 0;

        act(() => result.current.clearNewlyCreated());
        await act(async () => {
            await result.current.discardIfNew();
        });

        expect(deleteCalls).toEqual([]);
        expect(result.current.isNewlyCreatedLink).toBe(false);
    });
});
