// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { ArtifactWithSession } from "@/api/artifacts/types";

const BASE = "https://sam.example.test";

describe("useArtifactSource — artifact preview URL", () => {
    let useArtifactSource: typeof import("@/features/chat/file/useFileAttachmentSource").useArtifactSource;

    beforeEach(async () => {
        vi.resetModules();

        vi.doMock("@/api/client", () => ({
            api: {
                webui: {
                    get: vi.fn(async () => ({ data: [0] })),
                    getFullUrl: (endpoint: string) => `${BASE}${endpoint}`,
                },
            },
            getErrorFromResponse: vi.fn(async () => "error"),
        }));

        vi.doMock("@/utils", async importOriginal => {
            const actual = await importOriginal<typeof import("@/utils")>();
            return {
                ...actual,
                getArtifactContent: vi.fn(async () => ({ content: "AAAA", mimeType: "application/pdf" })),
            };
        });

        const mod = await import("@/features/chat/file/useFileAttachmentSource");
        useArtifactSource = mod.useArtifactSource;
    });

    function makeWrapper() {
        const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        return ({ children }: { children: React.ReactNode }) => React.createElement(QueryClientProvider, { client: queryClient }, children);
    }

    const pdfArtifact: ArtifactWithSession = {
        filename: "Solace Report[1].pdf",
        mimeType: "application/pdf",
        size: 1024,
        lastModified: "2026-05-29T00:00:00Z",
        sessionId: "sess-1",
        sessionName: null,
    };

    test("fileContent.url is a relative API path, not base-prepended (regression: doubled host on remote deployments)", async () => {
        const { result } = renderHook(() => useArtifactSource(pdfArtifact), { wrapper: makeWrapper() });

        await waitFor(() => expect(result.current.fileContent).not.toBeNull());

        const url = result.current.fileContent!.url!;
        expect(url.startsWith("/api/v1/artifacts/")).toBe(true);
        expect(url.startsWith(BASE)).toBe(false);
        expect(url).not.toContain("nethttps");
        expect(url).toBe(`/api/v1/artifacts/sess-1/${encodeURIComponent("Solace Report[1].pdf")}/versions/0`);
    });
});
