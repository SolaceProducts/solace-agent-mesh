// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement, type ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { builderKeys } from "@/api/builder";
import type { ApplyBuildResponse, DroppedResource, RemovalResult } from "@/api/builder/applyTypes";
import { api } from "@/api/client";

import type { BuildManifest } from "./useBuilderManifest";
import { useDroppedResources } from "./useDroppedResources";

const BUILDS_PATH = "/api/internal/platform/builder/builds";
const REMOVALS_PATH = "/api/internal/platform/builder/removals";
const previewBuild = vi.fn();
const removeDropped = vi.fn();
let chat = { sessionId: "0197b2c4-0000-7000-8000-00000000000a", isResponding: false };
let serverApply = true;

vi.mock("@/providers/contexts", () => ({ useChatContext: () => chat }));
vi.mock("@/hooks/useIsBuilderServerApplyEnabled", () => ({ useIsBuilderServerApplyEnabled: () => serverApply }));

const agentYaml = "apps:\n  - name: kept\n";
const fetchConfigByFilename = vi.fn(async () => agentYaml);

const droppedAgent: DroppedResource = { kind: "agent", id: "0197b2c4-0000-7000-8000-0000000000d1", name: "Name Nothing In The Manifest Resembles", createdBySession: true, deployment: "deployed" };

const manifestOf = (...names: string[]): BuildManifest => ({
    name: "build",
    components: names.map(name => ({ name, type: "agent", filename: `${name}.yaml`, status: "deployed" })),
});

const response = (over: Partial<ApplyBuildResponse> = {}): ApplyBuildResponse => ({ dryRun: true, operations: [], summary: { created: 0, updated: 0, unchanged: 0, failed: 0, skipped: 0 }, dropped: [], ...over });

function wrapper() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    return ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
}

function mount(manifest: BuildManifest | null = manifestOf("kept")) {
    return renderHook(() => useDroppedResources({ manifest, fetchConfigByFilename, versions: new Map() }), { wrapper: wrapper() });
}

beforeEach(() => {
    vi.clearAllMocks();
    removeDropped.mockReset();
    vi.spyOn(api.platform, "post").mockImplementation(((url: string, body: unknown) =>
        url === REMOVALS_PATH ? removeDropped(body) : url === BUILDS_PATH ? previewBuild(body) : Promise.reject(new Error(`unexpected POST ${url}`))) as typeof api.platform.post);
    chat = { sessionId: "0197b2c4-0000-7000-8000-00000000000a", isResponding: false };
    serverApply = true;
    previewBuild.mockResolvedValue(response({ dropped: [droppedAgent] }));
});

describe("useDroppedResources", () => {
    test("asks the build endpoint, with the session and the manifest's configs, and shows its list exactly", async () => {
        const { result } = mount();

        await waitFor(() => expect(result.current.view.status).toBe("ready"));
        expect(previewBuild).toHaveBeenCalledWith({ dryRun: true, builderSessionId: chat.sessionId, components: [expect.objectContaining({ kind: "agent", name: "kept", content: agentYaml })] });
        expect(result.current.view.resources).toEqual([droppedAgent]);
    });

    test("asks even when the manifest has no component left, which is how a plan that dropped everything learns what it left", async () => {
        const { result } = mount(manifestOf());

        await waitFor(() => expect(result.current.view.status).toBe("ready"));
        expect(previewBuild).toHaveBeenCalledWith({ dryRun: true, builderSessionId: chat.sessionId, components: [] });
    });

    test("carries the server's word that it could not tell", async () => {
        previewBuild.mockResolvedValue(response({ droppedCheckFailed: true }));
        const { result } = mount();

        await waitFor(() => expect(result.current.view.checkFailed).toBe(true));
        expect(result.current.view.resources).toEqual([]);
    });

    const apiError = (message: string, status?: number) => Object.assign(new Error(message), status === undefined ? {} : { status });

    test("stays quiet, and invents no answer, when the endpoint refuses the manifest", async () => {
        previewBuild.mockRejectedValue(apiError('agent "kept": unparseable', 422));
        const { result } = mount();

        await waitFor(() => expect(previewBuild).toHaveBeenCalled());
        await waitFor(() => expect(result.current.view.status).toBe("unavailable"));
        expect(result.current.view.resources).toEqual([]);
        expect(result.current.view.checkFailed).toBe(false);
    });

    test("stays quiet while a config is not written yet", async () => {
        fetchConfigByFilename.mockResolvedValueOnce(null as unknown as string);
        const { result } = mount();

        await waitFor(() => expect(fetchConfigByFilename).toHaveBeenCalled());
        await new Promise(resolve => setTimeout(resolve, 50));
        expect(previewBuild).not.toHaveBeenCalled();
        expect(result.current.view.checkFailed).toBe(false);
    });

    test.each([
        ["the platform failing", apiError("internal error", 500)],
        ["the platform being unreachable", new TypeError("Failed to fetch")],
    ])("says it could not check when the question fails by %s, never an empty answer", async (_name, failure) => {
        previewBuild.mockRejectedValue(failure);
        const { result } = mount();

        await waitFor(() => expect(result.current.view.checkFailed).toBe(true));
        expect(result.current.view.resources).toEqual([]);
    });

    test("never shows the last answer once the platform refuses the manifest it is asked again with", async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
        const wrap = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
        const { result } = renderHook(() => useDroppedResources({ manifest: manifestOf("kept"), fetchConfigByFilename, versions: new Map() }), { wrapper: wrap });
        await waitFor(() => expect(result.current.view.resources).toEqual([droppedAgent]));

        previewBuild.mockRejectedValue(apiError('agent "kept": unparseable', 422));
        await act(async () => {
            await client.invalidateQueries({ queryKey: builderKeys.droppedAll() });
        });

        await waitFor(() => expect(result.current.view.resources).toEqual([]));
        expect(result.current.view.checkFailed).toBe(false);
    });

    test("never shows the last answer once asking again has failed", async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
        const wrap = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
        const { result } = renderHook(() => useDroppedResources({ manifest: manifestOf("kept"), fetchConfigByFilename, versions: new Map() }), { wrapper: wrap });
        await waitFor(() => expect(result.current.view.resources).toEqual([droppedAgent]));

        previewBuild.mockRejectedValue(apiError("internal error", 503));
        await act(async () => {
            await client.invalidateQueries({ queryKey: builderKeys.droppedAll() });
        });

        await waitFor(() => expect(result.current.view.checkFailed).toBe(true));
        expect(result.current.view.resources).toEqual([]);
    });

    test("is loading while the question is asked again, and never offers removal from an old answer as settled", async () => {
        const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
        const wrap = ({ children }: { children: ReactNode }) => createElement(QueryClientProvider, { client }, children);
        const { result } = renderHook(() => useDroppedResources({ manifest: manifestOf("kept"), fetchConfigByFilename, versions: new Map() }), { wrapper: wrap });
        await waitFor(() => expect(result.current.view.status).toBe("ready"));

        previewBuild.mockReturnValue(new Promise(() => {}));
        act(() => {
            void client.invalidateQueries({ queryKey: builderKeys.droppedAll() });
        });

        await waitFor(() => expect(result.current.view.status).toBe("loading"));
    });

    test("never shows another session's list while this session's is being asked", async () => {
        const otherSession = "0197b2c4-0000-7000-8000-00000000000b";
        const { result, rerender } = mount();
        await waitFor(() => expect(result.current.view.resources).toEqual([droppedAgent]));

        previewBuild.mockReturnValue(new Promise(() => {}));
        chat = { ...chat, sessionId: otherSession };
        rerender();

        await waitFor(() => expect(previewBuild).toHaveBeenCalledTimes(2));
        expect(result.current.view.resources).toEqual([]);
    });

    test.each([
        ["while the agent is still responding", () => (chat = { ...chat, isResponding: true })],
        ["with server-side apply off", () => (serverApply = false)],
        ["without a session", () => (chat = { ...chat, sessionId: "" })],
    ])("does not ask %s", async (_name, arrange) => {
        arrange();
        const { result } = mount();

        await new Promise(resolve => setTimeout(resolve, 50));
        expect(previewBuild).not.toHaveBeenCalled();
        expect(result.current.view.resources).toEqual([]);
    });

    test("does not ask without a manifest", async () => {
        mount(null);
        await new Promise(resolve => setTimeout(resolve, 50));
        expect(previewBuild).not.toHaveBeenCalled();
    });

    test("removes exactly the resources it is given, in this session", async () => {
        removeDropped.mockResolvedValue({ results: [{ kind: "agent", id: droppedAgent.id, status: "removed" }] });
        const { result } = mount();
        await waitFor(() => expect(result.current.view.status).toBe("ready"));

        let outcome: RemovalResult[] = [];
        await act(async () => {
            outcome = await result.current.remove([droppedAgent]);
        });

        expect(removeDropped).toHaveBeenCalledWith({ builderSessionId: chat.sessionId, resources: [{ kind: "agent", id: droppedAgent.id }] });
        expect(outcome).toEqual([{ kind: "agent", id: droppedAgent.id, status: "removed" }]);
    });

    test("removes the set it is given, not the list the server shows now", async () => {
        const second: DroppedResource = { ...droppedAgent, id: "0197b2c4-0000-7000-8000-0000000000d2", name: "Second Dropped Agent" };
        previewBuild.mockResolvedValue(response({ dropped: [droppedAgent, second] }));
        removeDropped.mockResolvedValue({ results: [{ kind: "agent", id: droppedAgent.id, status: "removed" }] });
        const { result } = mount();
        await waitFor(() => expect(result.current.view.resources).toHaveLength(2));

        await act(async () => {
            await result.current.remove([droppedAgent]);
        });

        expect(removeDropped).toHaveBeenCalledTimes(1);
        expect(removeDropped).toHaveBeenCalledWith({ builderSessionId: chat.sessionId, resources: [{ kind: "agent", id: droppedAgent.id }] });
    });

    test("asks again after a removal, so what was removed is no longer listed", async () => {
        removeDropped.mockResolvedValue({ results: [{ kind: "agent", id: droppedAgent.id, status: "removed" }] });
        const { result } = mount();
        await waitFor(() => expect(result.current.view.resources).toEqual([droppedAgent]));
        const asked = previewBuild.mock.calls.length;

        previewBuild.mockResolvedValue(response({ dropped: [] }));
        await act(async () => {
            await result.current.remove([droppedAgent]);
        });

        await waitFor(() => expect(previewBuild.mock.calls.length).toBeGreaterThan(asked));
        await waitFor(() => expect(result.current.view.resources).toEqual([]));
    });

    test.each([400, 409, 422])("stays quiet when the platform answers %i, refusing the manifest", async status => {
        previewBuild.mockRejectedValue(apiError("refused", status));
        const { result } = mount();

        await waitFor(() => expect(previewBuild).toHaveBeenCalled());
        await waitFor(() => expect(result.current.view.status).toBe("unavailable"));
        expect(result.current.view.checkFailed).toBe(false);
    });

    test.each([401, 403, 404, 408, 429, 500, 502, 503])("says it could not check when the platform answers %i", async status => {
        previewBuild.mockRejectedValue(apiError("failed", status));
        const { result } = mount();

        await waitFor(() => expect(result.current.view.checkFailed).toBe(true));
        expect(result.current.view.resources).toEqual([]);
    });

    test("refuses to remove without a Builder session, and sends nothing", async () => {
        chat = { ...chat, sessionId: "" };
        const { result } = mount();

        await act(async () => {
            await expect(result.current.remove([droppedAgent])).rejects.toThrow(/no Builder session/);
        });
        expect(removeDropped).not.toHaveBeenCalled();
    });

    test("asks again when the plan's inputs change without their number changing", async () => {
        const { rerender } = renderHook(({ manifest }) => useDroppedResources({ manifest, fetchConfigByFilename, versions: new Map() }), {
            wrapper: wrapper(),
            initialProps: { manifest: manifestOf("kept") as BuildManifest | null },
        });
        await waitFor(() => expect(previewBuild).toHaveBeenCalledTimes(1));

        rerender({ manifest: manifestOf("renamed") });

        await waitFor(() => expect(previewBuild).toHaveBeenCalledTimes(2));
    });

    test("never removes on its own: a list that appears, changes or empties triggers no removal", async () => {
        const { result, rerender } = renderHook(({ manifest }) => useDroppedResources({ manifest, fetchConfigByFilename, versions: new Map() }), {
            wrapper: wrapper(),
            initialProps: { manifest: manifestOf("kept", "other") as BuildManifest | null },
        });
        await waitFor(() => expect(result.current.view.status).toBe("ready"));

        rerender({ manifest: manifestOf("kept") });
        await waitFor(() => expect(previewBuild).toHaveBeenCalledTimes(2));
        rerender({ manifest: manifestOf() });
        await waitFor(() => expect(previewBuild).toHaveBeenCalledTimes(3));

        expect(removeDropped).not.toHaveBeenCalled();
    });

    test("keeps a failed removal against its resource while the server still lists it, and forgets it once the resource is gone", async () => {
        removeDropped.mockResolvedValueOnce({ results: [{ kind: "agent", id: droppedAgent.id, status: "failed", error: { message: "undeploy failed", errorId: "e", httpStatus: 502 } }] });
        const { result } = mount();
        await waitFor(() => expect(result.current.view.status).toBe("ready"));

        await act(async () => {
            await result.current.remove([droppedAgent]);
        });
        await waitFor(() => expect(result.current.view.failures.get(droppedAgent.id)).toContain("undeploy failed"));

        removeDropped.mockResolvedValueOnce({ results: [{ kind: "agent", id: droppedAgent.id, status: "removed" }] });
        await act(async () => {
            await result.current.remove([droppedAgent]);
        });
        await waitFor(() => expect(result.current.view.failures.size).toBe(0));
    });

    test("records a request that failed outright against the resources it was for, and still throws it", async () => {
        removeDropped.mockRejectedValueOnce(new Error("network down"));
        const { result } = mount();
        await waitFor(() => expect(result.current.view.status).toBe("ready"));

        await act(async () => {
            await expect(result.current.remove([droppedAgent])).rejects.toThrow("network down");
        });
        await waitFor(() => expect(result.current.view.failures.get(droppedAgent.id)).toContain("network down"));
    });

    test("drops the problem of a resource the list no longer has", async () => {
        removeDropped.mockResolvedValueOnce({ results: [{ kind: "agent", id: droppedAgent.id, status: "failed", error: { message: "undeploy failed", errorId: "e", httpStatus: 502 } }] });
        const { result, rerender } = renderHook(({ manifest }) => useDroppedResources({ manifest, fetchConfigByFilename, versions: new Map() }), {
            wrapper: wrapper(),
            initialProps: { manifest: manifestOf("kept") },
        });
        await waitFor(() => expect(result.current.view.status).toBe("ready"));
        await act(async () => {
            await result.current.remove([droppedAgent]);
        });
        await waitFor(() => expect(result.current.view.failures.size).toBe(1));

        previewBuild.mockResolvedValue(response({ dropped: [] }));
        rerender({ manifest: manifestOf("kept", "another") });

        await waitFor(() => expect(result.current.view.resources).toEqual([]));
        expect(result.current.view.failures.size).toBe(0);
    });
});
