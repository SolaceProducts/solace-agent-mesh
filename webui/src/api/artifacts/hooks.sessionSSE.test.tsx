// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { ArtifactInfo } from "@/types";

const SESSION_ID = "session-sse";

const makeArtifact = (overrides: Partial<ArtifactInfo> = {}): ArtifactInfo => ({
    filename: "a.txt",
    mimeType: "text/plain",
    size: 10,
    lastModified: "2026-05-29T00:00:00Z",
    ...overrides,
});

describe("session artifact cache — SSE write pattern", () => {
    let useSessionArtifacts: typeof import("@/api/artifacts").useSessionArtifacts;
    let artifactKeys: typeof import("@/api/artifacts").artifactKeys;

    beforeEach(async () => {
        vi.resetModules();

        vi.doMock("@/api/client", () => ({
            api: {
                webui: { get: vi.fn(() => new Promise(() => {})) },
            },
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

    test("setQueryData full-replace lands in useSessionArtifacts.data", async () => {
        const { queryClient, wrapper } = setup();

        queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(SESSION_ID), []);

        const { result } = renderHook(() => useSessionArtifacts(SESSION_ID), { wrapper });
        await waitFor(() => expect(result.current.data).toEqual([]));

        const incoming = [makeArtifact({ filename: "report.md" }), makeArtifact({ filename: "summary.md" })];
        act(() => {
            queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(SESSION_ID), incoming);
        });

        await waitFor(() => expect(result.current.data).toEqual(incoming));
    });

    test("functional setQueryData (markArtifactAsDisplayed) updates only the matching filename", async () => {
        const { queryClient, wrapper } = setup();

        const seed = [makeArtifact({ filename: "a.txt", isDisplayed: false }), makeArtifact({ filename: "b.txt", isDisplayed: false }), makeArtifact({ filename: "c.txt", isDisplayed: false })];
        queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(SESSION_ID), seed);

        const { result } = renderHook(() => useSessionArtifacts(SESSION_ID), { wrapper });
        await waitFor(() => expect(result.current.data?.every(a => a.isDisplayed === false)).toBe(true));

        act(() => {
            queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(SESSION_ID), prev => (prev ?? []).map(a => (a.filename === "b.txt" ? { ...a, isDisplayed: true } : a)));
        });

        await waitFor(() => {
            const updated = result.current.data!;
            expect(updated.find(a => a.filename === "a.txt")!.isDisplayed).toBe(false);
            expect(updated.find(a => a.filename === "b.txt")!.isDisplayed).toBe(true);
            expect(updated.find(a => a.filename === "c.txt")!.isDisplayed).toBe(false);
        });
    });

    test("writes to one session do not bleed into another session's cache", async () => {
        const { queryClient, wrapper } = setup();

        const otherSession = "session-other";
        const aArtifacts = [makeArtifact({ filename: "a.txt" })];
        const otherArtifacts = [makeArtifact({ filename: "other.txt" })];

        queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(SESSION_ID), aArtifacts);
        queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(otherSession), otherArtifacts);

        const { result: aResult } = renderHook(() => useSessionArtifacts(SESSION_ID), { wrapper });
        const { result: otherResult } = renderHook(() => useSessionArtifacts(otherSession), { wrapper });

        await waitFor(() => {
            expect(aResult.current.data?.map(a => a.filename)).toEqual(["a.txt"]);
            expect(otherResult.current.data?.map(a => a.filename)).toEqual(["other.txt"]);
        });
    });
});
