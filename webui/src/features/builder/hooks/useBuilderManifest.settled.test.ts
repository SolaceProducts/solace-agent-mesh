// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { SAM_BUILD_MANIFEST_MIME_TYPE } from "./useBuilderManifest";

function wrapper() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

const VALID = `name: build
components:
  - filename: a.yaml
    type: agent
    name: A
    status: draft
`;

describe("useBuilderManifest — settledness and readability", () => {
    let useBuilderManifest: typeof import("./useBuilderManifest").useBuilderManifest;
    let persistedContent: string | null;
    let queryState: { data?: string; isFetched: boolean; isFetching: boolean };
    let currentSessionId: string;
    let artifacts: Array<Record<string, unknown>>;
    let hasEverFetched: boolean;

    beforeEach(async () => {
        vi.resetModules();
        persistedContent = null;
        queryState = { data: undefined, isFetched: true, isFetching: false };
        currentSessionId = "session-1";
        artifacts = [];
        hasEverFetched = false;

        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({ data: artifacts, isLoading: false, refetch: () => Promise.resolve() }),
        }));

        vi.doMock("@/api/builder", () => ({
            builderKeys: { manifestContent: (...a: unknown[]) => ["mc", ...a] },
            getManifestContent: () => Promise.resolve(persistedContent),
            useManifestContentQuery: (_s: unknown, filename: unknown, _v: unknown, enabled: boolean) => {
                if (enabled && queryState.isFetched) hasEverFetched = true;
                return {
                    data: filename ? queryState.data : undefined,
                    isFetched: hasEverFetched,
                    isFetching: enabled && queryState.isFetching,
                };
            },
            usePatchSessionNameMutation: () => ({ mutateAsync: vi.fn().mockResolvedValue(undefined) }),
            useUploadManifestMutation: () => ({ mutateAsync: vi.fn().mockResolvedValue({ ok: true }) }),
        }));

        const downloadAndResolveArtifact = vi.fn().mockResolvedValue(null);
        vi.doMock("@/providers/contexts", () => ({
            useChatContext: () => ({ downloadAndResolveArtifact, sessionId: currentSessionId }),
        }));

        useBuilderManifest = (await import("./useBuilderManifest")).useBuilderManifest;
    });

    test("is not settled while the content query has never resolved", () => {
        queryState = { data: undefined, isFetched: false, isFetching: true };

        const { result } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });

        expect(result.current.isSettled).toBe(false);
    });

    test("is not settled while a resolved content query is refetching", async () => {
        queryState = { data: undefined, isFetched: true, isFetching: true };

        const { result } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });

        await waitFor(() => expect(result.current.isLoading).toBe(false));
        expect(result.current.isSettled).toBe(false);
    });

    test("is settled, and readable, for a session that genuinely has no manifest", async () => {
        const { result } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });

        await waitFor(() => expect(result.current.isSettled).toBe(true));
        expect(result.current.manifest).toBeNull();
        expect(result.current.isUnreadable).toBe(false);
    });

    test("reports YAML that does not parse as unreadable, not as absent", async () => {
        queryState = { data: "just a bare string", isFetched: true, isFetching: false };

        const { result } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });

        await waitFor(() => expect(result.current.isUnreadable).toBe(true));
        expect(result.current.manifest).toBeNull();
    });

    test("a manifest that parses is neither unreadable nor blocking", async () => {
        queryState = { data: VALID, isFetched: true, isFetching: false };

        const { result } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });

        await waitFor(() => expect(result.current.manifest?.name).toBe("build"));
        expect(result.current.isUnreadable).toBe(false);
        expect(result.current.isSettled).toBe(true);
    });

    test("reports a half-streamed manifest as unreadable, which is what guards it", async () => {
        queryState = { data: undefined, isFetched: true, isFetching: false };
        artifacts = [
            {
                filename: "build_manifest.yaml",
                mimeType: SAM_BUILD_MANIFEST_MIME_TYPE,
                version: 1,
                creationStatus: "in-progress",
                accumulatedContent: "name: build\ncomponents:\n  - filename: [unclosed",
            },
        ];

        const { result } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });

        await waitFor(() => expect(result.current.isUnreadable).toBe(true));
        expect(result.current.manifest).toBeNull();
    });

    test("settles for a stream that is pinned and will never deliver", async () => {
        artifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 1, creationStatus: "in-progress" }];

        const { result } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });

        await waitFor(() => expect(result.current.isSettled).toBe(true), { timeout: 5000 });
        expect(result.current.manifest).toBeNull();
    });

    test("reads a completed stream straight from its accumulated content", async () => {
        artifacts = [
            {
                filename: "build_manifest.yaml",
                mimeType: SAM_BUILD_MANIFEST_MIME_TYPE,
                version: 1,
                creationStatus: "completed",
                accumulatedContent: VALID,
            },
        ];

        const { result } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });

        await waitFor(() => expect(result.current.manifest?.name).toBe("build"), { timeout: 5000 });
        expect(result.current.isUnreadable).toBe(false);
    });

    test("settles for a stream that failed outright", async () => {
        artifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 1, creationStatus: "failed" }];

        const { result } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });

        await waitFor(() => expect(result.current.isSettled).toBe(true), { timeout: 5000 });
    });

    test("is not settled on the render that observes a session change", async () => {
        const seen: boolean[] = [];
        const { result, rerender } = renderHook(
            () => {
                const state = useBuilderManifest();
                seen.push(state.isSettled);
                return state;
            },
            { wrapper: wrapper() }
        );
        await waitFor(() => expect(result.current.isSettled).toBe(true));

        seen.length = 0;
        currentSessionId = "session-2";
        rerender();

        expect(seen[0]).toBe(false);
    });
});
