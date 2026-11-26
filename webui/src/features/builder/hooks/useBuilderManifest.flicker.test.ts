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

const manifestYaml = (comps: Array<[string, string]>) => `name: sys\ncomponents:\n` + comps.map(([name, status]) => `  - filename: ${name}.yaml\n    type: agent\n    name: ${name}\n    status: ${status}\n`).join("");

const workflowManifest = (ownsA: boolean) =>
    `name: sys\ncomponents:\n  - filename: WF.yaml\n    type: workflow\n    name: WF\n    status: draft\n` +
    (ownsA ? `    nodes:\n      - id: n1\n        type: agent\n        agent_name: A\n` : ``) +
    `  - filename: A.yaml\n    type: agent\n    name: A\n    status: draft\n`;

describe("useBuilderManifest — streaming flicker guard", () => {
    let useBuilderManifest: typeof import("./useBuilderManifest").useBuilderManifest;
    let content: string;
    let creationStatus: "in-progress" | "completed" | "failed" | undefined;

    beforeEach(async () => {
        vi.resetModules();
        content = manifestYaml([
            ["A", "draft"],
            ["B", "draft"],
        ]);
        creationStatus = "in-progress";

        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({
                data: [{ filename: "build_manifest.yaml", mimeType: SAM_BUILD_MANIFEST_MIME_TYPE, version: 0, accumulatedContent: content, creationStatus }],
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
            useChatContext: () => ({ downloadAndResolveArtifact: vi.fn().mockResolvedValue(null), sessionId: "session-1" }),
        }));

        useBuilderManifest = (await import("./useBuilderManifest")).useBuilderManifest;
    });

    const names = (r: { current: { manifest: { components?: Array<{ name?: string }> } | null } }) => (r.current.manifest?.components ?? []).map(c => c.name);

    test("first plan streams in live (grows before any completion)", async () => {
        content = manifestYaml([["A", "draft"]]);
        const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });
        await waitFor(() => expect(names(result)).toEqual(["A"]));

        content = manifestYaml([
            ["A", "draft"],
            ["B", "draft"],
        ]);
        rerender();
        await waitFor(() => expect(names(result)).toEqual(["A", "B"]));
    });

    test("holds the complete manifest during a rewrite and snaps on completion (the flicker fix)", async () => {
        const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });
        await waitFor(() => expect(names(result)).toEqual(["A", "B"]));

        creationStatus = "completed";
        rerender();
        await waitFor(() => expect(names(result)).toEqual(["A", "B"]));

        creationStatus = "in-progress";
        content = manifestYaml([["A", "draft"]]);
        rerender();
        await new Promise(r => setTimeout(r, 20));
        expect(names(result)).toEqual(["A", "B"]);

        content = manifestYaml([
            ["A", "draft"],
            ["B", "draft"],
            ["C", "draft"],
        ]);
        rerender();
        await new Promise(r => setTimeout(r, 20));
        expect(names(result)).toEqual(["A", "B"]);

        creationStatus = "completed";
        rerender();
        await waitFor(() => expect(names(result)).toEqual(["A", "B", "C"]));
    });

    test("a genuine removal is adopted once the rewrite completes", async () => {
        const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });
        await waitFor(() => expect(names(result)).toEqual(["A", "B"]));
        creationStatus = "completed";
        rerender();
        await waitFor(() => expect(names(result)).toEqual(["A", "B"]));

        content = manifestYaml([["A", "draft"]]);
        creationStatus = "completed";
        rerender();
        await waitFor(() => expect(names(result)).toEqual(["A"]));
    });

    test("holds during ownership churn — same components, but a workflow's nodes[] not yet written (no removal)", async () => {
        content = workflowManifest(true);
        const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });
        await waitFor(() => expect(names(result)).toEqual(["WF", "A"]));
        creationStatus = "completed";
        rerender();
        await waitFor(() => expect((result.current.manifest?.components?.find(c => c.name === "WF") as { nodes?: unknown[] } | undefined)?.nodes?.length).toBe(1));

        creationStatus = "in-progress";
        content = workflowManifest(false);
        rerender();
        await new Promise(r => setTimeout(r, 20));
        expect(names(result)).toEqual(["WF", "A"]);
        expect((result.current.manifest?.components?.find(c => c.name === "WF") as { nodes?: unknown[] } | undefined)?.nodes?.length).toBe(1);
    });

    test("creationStatus undefined (persisted/reload) adopts live — never held", async () => {
        creationStatus = undefined;
        content = manifestYaml([["A", "draft"]]);
        const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });
        await waitFor(() => expect(names(result)).toEqual(["A"]));
        content = manifestYaml([
            ["A", "draft"],
            ["B", "draft"],
        ]);
        rerender();
        await waitFor(() => expect(names(result)).toEqual(["A", "B"]));
    });

    test("a failed stream holds the last-good manifest (does not adopt the truncated partial)", async () => {
        const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });
        await waitFor(() => expect(names(result)).toEqual(["A", "B"]));
        creationStatus = "completed";
        rerender();
        await waitFor(() => expect(names(result)).toEqual(["A", "B"]));

        creationStatus = "failed";
        content = manifestYaml([["A", "draft"]]);
        rerender();
        await new Promise(r => setTimeout(r, 20));
        expect(names(result)).toEqual(["A", "B"]);
    });

    test("status-only rewrite is held during streaming, applied on completion", async () => {
        const { result, rerender } = renderHook(() => useBuilderManifest(), { wrapper: wrapper() });
        await waitFor(() => expect(result.current.manifest?.components?.[0]?.status).toBe("draft"));
        creationStatus = "completed";
        rerender();
        await waitFor(() => expect(names(result)).toEqual(["A", "B"]));

        creationStatus = "in-progress";
        content = manifestYaml([
            ["A", "validated"],
            ["B", "validated"],
        ]);
        rerender();
        await new Promise(r => setTimeout(r, 20));
        expect(result.current.manifest?.components?.[0]?.status).toBe("draft");

        creationStatus = "completed";
        rerender();
        await waitFor(() => expect(result.current.manifest?.components?.every(c => c.status === "validated")).toBe(true));
    });
});
