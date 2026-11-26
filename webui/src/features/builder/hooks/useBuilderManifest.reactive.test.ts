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

const DRAFT = `name: spanish-translator-system
components:
  - filename: spanish_translator.yaml
    type: agent
    name: SpanishTranslator
    status: draft
`;
const VALIDATED = `name: spanish-translator-system
full_validation_passed: true
components:
  - filename: spanish_translator.yaml
    type: agent
    name: SpanishTranslator
    status: validated
`;

describe("useBuilderManifest — reactive latest-content refresh", () => {
    let useBuilderManifest: typeof import("./useBuilderManifest").useBuilderManifest;
    let latestContent: string | undefined;

    beforeEach(async () => {
        vi.resetModules();
        latestContent = DRAFT;

        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({
                data: [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 0 }],
                isLoading: false,
                refetch: () => Promise.resolve(),
            }),
        }));

        vi.doMock("@/api/builder", () => ({
            builderKeys: { manifestContent: (...a: unknown[]) => ["mc", ...a] },
            getManifestContent: () => Promise.resolve(null),
            useManifestContentQuery: () => ({ data: latestContent }),
            usePatchSessionNameMutation: () => ({ mutateAsync: vi.fn().mockResolvedValue(undefined) }),
            useUploadManifestMutation: () => ({ mutateAsync: vi.fn().mockResolvedValue(true) }),
        }));

        vi.doMock("@/providers/contexts", () => ({
            useChatContext: () => ({ downloadAndResolveArtifact: vi.fn().mockResolvedValue(null), sessionId: "session-1" }),
        }));

        useBuilderManifest = (await import("./useBuilderManifest")).useBuilderManifest;
    });

    const DRAFT_REORDERED = `components:
  - name: "SpanishTranslator"
    status: draft
    type: agent
    filename: spanish_translator.yaml
name: spanish-translator-system
`;

    test("keeps a STABLE manifest reference across a structurally-identical refetch (no relayout blink)", async () => {
        const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });
        await waitFor(() => expect(result.current.manifest?.components?.[0]?.status).toBe("draft"));
        const firstRef = result.current.manifest;

        latestContent = DRAFT_REORDERED;
        rerender();
        await new Promise(r => setTimeout(r, 20));
        expect(result.current.manifest).toBe(firstRef);

        latestContent = VALIDATED;
        rerender();
        await waitFor(() => expect(result.current.manifest?.components?.[0]?.status).toBe("validated"));
        expect(result.current.manifest).not.toBe(firstRef);
    });

    test("advances to a newer manifest version on refetch without a version bump", async () => {
        const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });

        await waitFor(() => expect(result.current.manifest?.components?.[0]?.status).toBe("draft"));

        latestContent = VALIDATED;
        rerender();

        await waitFor(() => expect(result.current.manifest?.components?.[0]?.status).toBe("validated"));
        expect(result.current.manifest?.full_validation_passed).toBe(true);
    });
});
