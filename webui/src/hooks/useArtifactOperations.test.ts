// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { ArtifactInfo } from "@/types";

const SESSION_ID = "session-test";

const makeArtifact = (overrides: Partial<ArtifactInfo> = {}): ArtifactInfo => ({
    filename: "a.txt",
    mimeType: "text/plain",
    size: 10,
    lastModified: "2026-05-29T00:00:00Z",
    ...overrides,
});

describe("useArtifactOperations — cache writes via queryClient", () => {
    let useArtifactOperations: typeof import("@/hooks/useArtifactOperations").useArtifactOperations;
    let artifactKeys: typeof import("@/api/artifacts").artifactKeys;
    let mockWebuiGet: ReturnType<typeof vi.fn>;
    let mockWebuiDelete: ReturnType<typeof vi.fn>;
    let mockWebuiPost: ReturnType<typeof vi.fn>;

    beforeEach(async () => {
        vi.resetModules();
        mockWebuiGet = vi.fn();
        mockWebuiDelete = vi.fn();
        mockWebuiPost = vi.fn();

        vi.doMock("@/api/client", () => ({
            api: {
                webui: {
                    get: mockWebuiGet,
                    delete: mockWebuiDelete,
                    post: mockWebuiPost,
                },
            },
        }));

        vi.doMock("@/utils", () => ({
            blobToBase64: vi.fn(async () => "base64-content"),
            getErrorMessage: (err: unknown, fallback: string) => (err instanceof Error ? err.message : fallback),
        }));

        const hookMod = await import("@/hooks/useArtifactOperations");
        const keysMod = await import("@/api/artifacts");
        useArtifactOperations = hookMod.useArtifactOperations;
        artifactKeys = keysMod.artifactKeys;
    });

    function setup(seedArtifacts: ArtifactInfo[] = []) {
        const queryClient = new QueryClient({
            defaultOptions: { queries: { retry: false } },
        });
        queryClient.setQueryData(artifactKeys.session(SESSION_ID), seedArtifacts);

        const wrapper = ({ children }: { children: React.ReactNode }) => React.createElement(QueryClientProvider, { client: queryClient }, children);

        const addNotification = vi.fn();
        const setError = vi.fn();
        const closePreview = vi.fn();

        const { result } = renderHook(
            () =>
                useArtifactOperations({
                    sessionId: SESSION_ID,
                    addNotification,
                    setError,
                    previewArtifact: null,
                    closePreview,
                }),
            { wrapper }
        );

        return { result, queryClient, addNotification, setError, closePreview };
    }

    describe("downloadAndResolveArtifact — post-download cache mutation", () => {
        test("clears accumulatedContent and needsEmbedResolution for the right filename", async () => {
            const seed: ArtifactInfo[] = [makeArtifact({ filename: "a.txt", accumulatedContent: "partial", needsEmbedResolution: true }), makeArtifact({ filename: "b.txt", accumulatedContent: "other-partial", needsEmbedResolution: true })];
            const { result, queryClient } = setup(seed);

            mockWebuiGet.mockResolvedValueOnce({ data: [1, 2, 3] }).mockResolvedValueOnce({
                ok: true,
                blob: async () => new Blob(["payload"]),
            });

            await act(async () => {
                await result.current.downloadAndResolveArtifact("a.txt");
            });

            const cached = queryClient.getQueryData<ArtifactInfo[]>(artifactKeys.session(SESSION_ID))!;
            const a = cached.find(x => x.filename === "a.txt")!;
            const b = cached.find(x => x.filename === "b.txt")!;

            expect(a.accumulatedContent).toBeUndefined();
            expect(a.needsEmbedResolution).toBe(false);
            expect(b.accumulatedContent).toBe("other-partial");
            expect(b.needsEmbedResolution).toBe(true);
        });

        test("returns null when artifact not in cache and does not mutate", async () => {
            const { result, queryClient } = setup([makeArtifact({ filename: "a.txt" })]);

            let returnValue: unknown;
            await act(async () => {
                returnValue = await result.current.downloadAndResolveArtifact("missing.txt");
            });

            expect(returnValue).toBeNull();
            const cached = queryClient.getQueryData<ArtifactInfo[]>(artifactKeys.session(SESSION_ID))!;
            expect(cached).toHaveLength(1);
            expect(cached[0].filename).toBe("a.txt");
        });
    });

    describe("upload artifact — invalidates session cache", () => {
        test("after successful upload, refetch is triggered for the session cache", async () => {
            const { result, queryClient } = setup([]);

            const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
            mockWebuiPost.mockResolvedValueOnce({
                ok: true,
                status: 200,
                json: async () => ({ uri: "uri://a.txt", sessionId: SESSION_ID }),
            });

            await act(async () => {
                await result.current.uploadArtifactFile(new File(["x"], "a.txt"));
            });

            expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: artifactKeys.session(SESSION_ID) });
        });

        test("a 413 response reports the size-limit error rather than throwing", async () => {
            const { result, setError } = setup([]);
            mockWebuiPost.mockResolvedValueOnce({
                ok: false,
                status: 413,
                json: async () => ({ message: 'File "a.txt" exceeds the maximum allowed size.' }),
            });

            let returnValue: unknown;
            await act(async () => {
                returnValue = await result.current.uploadArtifactFile(new File(["x"], "a.txt"));
            });

            expect(returnValue).toEqual({ error: 'File "a.txt" exceeds the maximum allowed size.' });
            expect(setError).toHaveBeenCalledWith({ title: "File Upload Failed", error: 'File "a.txt" exceeds the maximum allowed size.', errorSource: "api" });
        });

        test("a network exception reports the upload failure and returns it rather than throwing", async () => {
            const { result, setError } = setup([]);
            mockWebuiPost.mockRejectedValueOnce(new Error("network down"));

            let returnValue: unknown;
            await act(async () => {
                returnValue = await result.current.uploadArtifactFile(new File(["x"], "a.txt"));
            });

            expect(returnValue).toEqual({ error: "network down" });
            expect(setError).toHaveBeenCalledWith({ title: "File Upload Failed", error: "network down", errorSource: "api" });
        });
    });

    describe("confirmDelete — single artifact deletion failure", () => {
        test("a failed delete reports the failure instead of throwing", async () => {
            const { result, setError } = setup([makeArtifact({ filename: "a.txt" })]);
            mockWebuiDelete.mockRejectedValueOnce(new Error("permission denied"));

            act(() => {
                result.current.openDeleteModal(makeArtifact({ filename: "a.txt" }));
            });
            await act(async () => {
                await result.current.confirmDelete();
            });

            expect(setError).toHaveBeenCalledWith({ title: "File Deletion Failed", error: "permission denied", errorSource: "api" });
        });
    });

    describe("confirmBatchDeleteArtifacts — partial failure", () => {
        test("reports how many files failed to delete without dropping the successes", async () => {
            const { result, setError, addNotification } = setup([makeArtifact({ filename: "a.txt" }), makeArtifact({ filename: "b.txt" })]);
            mockWebuiDelete.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error("locked"));

            act(() => {
                result.current.setSelectedArtifactFilenames(new Set(["a.txt", "b.txt"]));
            });
            await act(async () => {
                await result.current.confirmBatchDeleteArtifacts();
            });

            expect(addNotification).toHaveBeenCalledWith("1 files(s) deleted.", "success");
            expect(setError).toHaveBeenCalledWith({ title: "File Deletion Failed", error: "1 file(s) failed to delete.", errorSource: "api" });
        });
    });

    describe("downloadAndResolveArtifact — failure path", () => {
        test("a failed version fetch reports the download failure and resolves to null", async () => {
            const { result, setError } = setup([makeArtifact({ filename: "a.txt" })]);
            mockWebuiGet.mockRejectedValueOnce(new Error("versions endpoint down"));

            let returnValue: unknown;
            await act(async () => {
                returnValue = await result.current.downloadAndResolveArtifact("a.txt");
            });

            expect(returnValue).toBeNull();
            expect(setError).toHaveBeenCalledWith({ title: "File Download Failed", error: "versions endpoint down", errorSource: "api" });
        });
    });
});
