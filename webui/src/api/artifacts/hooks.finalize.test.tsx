// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { ArtifactInfo } from "@/types";

const SESSION_ID = "session-finalize";

const makeArtifact = (overrides: Partial<ArtifactInfo> = {}): ArtifactInfo => ({
    filename: "a.txt",
    mimeType: "text/plain",
    size: 10,
    lastModified: "2026-05-29T00:00:00Z",
    uri: `artifact://${SESSION_ID}/a.txt`,
    ...overrides,
});

describe("finalizeInProgressArtifacts", () => {
    let finalizeInProgressArtifacts: typeof import("@/api/artifacts").finalizeInProgressArtifacts;

    beforeEach(async () => {
        vi.resetModules();
        vi.doMock("@/api/client", () => ({ api: { webui: { get: vi.fn() } } }));
        finalizeInProgressArtifacts = (await import("@/api/artifacts")).finalizeInProgressArtifacts;
    });

    test("flips in-progress entries to completed and leaves the rest untouched", () => {
        const input = [makeArtifact({ filename: "done.md", creationStatus: "completed" }), makeArtifact({ filename: "stuck.md", creationStatus: "in-progress" }), makeArtifact({ filename: "failed.md", creationStatus: "failed" })];

        const result = finalizeInProgressArtifacts(input)!;

        expect(result.find(a => a.filename === "stuck.md")?.creationStatus).toBe("completed");
        expect(result.find(a => a.filename === "done.md")?.creationStatus).toBe("completed");
        expect(result.find(a => a.filename === "failed.md")?.creationStatus).toBe("failed");
    });

    test("returns the same reference when nothing is in-progress (no needless cache write)", () => {
        const input = [makeArtifact({ filename: "done.md", creationStatus: "completed" })];
        expect(finalizeInProgressArtifacts(input)).toBe(input);
    });

    test("passes undefined through so it is usable as a setQueryData updater on an unseeded cache", () => {
        expect(finalizeInProgressArtifacts(undefined)).toBeUndefined();
    });
});

describe("finalize-then-refetch stops the streaming re-pin", () => {
    let useSessionArtifacts: typeof import("@/api/artifacts").useSessionArtifacts;
    let artifactKeys: typeof import("@/api/artifacts").artifactKeys;
    let finalizeSessionArtifacts: typeof import("@/api/artifacts").finalizeSessionArtifacts;
    let getMock: ReturnType<typeof vi.fn>;

    beforeEach(async () => {
        vi.resetModules();
        getMock = vi.fn();
        vi.doMock("@/api/client", () => ({ api: { webui: { get: getMock } } }));
        const mod = await import("@/api/artifacts");
        useSessionArtifacts = mod.useSessionArtifacts;
        artifactKeys = mod.artifactKeys;
        finalizeSessionArtifacts = mod.finalizeSessionArtifacts;
    });

    test("finalizeSessionArtifacts flips the session cache's in-progress entries to completed", () => {
        const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(SESSION_ID), [makeArtifact({ filename: "stuck.md", creationStatus: "in-progress" }), makeArtifact({ filename: "done.md", creationStatus: "completed" })]);

        finalizeSessionArtifacts(queryClient, SESSION_ID);

        const cached = queryClient.getQueryData<ArtifactInfo[]>(artifactKeys.session(SESSION_ID))!;
        expect(cached.find(a => a.filename === "stuck.md")?.creationStatus).toBe("completed");
        expect(cached.find(a => a.filename === "done.md")?.creationStatus).toBe("completed");
    });

    test("without finalize, a stuck in-progress entry is re-pinned over the persisted server copy", async () => {
        const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(SESSION_ID), [makeArtifact({ filename: "report.md", creationStatus: "in-progress" })]);
        getMock.mockResolvedValue({ data: [makeArtifact({ filename: "report.md", size: 500 }), makeArtifact({ filename: "sibling.md", size: 20 })] });

        const wrapper = ({ children }: { children: React.ReactNode }) => React.createElement(QueryClientProvider, { client: queryClient }, children);
        const { result } = renderHook(() => useSessionArtifacts(SESSION_ID), { wrapper });

        await waitFor(() => {
            expect(result.current.data?.map(a => a.filename)).toEqual(["report.md", "sibling.md"]);
        });

        const report = result.current.data?.find(a => a.filename === "report.md");
        expect(report?.creationStatus).toBe("in-progress");
        expect(report?.size).toBe(10);
    });

    test("finalizing on task end before the refetch lets the server row win", async () => {
        const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        queryClient.setQueryData<ArtifactInfo[]>(artifactKeys.session(SESSION_ID), [makeArtifact({ filename: "report.md", creationStatus: "in-progress" })]);
        getMock.mockResolvedValue({ data: [makeArtifact({ filename: "report.md", size: 500 })] });

        finalizeSessionArtifacts(queryClient, SESSION_ID);

        const wrapper = ({ children }: { children: React.ReactNode }) => React.createElement(QueryClientProvider, { client: queryClient }, children);
        const { result } = renderHook(() => useSessionArtifacts(SESSION_ID), { wrapper });

        await waitFor(() => expect(getMock).toHaveBeenCalled());
        await waitFor(() => {
            const report = result.current.data?.find(a => a.filename === "report.md");
            expect(report?.creationStatus).not.toBe("in-progress");
            expect(report?.size).toBe(500);
        });
    });
});
