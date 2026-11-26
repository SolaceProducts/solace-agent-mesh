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

const DRAFT = `name: financial-advisor-system
components:
  - filename: financial_advisor_agent.yaml
    type: agent
    name: FinancialAdvisor
    status: draft
`;
const VALIDATED = `name: financial-advisor-system
full_validation_passed: true
components:
  - filename: financial_advisor_agent.yaml
    type: agent
    name: FinancialAdvisor
    status: validated
`;

describe("useBuilderManifest — recovery when the working manifest is absent from the artifact list", () => {
    let useBuilderManifest: typeof import("./useBuilderManifest").useBuilderManifest;
    let persistedContent: string | null;
    let artifacts: Array<Record<string, unknown>>;
    let currentSessionId: string;
    let contentBySession: Record<string, string | null>;

    function contentFor(session: unknown): string | null {
        const s = session as string;
        return s in contentBySession ? contentBySession[s] : persistedContent;
    }

    beforeEach(async () => {
        vi.resetModules();
        persistedContent = VALIDATED;
        artifacts = [];
        currentSessionId = "session-1";
        contentBySession = {};

        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({ data: artifacts, isLoading: false, refetch: () => Promise.resolve() }),
        }));

        vi.doMock("@/api/builder", () => ({
            builderKeys: { manifestContent: (...a: unknown[]) => ["mc", ...a] },
            getManifestContent: (session: unknown) => Promise.resolve(contentFor(session)),
            useManifestContentQuery: (session: unknown, filename: unknown, _version: unknown, enabled: boolean) => ({
                data: enabled && filename ? (contentFor(session) ?? undefined) : undefined,
            }),
            usePatchSessionNameMutation: () => ({ mutateAsync: vi.fn().mockResolvedValue(undefined) }),
            useUploadManifestMutation: () => ({ mutateAsync: vi.fn().mockResolvedValue({ ok: true }) }),
        }));

        vi.doMock("@/providers/contexts", () => ({
            useChatContext: () => ({ downloadAndResolveArtifact: vi.fn().mockResolvedValue(null), sessionId: currentSessionId }),
        }));

        useBuilderManifest = (await import("./useBuilderManifest")).useBuilderManifest;
    });

    test("loads the persisted manifest on reload when it is absent from the artifact list", async () => {
        const { result } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });

        await waitFor(() => expect(result.current.manifest?.components?.[0]?.status).toBe("validated"));
        expect(result.current.manifest?.full_validation_passed).toBe(true);
        expect(result.current.manifest?.name).toBe("financial-advisor-system");
    });

    test("resolves to null (not stuck loading) for a session with no manifest yet", async () => {
        persistedContent = null;

        const { result } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });

        await waitFor(() => expect(result.current.isLoading).toBe(false));
        expect(result.current.manifest).toBeNull();
    });

    test("recovers the persisted manifest on an in-app session SWITCH (not just a fresh reload)", async () => {
        currentSessionId = "session-empty";
        persistedContent = null;

        const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });
        await waitFor(() => expect(result.current.isLoading).toBe(false));
        expect(result.current.manifest).toBeNull();

        currentSessionId = "session-2";
        persistedContent = VALIDATED;
        rerender();

        await waitFor(() => expect(result.current.manifest?.name).toBe("financial-advisor-system"));
        expect(result.current.manifest?.components?.[0]?.status).toBe("validated");
    });

    test("clears the canvas when switching from a manifest session to an empty one (no stale manifest)", async () => {
        const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });
        await waitFor(() => expect(result.current.manifest?.name).toBe("financial-advisor-system"));

        currentSessionId = "session-empty";
        contentBySession = { "session-empty": null };
        rerender();

        await waitFor(() => expect(result.current.manifest).toBeNull());
    });

    test("shows the switched-to session's manifest, never the previous session's (no cross-session leak)", async () => {
        const OTHER = `name: other-system
components:
  - filename: other_agent.yaml
    type: agent
    name: OtherAgent
    status: validated
`;
        contentBySession = { "session-1": VALIDATED, "session-2": OTHER };
        const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });
        await waitFor(() => expect(result.current.manifest?.name).toBe("financial-advisor-system"));

        currentSessionId = "session-2";
        rerender();

        await waitFor(() => expect(result.current.manifest?.name).toBe("other-system"));
        expect(result.current.manifest?.components?.[0]?.name).toBe("OtherAgent");
    });

    test("keeps the streamed draft authoritative while accumulatedContent is present", async () => {
        artifacts = [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 0, accumulatedContent: DRAFT }];
        persistedContent = VALIDATED;

        const { result } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });

        await waitFor(() => expect(result.current.manifest?.components?.[0]?.status).toBe("draft"));
        expect(result.current.manifest?.full_validation_passed).toBeUndefined();
    });
});
