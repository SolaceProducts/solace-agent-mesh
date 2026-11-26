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

const GOOD = `name: sys\nfull_validation_passed: true\ncomponents:\n  - filename: A.yaml\n    type: agent\n    name: A\n    status: validated\n`;
const BROKEN = `components:\n  - filename: A.yaml\n    type: agent\n    name: A\n    status: validated\n`;
const UNPARSEABLE = `name: sys\ncomponents: [oops\n  - : :\n`;

describe("useBuilderManifest — a broken manifest after the first good one", () => {
    let useBuilderManifest: typeof import("./useBuilderManifest").useBuilderManifest;
    let content: string;
    let creationStatus: "in-progress" | "completed" | "failed" | undefined;
    let sessionId: string;
    let hasArtifact: boolean;

    beforeEach(async () => {
        vi.resetModules();
        content = GOOD;
        creationStatus = "completed";
        sessionId = "session-1";
        hasArtifact = true;

        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({
                data: hasArtifact ? [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 0, accumulatedContent: content, creationStatus }] : [],
                isLoading: false,
                refetch: () => Promise.resolve(),
            }),
        }));
        vi.doMock("@/api/builder", () => ({
            builderKeys: { manifestContent: (...a: unknown[]) => ["mc", ...a] },
            getManifestContent: () => Promise.resolve(null),
            useManifestContentQuery: () => ({ data: undefined }),
            usePatchSessionNameMutation: () => ({ mutateAsync: vi.fn().mockResolvedValue(undefined) }),
            useUploadManifestMutation: () => ({ mutateAsync: vi.fn().mockResolvedValue(true) }),
        }));
        vi.doMock("@/providers/contexts", () => ({
            useChatContext: () => ({ downloadAndResolveArtifact: vi.fn().mockResolvedValue(null), sessionId }),
        }));

        useBuilderManifest = (await import("./useBuilderManifest")).useBuilderManifest;
    });

    for (const [label, bad] of [
        ["schema-invalid", BROKEN],
        ["unparseable YAML", UNPARSEABLE],
    ] as const) {
        test(`reports ${label} content as unreadable even though a good manifest was seen first`, async () => {
            const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });
            await waitFor(() => expect(result.current.manifest?.name).toBe("sys"));
            expect(result.current.isUnreadable).toBe(false);

            content = bad;
            rerender();
            await waitFor(() => expect(result.current.isUnreadable).toBe(true));

            expect(result.current.manifest?.name).toBe("sys");
        });
    }

    test("reports a mid-stream partial rather than deciding when to say so", async () => {
        const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });
        await waitFor(() => expect(result.current.manifest?.name).toBe("sys"));

        creationStatus = "in-progress";
        content = "name: sys\ncomponents:\n  - filena";
        rerender();
        await waitFor(() => expect(result.current.isUnreadable).toBe(true));

        creationStatus = "completed";
        content = GOOD;
        rerender();
        await waitFor(() => expect(result.current.isUnreadable).toBe(false));
    });

    test("reports an unsettled partial when no manifest has ever parsed", async () => {
        content = "name: sys\ncomponents:\n  - filena";
        creationStatus = "in-progress";
        const { result } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });
        await waitFor(() => expect(result.current.isUnreadable).toBe(true));
        expect(result.current.manifest).toBeNull();
    });

    test("says nothing when there is no manifest content at all", async () => {
        hasArtifact = false;
        const { result } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });
        await new Promise(r => setTimeout(r, 20));
        expect(result.current.isUnreadable).toBe(false);
        expect(result.current.manifest).toBeNull();
    });

    test("does not carry a broken manifest into the session being opened", async () => {
        content = BROKEN;
        const seen: boolean[] = [];
        const { result, rerender } = renderHook(
            () => {
                const state = useBuilderManifest();
                seen.push(state.isUnreadable);
                return state;
            },
            { wrapper: wrapper() }
        );
        await waitFor(() => expect(result.current.isUnreadable).toBe(true));

        hasArtifact = false;
        sessionId = "session-2";
        seen.length = 0;
        rerender();
        await new Promise(r => setTimeout(r, 20));
        expect(seen).not.toContain(true);
    });
});
