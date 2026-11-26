// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { ArtifactInfo } from "@/types";

const SESSION_ID = "session_deadbeefdeadbeef";

const makeArtifact = (overrides: Partial<ArtifactInfo> = {}): ArtifactInfo => ({
    filename: "report.md",
    mimeType: "text/markdown",
    size: 42,
    lastModified: "2026-06-08T00:00:00Z",
    ...overrides,
});

describe("session artifact cache — shared (read-only) session", () => {
    let useSessionArtifacts: typeof import("@/api/artifacts").useSessionArtifacts;
    let artifactKeys: typeof import("@/api/artifacts").artifactKeys;
    const get = vi.fn(() => Promise.resolve({ data: [] as ArtifactInfo[] }));

    beforeEach(async () => {
        vi.resetModules();
        get.mockClear();
        vi.doMock("@/api/client", () => ({
            api: { webui: { get } },
            getErrorFromResponse: vi.fn(),
        }));

        const mod = await import("@/api/artifacts");
        useSessionArtifacts = mod.useSessionArtifacts;
        artifactKeys = mod.artifactKeys;
    });

    function setup() {
        const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        const wrapper = ({ children }: { children: React.ReactNode }) => React.createElement(QueryClientProvider, { client: queryClient }, children);
        return { queryClient, wrapper };
    }

    test("networkFetch:false preserves the seeded artifacts (no clobbering fetch)", async () => {
        const { queryClient, wrapper } = setup();
        const seeded = [makeArtifact({ filename: "report.md" }), makeArtifact({ filename: "summary.md" })];
        queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(SESSION_ID), seeded);

        const { result } = renderHook(() => useSessionArtifacts(SESSION_ID, { networkFetch: false }), { wrapper });

        await waitFor(() => expect(result.current.data?.map(a => a.filename)).toEqual(["report.md", "summary.md"]));
        await new Promise(r => setTimeout(r, 20));
        expect(result.current.data?.map(a => a.filename)).toEqual(["report.md", "summary.md"]);
        expect(get).not.toHaveBeenCalled();
    });

    test("default (network enabled) clobbers the seed with the empty response — the bug being guarded against", async () => {
        const { queryClient, wrapper } = setup();
        queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(SESSION_ID), [makeArtifact()]);

        const { result } = renderHook(() => useSessionArtifacts(SESSION_ID), { wrapper });

        await waitFor(() => expect(get).toHaveBeenCalledTimes(1));
        await waitFor(() => expect(result.current.data).toEqual([]));
    });
});

describe("useChatArtifacts — isSharedSession wiring", () => {
    let useChatArtifacts: typeof import("@/api/artifacts").useChatArtifacts;
    let artifactKeys: typeof import("@/api/artifacts").artifactKeys;
    const get = vi.fn(() => Promise.resolve({ data: [] as ArtifactInfo[] }));
    const chatCtx = { sessionId: SESSION_ID, isSharedSession: true };

    beforeEach(async () => {
        vi.resetModules();
        get.mockClear();
        chatCtx.isSharedSession = true;
        vi.doMock("@/api/client", () => ({ api: { webui: { get } }, getErrorFromResponse: vi.fn() }));
        vi.doMock("@/providers/contexts", () => ({ useChatContext: () => chatCtx }));
        vi.doMock("@/providers/contexts/ProjectContext", () => ({ useProjectContext: () => ({ activeProject: undefined }) }));

        const mod = await import("@/api/artifacts");
        useChatArtifacts = mod.useChatArtifacts;
        artifactKeys = mod.artifactKeys;
    });

    function setup() {
        const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        const wrapper = ({ children }: { children: React.ReactNode }) => React.createElement(QueryClientProvider, { client: queryClient }, children);
        return { queryClient, wrapper };
    }

    test("shared session (isSharedSession:true) returns the seeded artifacts and fires no fetch", async () => {
        const { queryClient, wrapper } = setup();
        const seeded = [makeArtifact({ filename: "report.md" })];
        queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(SESSION_ID), seeded);

        const { result } = renderHook(() => useChatArtifacts(), { wrapper });

        await waitFor(() => expect(result.current.data.map(a => a.filename)).toEqual(["report.md"]));
        await new Promise(r => setTimeout(r, 20));
        expect(result.current.data.map(a => a.filename)).toEqual(["report.md"]);
        expect(get).not.toHaveBeenCalled();
    });

    test("non-shared session (isSharedSession:false) still fetches from the network", async () => {
        chatCtx.isSharedSession = false;
        const { queryClient, wrapper } = setup();
        queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(SESSION_ID), [makeArtifact()]);

        const { result } = renderHook(() => useChatArtifacts(), { wrapper });

        await waitFor(() => expect(get).toHaveBeenCalledTimes(1));
        await waitFor(() => expect(result.current.data).toEqual([]));
    });
});
