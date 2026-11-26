// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import type { BuildManifest } from "./useBuilderManifest";

const h = vi.hoisted(() => ({
    isResponding: false,
    listManifestVersions: vi.fn<(sessionId: string, filename: string) => Promise<number[]>>(),
}));

vi.mock("@/api/artifacts", () => ({ useChatArtifacts: () => ({ data: [] }) }));
vi.mock("@/providers/contexts", () => ({ useChatContext: () => ({ sessionId: "session-1", isResponding: h.isResponding }) }));
vi.mock("@/api/builder", async importOriginal => {
    const actual = await importOriginal<typeof import("@/api/builder")>();
    return { ...actual, listManifestVersions: h.listManifestVersions };
});

import { useArtifactVersions } from "./useArtifactVersions";

const manifest = {
    name: "build",
    components: [
        { filename: "a.yaml", type: "agent", name: "a", origin: "session" },
        { filename: "b.yaml", type: "agent", name: "b", origin: "session" },
        { filename: "c.yaml", type: "agent", name: "c", origin: "session" },
    ],
} as BuildManifest;

function render() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
    const wrapper = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
    return renderHook(() => useArtifactVersions(manifest), { wrapper });
}

describe("useArtifactVersions: which lookups settled without an answer", () => {
    beforeEach(() => {
        h.isResponding = false;
        h.listManifestVersions.mockReset();
    });

    test("names a file whose version query failed and one whose config has no versions, but not one that answered", async () => {
        h.listManifestVersions.mockImplementation(async (_session, filename) => {
            if (filename === "a.yaml") return [0, 1, 2];
            if (filename === "b.yaml") throw new Error("boom");
            return [];
        });
        const { result } = render();

        await waitFor(() => expect(result.current.unresolved).toEqual(new Set(["b.yaml", "c.yaml"])));
        expect(result.current.get("a.yaml")).toBe(2);
        expect(result.current.has("b.yaml")).toBe(false);
    });

    test("names nothing while the lookups are still in flight", () => {
        h.listManifestVersions.mockReturnValue(new Promise(() => {}));
        const { result } = render();

        expect(result.current.unresolved?.size ?? 0).toBe(0);
    });

    test("names nothing while the agent streams, because the lookups are deferred", () => {
        h.isResponding = true;
        h.listManifestVersions.mockRejectedValue(new Error("boom"));
        const { result } = render();

        expect(h.listManifestVersions).not.toHaveBeenCalled();
        expect(result.current.unresolved?.size ?? 0).toBe(0);
    });
});
