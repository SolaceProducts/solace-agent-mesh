// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { ArtifactInfo } from "@/types";

const SESSION_ID = "session-refetch";

const makeArtifact = (overrides: Partial<ArtifactInfo> = {}): ArtifactInfo => ({
    filename: "a.txt",
    mimeType: "text/plain",
    size: 10,
    lastModified: "2026-05-29T00:00:00Z",
    uri: `artifact://${SESSION_ID}/a.txt`,
    ...overrides,
});

describe("session artifact refetch — streaming merge", () => {
    let useSessionArtifacts: typeof import("@/api/artifacts").useSessionArtifacts;
    let artifactKeys: typeof import("@/api/artifacts").artifactKeys;
    let getMock: ReturnType<typeof vi.fn>;

    beforeEach(async () => {
        vi.resetModules();
        getMock = vi.fn();
        vi.doMock("@/api/client", () => ({
            api: {
                webui: { get: getMock },
            },
        }));

        const mod = await import("@/api/artifacts");
        useSessionArtifacts = mod.useSessionArtifacts;
        artifactKeys = mod.artifactKeys;
    });

    function setup(cached: ArtifactInfo[], serverResponse: ArtifactInfo[]) {
        const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(SESSION_ID), cached);
        getMock.mockResolvedValue({ data: serverResponse });
        const wrapper = ({ children }: { children: React.ReactNode }) => React.createElement(QueryClientProvider, { client: queryClient }, children);
        return renderHook(() => useSessionArtifacts(SESSION_ID), { wrapper });
    }

    test("in-progress artifacts absent from the server response survive the refetch", async () => {
        const persisted = makeArtifact({ filename: "done.md" });
        const streaming = makeArtifact({ filename: "streaming.md", creationStatus: "in-progress" });
        const serverPersisted = makeArtifact({ filename: "done.md", size: 99 });

        const { result } = setup([persisted, streaming], [serverPersisted]);

        await waitFor(() => expect(getMock).toHaveBeenCalled());
        await waitFor(() => {
            const filenames = result.current.data?.map(a => a.filename);
            expect(filenames).toEqual(["done.md", "streaming.md"]);
        });
        expect(result.current.data?.find(a => a.filename === "done.md")?.size).toBe(99);
        expect(result.current.data?.find(a => a.filename === "streaming.md")?.creationStatus).toBe("in-progress");
    });

    test("a cached in-progress entry wins over the server row with the same filename", async () => {
        const streamingResave = makeArtifact({ filename: "report.md", creationStatus: "in-progress", accumulatedContent: "partial chunk" });
        const serverPreviousVersion = makeArtifact({ filename: "report.md", size: 500 });

        const { result } = setup([streamingResave], [serverPreviousVersion]);

        await waitFor(() => expect(getMock).toHaveBeenCalled());
        await waitFor(() => {
            const report = result.current.data?.find(a => a.filename === "report.md");
            expect(report?.creationStatus).toBe("in-progress");
            expect(report?.accumulatedContent).toBe("partial chunk");
        });
        expect(result.current.data).toHaveLength(1);
    });

    test("artifacts the server no longer returns are dropped unless they are streaming", async () => {
        const deleted = makeArtifact({ filename: "deleted.md" });
        const streaming = makeArtifact({ filename: "streaming.md", creationStatus: "in-progress" });

        const { result } = setup([deleted, streaming], []);

        await waitFor(() => expect(getMock).toHaveBeenCalled());
        await waitFor(() => {
            expect(result.current.data?.map(a => a.filename)).toEqual(["streaming.md"]);
        });
    });

    test("with nothing streaming the server response replaces the cache wholesale", async () => {
        const stale = makeArtifact({ filename: "stale.md" });
        const fresh = makeArtifact({ filename: "fresh.md" });

        const { result } = setup([stale], [fresh]);

        await waitFor(() => expect(getMock).toHaveBeenCalled());
        await waitFor(() => {
            expect(result.current.data?.map(a => a.filename)).toEqual(["fresh.md"]);
        });
    });
});
