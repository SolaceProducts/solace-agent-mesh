// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { detectComponentsWithMissingCredentials } from "../builderUtils";

const PLACEHOLDER = "<<__SAM_REQUIRED__>>";
const CONNECTOR_MIME = "application/vnd.sam-connector-config+json";
const FILE = "ledger_api_connector.json";

const connectorJson = (apiKey: string) => JSON.stringify({ name: "LedgerAPI", type: "api", subtype: "openapi", values: { base_url: "https://api.example-ledger.test/v1", auth_type: "apikey", auth_apikey_value: apiKey } });

type ListEntry = { filename: string; mimeType: string; version?: number; accumulatedContent?: string; creationStatus?: "in-progress" | "completed" };

describe("useBuilderCanvasData — config version refresh", () => {
    let useBuilderCanvasData: typeof import("./useBuilderCanvasData").useBuilderCanvasData;
    let list: ListEntry[];
    let storedContent: string | null;
    let directContent: string | null;
    let sessionId: string;
    let isResponding: boolean;
    let componentStatus: string;
    let downloadGate: Promise<void> | null;
    let download: ReturnType<typeof vi.fn>;
    let client: QueryClient;
    const manifests = new Map<string, { name: string; components: Array<{ filename: string; type: string; name: string; status: string; origin: string }> }>();
    const manifest = () => {
        if (!manifests.has(componentStatus)) {
            manifests.set(componentStatus, { name: "ledger-system", components: [{ filename: FILE, type: "connector", name: "LedgerAPI", status: componentStatus, origin: "session" }] });
        }
        return manifests.get(componentStatus)!;
    };

    beforeEach(async () => {
        vi.resetModules();
        list = [];
        storedContent = null;
        directContent = null;
        sessionId = "session-1";
        isResponding = false;
        componentStatus = "validated";
        downloadGate = null;
        client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
        download = vi.fn(async () => {
            const content = storedContent;
            if (downloadGate) await downloadGate;
            if (content === null) return null;
            list = list.map(e => (e.filename === FILE ? { ...e, accumulatedContent: undefined } : e));
            return { content: btoa(content) };
        });

        vi.doMock("@/api/artifacts", () => ({
            useChatArtifacts: () => ({ data: list, isLoading: false, refetch: () => Promise.resolve() }),
        }));
        vi.doMock("@/api/remote-agents", () => ({ useRemoteAgents: () => ({ data: [] }) }));
        vi.doMock("@/providers/contexts", () => ({
            useChatContext: () => ({ downloadAndResolveArtifact: download, sessionId, isResponding, messages: [] }),
        }));
        vi.doMock("./useBuilderManifest", () => ({ useBuilderManifest: () => ({ manifest: manifest() }) }));
        vi.doMock("@/api/builder", async importOriginal => {
            const actual = await importOriginal<typeof import("@/api/builder")>();
            return { ...actual, getManifestContent: vi.fn(() => Promise.resolve(directContent)) };
        });

        useBuilderCanvasData = (await import("./useBuilderCanvasData")).useBuilderCanvasData;
    });

    const wrapper =
        () =>
        ({ children }: { children: ReactNode }) =>
            createElement(QueryClientProvider, { client }, children);
    const card = (r: { current: ReturnType<typeof useBuilderCanvasData> }) => r.current.componentData.get("LedgerAPI");
    const apiKeyOf = (r: { current: ReturnType<typeof useBuilderCanvasData> }) => card(r)?.connectorMetadata?.values?.auth_apikey_value;
    const settle = () => new Promise(resolve => setTimeout(resolve, 50));
    const listed = (version: number, extra: Partial<ListEntry> = {}): ListEntry[] => [{ filename: FILE, mimeType: CONNECTOR_MIME, version, ...extra }];
    const streaming = (content: string, extra: Partial<ListEntry> = {}): ListEntry[] => [{ filename: FILE, mimeType: CONNECTOR_MIME, accumulatedContent: content, creationStatus: "in-progress", ...extra }];
    const gate = () => {
        let open!: () => void;
        downloadGate = new Promise<void>(resolve => (open = resolve));
        return () => {
            downloadGate = null;
            open();
        };
    };

    async function renderAtVersion(version: number, apiKey: string) {
        list = listed(version);
        storedContent = connectorJson(apiKey);
        const hook = renderHook(() => useBuilderCanvasData(), { wrapper: wrapper() });
        await waitFor(() => expect(apiKeyOf(hook.result)).toBe(apiKey));
        download.mockClear();
        return hook;
    }

    test("re-resolves an in-list config when its listed version advances", async () => {
        const { result, rerender } = await renderAtVersion(0, PLACEHOLDER);
        expect(detectComponentsWithMissingCredentials(manifest(), result.current.componentData).get("LedgerAPI")).toBe(1);

        storedContent = connectorJson("lk_live_real");
        list = listed(1);
        rerender();

        await waitFor(() => expect(apiKeyOf(result)).toBe("lk_live_real"));
        expect(detectComponentsWithMissingCredentials(manifest(), result.current.componentData).size).toBe(0);
    });

    test("re-resolves a placeholder streamed and rewritten within one turn", async () => {
        list = streaming(connectorJson(PLACEHOLDER), { creationStatus: "completed" });
        isResponding = true;
        componentStatus = "draft";
        const { result, rerender } = renderHook(() => useBuilderCanvasData(), { wrapper: wrapper() });
        await waitFor(() => expect(apiKeyOf(result)).toBe(PLACEHOLDER));

        list = [];
        rerender();
        await settle();

        storedContent = connectorJson("lk_live_real");
        list = listed(1);
        rerender();
        await waitFor(() => expect(apiKeyOf(result)).toBe("lk_live_real"));
        expect(detectComponentsWithMissingCredentials(manifest(), result.current.componentData).size).toBe(0);
    });

    test("re-resolves a config first served by the direct query once it appears in the list", async () => {
        directContent = connectorJson(PLACEHOLDER);
        const { result, rerender } = renderHook(() => useBuilderCanvasData(), { wrapper: wrapper() });
        await waitFor(() => expect(apiKeyOf(result)).toBe(PLACEHOLDER));

        storedContent = connectorJson("lk_live_real");
        list = listed(1);
        rerender();

        await waitFor(() => expect(apiKeyOf(result)).toBe("lk_live_real"));
        expect(detectComponentsWithMissingCredentials(manifest(), result.current.componentData).size).toBe(0);
    });

    test("verifies streamed content once at its first settled version, then only on advances", async () => {
        list = streaming(connectorJson(PLACEHOLDER));
        const { result, rerender } = renderHook(() => useBuilderCanvasData(), { wrapper: wrapper() });
        await waitFor(() => expect(apiKeyOf(result)).toBe(PLACEHOLDER));

        storedContent = connectorJson(PLACEHOLDER);
        list = listed(0);
        rerender();
        await waitFor(() => expect(download).toHaveBeenCalledTimes(1));
        rerender();
        await settle();
        expect(download).toHaveBeenCalledTimes(1);

        storedContent = connectorJson("lk_live_real");
        list = listed(1);
        rerender();
        await waitFor(() => expect(apiKeyOf(result)).toBe("lk_live_real"));
        expect(download).toHaveBeenCalledTimes(2);
    });

    test("leaves a live stream alone even when the entry has no version yet", async () => {
        const { result, rerender } = await renderAtVersion(1, PLACEHOLDER);

        list = streaming(connectorJson("streamed-key"));
        rerender();
        await waitFor(() => expect(apiKeyOf(result)).toBe("streamed-key"));
        rerender();
        await settle();

        expect(download).not.toHaveBeenCalled();
        expect(apiKeyOf(result)).toBe("streamed-key");
    });

    test("does not treat a completed entry without a version as an advance", async () => {
        const { rerender } = await renderAtVersion(1, PLACEHOLDER);

        list = [{ filename: FILE, mimeType: CONNECTOR_MIME, creationStatus: "completed" }];
        rerender();
        await settle();
        expect(download).not.toHaveBeenCalled();
    });

    test("does not re-resolve an entry that is still being created", async () => {
        const { rerender } = await renderAtVersion(1, PLACEHOLDER);

        list = listed(2, { creationStatus: "in-progress" });
        rerender();
        await settle();
        expect(download).not.toHaveBeenCalled();
    });

    test("keeps showing the card while a re-resolve is in flight", async () => {
        const { result, rerender } = await renderAtVersion(0, PLACEHOLDER);

        const release = gate();
        storedContent = connectorJson("lk_live_real");
        list = listed(1);
        rerender();
        await waitFor(() => expect(download).toHaveBeenCalledTimes(1));
        expect(card(result)?.isLoading).toBeFalsy();
        expect(apiKeyOf(result)).toBe(PLACEHOLDER);

        release();
        await waitFor(() => expect(apiKeyOf(result)).toBe("lk_live_real"));
    });

    test("picks up a version that arrives while a failing re-resolve is in flight", async () => {
        const { result, rerender } = await renderAtVersion(0, PLACEHOLDER);

        const release = gate();
        storedContent = null;
        list = listed(1);
        rerender();
        await waitFor(() => expect(download).toHaveBeenCalledTimes(1));

        list = listed(2);
        rerender();
        storedContent = connectorJson("lk_live_real");
        release();

        await waitFor(() => expect(apiKeyOf(result)).toBe("lk_live_real"));
    });

    test("drops a fetch that settles after the session changed", async () => {
        list = listed(0);
        storedContent = connectorJson(PLACEHOLDER);
        const release = gate();
        const { result, rerender } = renderHook(() => useBuilderCanvasData(), { wrapper: wrapper() });
        await waitFor(() => expect(download).toHaveBeenCalledTimes(1));

        sessionId = "session-2";
        list = [];
        rerender();
        release();
        await settle();
        rerender();
        await settle();

        expect(apiKeyOf(result)).toBeUndefined();
    });

    test("keeps the card on a failed re-resolve and retries only when the version moves again", async () => {
        const { result, rerender } = await renderAtVersion(0, PLACEHOLDER);

        storedContent = null;
        list = listed(1);
        rerender();
        await waitFor(() => expect(download).toHaveBeenCalledTimes(1));
        await settle();
        rerender();
        await settle();
        expect(download).toHaveBeenCalledTimes(1);
        expect(apiKeyOf(result)).toBe(PLACEHOLDER);
        expect(card(result)?.isLoading).toBeFalsy();

        storedContent = connectorJson("lk_live_real");
        list = listed(2);
        rerender();
        await waitFor(() => expect(apiKeyOf(result)).toBe("lk_live_real"));
    });

    test("does not refetch while the listed version is unchanged", async () => {
        const { result, rerender } = await renderAtVersion(0, PLACEHOLDER);

        storedContent = connectorJson("not-refetched");
        rerender();
        rerender();
        await settle();
        expect(download).not.toHaveBeenCalled();
        expect(apiKeyOf(result)).toBe(PLACEHOLDER);
    });
});
