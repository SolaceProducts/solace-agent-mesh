// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { SAM_BUILD_MANIFEST_MIME_TYPE } from "./useBuilderManifest";

function buildQueryWrapper() {
    const client = new QueryClient({
        defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

describe("useBuilderManifest", () => {
    let useBuilderManifest: typeof import("./useBuilderManifest").useBuilderManifest;

    type Artifact = {
        filename: string;
        mimeType: string;
        version?: number;
        accumulatedContent?: string;
    };
    let mockArtifacts: Artifact[];
    let mockSessionId: string;
    let mockDownload: ReturnType<typeof vi.fn>;
    let apiWebuiPatch: ((...args: unknown[]) => Promise<unknown>) & { mock?: unknown };

    beforeEach(async () => {
        vi.resetModules();

        mockArtifacts = [];
        mockSessionId = "session-1";
        mockDownload = vi.fn().mockResolvedValue(null);
        apiWebuiPatch = vi.fn().mockResolvedValue(undefined) as unknown as typeof apiWebuiPatch;

        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({ data: mockArtifacts, isLoading: false, refetch: () => Promise.resolve() }),
        }));

        vi.doMock("@/api/builder", async importOriginal => ({
            ...(await importOriginal<typeof import("@/api/builder")>()),
            usePatchSessionNameMutation: () => ({ mutateAsync: vi.fn().mockResolvedValue(undefined) }),
        }));

        vi.doMock("@/providers/contexts/ChatContext", () => ({
            useChatContext: () => ({
                downloadAndResolveArtifact: mockDownload,
                sessionId: mockSessionId,
            }),
            api: {
                webui: {
                    get: () => Promise.resolve([]),
                    patch: (...args: unknown[]) => apiWebuiPatch(...args),
                },
            },
        }));

        const mod = await import("./useBuilderManifest");
        useBuilderManifest = mod.useBuilderManifest;
    });

    test("returns null manifest when there are no artifacts", () => {
        mockArtifacts = [];

        const { result } = renderHook(() => useBuilderManifest(), { wrapper: buildQueryWrapper() });

        expect(result.current.manifest).toBeNull();
        expect(result.current.isLoading).toBe(false);
    });

    test("returns null manifest when artifact list has no build manifest MIME type", () => {
        mockArtifacts = [{ filename: "foo.yaml", mimeType: "text/plain", accumulatedContent: "hi" }];

        const { result } = renderHook(() => useBuilderManifest(), { wrapper: buildQueryWrapper() });

        expect(result.current.manifest).toBeNull();
    });

    test("parses manifest from accumulatedContent without triggering a download", () => {
        const yamlText = `
name: my-build
display_name: My Build
components:
  - filename: agent.yaml
    type: agent
    name: Foo
`;
        mockArtifacts = [
            {
                filename: "build-manifest.yaml",
                mimeType: SAM_BUILD_MANIFEST_MIME_TYPE,
                version: 0,
                accumulatedContent: yamlText,
            },
        ];

        const { result } = renderHook(() => useBuilderManifest(), { wrapper: buildQueryWrapper() });

        expect(result.current.manifest?.name).toBe("my-build");
        expect(result.current.manifest?.display_name).toBe("My Build");
        expect(result.current.manifest?.components).toHaveLength(1);
        expect(result.current.manifest?.components?.[0].name).toBe("Foo");
        expect(mockDownload).not.toHaveBeenCalled();
    });

    test("gracefully returns null manifest for malformed YAML", () => {
        mockArtifacts = [
            {
                filename: "build-manifest.yaml",
                mimeType: SAM_BUILD_MANIFEST_MIME_TYPE,
                version: 0,
                accumulatedContent: "::not yaml::\n  - [",
            },
        ];

        const { result } = renderHook(() => useBuilderManifest(), { wrapper: buildQueryWrapper() });

        expect(result.current.manifest).toBeNull();
    });

    test("prefers the latest build-manifest artifact when there are multiple", () => {
        mockArtifacts = [
            {
                filename: "old.yaml",
                mimeType: SAM_BUILD_MANIFEST_MIME_TYPE,
                version: 0,
                accumulatedContent: "name: older\ncomponents: []\n",
            },
            {
                filename: "new.yaml",
                mimeType: SAM_BUILD_MANIFEST_MIME_TYPE,
                version: 1,
                accumulatedContent: "name: newer\ncomponents: []\n",
            },
        ];

        const { result } = renderHook(() => useBuilderManifest(), { wrapper: buildQueryWrapper() });

        expect(result.current.manifest?.name).toBe("newer");
    });
});
