// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { SHOW_WORKING_ARTIFACTS_STORAGE_KEY } from "@/features/chat/artifactTags";

import type { ArtifactInfo } from "@/types";

const SESSION_ID = "session-panel";
const OTHER_SESSION_ID = "session-other";
const PROJECT_ID = "project-panel";
const SESSION_LIST_URL = `/api/v1/artifacts/${SESSION_ID}?visibility=all`;
const PROJECT_LIST_URL = `/api/v1/artifacts/null?projectId=${PROJECT_ID}&visibility=all`;

const deck: ArtifactInfo = { filename: "deck.md", mimeType: "text/markdown", size: 10, lastModified: "2026-09-25T00:00:00Z", uri: `artifact://${SESSION_ID}/deck.md` };
const notes: ArtifactInfo = { ...deck, filename: "notes.md", uri: `artifact://${SESSION_ID}/notes.md` };
const spec: ArtifactInfo = { ...deck, filename: "deck-spec.yaml", mimeType: "application/yaml", uri: `artifact://${SESSION_ID}/deck-spec.yaml`, tags: ["__working"] };

describe("Files panel artifact list", () => {
    let artifacts: typeof import("@/api/artifacts");
    let workingToggle: typeof import("@/hooks/useShowWorkingArtifacts");
    let previewModule: typeof import("@/hooks/useArtifactPreview");
    let chatContextModule: typeof import("@/providers/contexts/ChatContext");
    let projectContextModule: typeof import("@/providers/contexts/ProjectContext");
    let getMock: ReturnType<typeof vi.fn>;
    let queryClient: QueryClient;
    let server: { files: ArtifactInfo[]; hold: boolean };
    let held: Array<() => void>;
    let scope: { sessionId: string; projectId: string | null };

    function respond(url: string) {
        const includeWorking = url.includes("visibility=all");
        const answer = { data: includeWorking ? [...server.files] : server.files.filter(artifact => !artifact.tags?.includes("__working")) };
        if (!server.hold) return Promise.resolve(answer);
        return new Promise(resolve => held.push(() => resolve(answer)));
    }

    async function importWithToggle(on: boolean) {
        localStorage.setItem(SHOW_WORKING_ARTIFACTS_STORAGE_KEY, String(on));
        vi.resetModules();
        getMock = vi.fn(respond);
        vi.doMock("@/api/client", () => ({ api: { webui: { get: getMock } } }));
        artifacts = await import("@/api/artifacts");
        workingToggle = await import("@/hooks/useShowWorkingArtifacts");
        previewModule = await import("@/hooks/useArtifactPreview");
        chatContextModule = await import("@/providers/contexts/ChatContext");
        projectContextModule = await import("@/providers/contexts/ProjectContext");
        queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    }

    function wrapper({ children }: { children: React.ReactNode }) {
        const chat = { sessionId: scope.sessionId, isSharedSession: false } as unknown as import("@/providers/contexts/ChatContext").ChatContextValue;
        const project = { activeProject: scope.projectId ? { id: scope.projectId } : null } as unknown as import("@/types/projects").ProjectContextValue;
        return React.createElement(
            QueryClientProvider,
            { client: queryClient },
            React.createElement(chatContextModule.ChatContext.Provider, { value: chat }, React.createElement(projectContextModule.ProjectContext.Provider, { value: project }, children))
        );
    }

    function renderPanelAndSharedList() {
        return renderHook(
            () => ({
                panel: artifacts.useArtifactsWithWorking(),
                shared: artifacts.useChatArtifacts(),
                toggle: workingToggle.useShowWorkingArtifacts(),
            }),
            { wrapper }
        );
    }

    const names = (list: ArtifactInfo[]) => list.map(artifact => artifact.filename).sort();
    const settled = () => waitFor(() => expect(queryClient.isFetching()).toBe(0));
    const sessionKey = () => artifacts.artifactKeys.session(SESSION_ID);
    const requestedUrls = () => getMock.mock.calls.map(([url]) => url);

    beforeEach(() => {
        localStorage.removeItem(SHOW_WORKING_ARTIFACTS_STORAGE_KEY);
        server = { files: [deck, spec], hold: false };
        held = [];
        scope = { sessionId: SESSION_ID, projectId: null };
    });
    afterEach(() => localStorage.removeItem(SHOW_WORKING_ARTIFACTS_STORAGE_KEY));

    test("one request fills both: the panel knows the working file, the shared list doesn't", async () => {
        await importWithToggle(false);
        const { result } = renderPanelAndSharedList();

        await waitFor(() => expect(names(result.current.panel.allArtifacts)).toEqual(["deck-spec.yaml", "deck.md"]));
        expect(names(result.current.panel.artifacts)).toEqual(["deck.md"]);
        expect(names(result.current.shared.data)).toEqual(["deck.md"]);
        expect(requestedUrls()).toEqual([SESSION_LIST_URL]);
    });

    test("turning the toggle on reveals the working file without another request", async () => {
        await importWithToggle(false);
        const { result } = renderPanelAndSharedList();
        await waitFor(() => expect(names(result.current.panel.allArtifacts)).toContain("deck-spec.yaml"));
        const requestsBefore = getMock.mock.calls.length;

        act(() => result.current.toggle.toggleShowWorkingArtifacts());

        expect(names(result.current.panel.artifacts)).toEqual(["deck-spec.yaml", "deck.md"]);
        expect(getMock.mock.calls.length).toBe(requestsBefore);
    });

    test("the shared list never holds working files, even with the toggle on", async () => {
        await importWithToggle(true);
        const { result } = renderPanelAndSharedList();

        await waitFor(() => expect(names(result.current.panel.artifacts)).toEqual(["deck-spec.yaml", "deck.md"]));
        expect(names(result.current.shared.data)).toEqual(["deck.md"]);
    });

    test("in project scope the panel knows the working file and the shared project list doesn't", async () => {
        scope = { sessionId: "", projectId: PROJECT_ID };
        await importWithToggle(true);
        const { result } = renderPanelAndSharedList();

        await waitFor(() => expect(names(result.current.panel.artifacts)).toEqual(["deck-spec.yaml", "deck.md"]));
        expect(names(result.current.shared.data)).toEqual(["deck.md"]);
        expect(requestedUrls()).toEqual([PROJECT_LIST_URL]);
    });

    test("a deleted working file leaves the panel with the shared list's refetch", async () => {
        await importWithToggle(true);
        const { result } = renderPanelAndSharedList();
        await settled();
        expect(names(result.current.panel.artifacts)).toContain("deck-spec.yaml");

        server.files = [deck];
        await act(() => queryClient.invalidateQueries({ queryKey: sessionKey() }));
        await settled();

        expect(names(result.current.panel.artifacts)).toEqual(["deck.md"]);
    });

    test.each([
        ["session", () => ({ sessionId: SESSION_ID, projectId: null })],
        ["project", () => ({ sessionId: "", projectId: PROJECT_ID })],
    ])("in %s scope a superseded list response landing last doesn't bring back a deleted working file", async (_, scopeFor) => {
        scope = scopeFor();
        await importWithToggle(true);
        const { result } = renderPanelAndSharedList();
        await settled();
        const listKey = scope.projectId ? artifacts.artifactKeys.project(PROJECT_ID) : sessionKey();

        server.hold = true;
        void queryClient.refetchQueries({ queryKey: listKey, exact: true });
        server.files = [deck];
        void queryClient.invalidateQueries({ queryKey: listKey });
        await waitFor(() => expect(held).toHaveLength(2));
        const [staleResponse, currentResponse] = held;
        const [staleSignal, currentSignal] = getMock.mock.calls.slice(-2).map(([, options]) => options?.signal);
        expect(staleSignal).toBeInstanceOf(AbortSignal);
        expect(staleSignal.aborted).toBe(true);
        expect(currentSignal.aborted).toBe(false);

        await act(async () => currentResponse());
        await act(async () => staleResponse());
        await settled();

        expect(names(result.current.shared.data)).toEqual(["deck.md"]);
        expect(names(result.current.panel.artifacts)).toEqual(["deck.md"]);
    });

    test("a working file saved live stays listed after the post-save refetch strips it from the shared list", async () => {
        server.files = [deck];
        await importWithToggle(true);
        const { result } = renderPanelAndSharedList();
        await settled();

        server.files = [deck, spec];
        act(() => queryClient.setQueryData(sessionKey(), [deck, spec]));
        await waitFor(() => expect(names(result.current.panel.artifacts)).toContain("deck-spec.yaml"));
        await act(() => queryClient.refetchQueries({ queryKey: sessionKey(), exact: true }));
        await settled();

        expect(names(result.current.shared.data)).toEqual(["deck.md"]);
        expect(names(result.current.panel.artifacts)).toEqual(["deck-spec.yaml", "deck.md"]);
    });

    test("the full list adds only working files: a visible file the shared list dropped stays gone", async () => {
        server.files = [deck, notes, spec];
        await importWithToggle(true);
        const { result } = renderPanelAndSharedList();
        await settled();
        expect(names(result.current.panel.artifacts)).toEqual(["deck-spec.yaml", "deck.md", "notes.md"]);

        act(() => queryClient.setQueryData(sessionKey(), [deck]));

        await waitFor(() => expect(names(result.current.panel.artifacts)).toEqual(["deck-spec.yaml", "deck.md"]));
    });

    test("the shared list's entry wins over the full list's copy of the same file", async () => {
        await importWithToggle(true);
        const { result } = renderPanelAndSharedList();
        await settled();

        const streamingSpec: ArtifactInfo = { ...spec, size: 99, creationStatus: "in-progress" };
        act(() => queryClient.setQueryData(sessionKey(), [deck, streamingSpec]));

        await waitFor(() => expect(result.current.panel.artifacts.find(artifact => artifact.filename === "deck-spec.yaml")).toEqual(streamingSpec));
        expect(result.current.panel.artifacts.filter(artifact => artifact.filename === "deck-spec.yaml")).toHaveLength(1);
    });

    test("SSE writes to the shared list send no request", async () => {
        await importWithToggle(true);
        const { result } = renderPanelAndSharedList();
        await settled();
        const requestsBefore = getMock.mock.calls.length;

        act(() => queryClient.setQueryData(sessionKey(), [deck, notes]));
        await waitFor(() => expect(names(result.current.panel.artifacts)).toContain("notes.md"));

        expect(getMock.mock.calls.length).toBe(requestsBefore);
    });

    test("a working file revealed after a refetch opens in the chat's preview", async () => {
        server.files = [deck];
        await importWithToggle(true);
        const { result } = renderHook(
            () => {
                const { data: shared = [] } = artifacts.useSessionArtifacts(SESSION_ID);
                const previewable = artifacts.useSessionArtifactsWithWorking(SESSION_ID, shared);
                return { preview: previewModule.useArtifactPreview({ sessionId: SESSION_ID, artifacts: previewable }) };
            },
            { wrapper }
        );
        await settled();

        server.files = [deck, spec];
        await act(() => queryClient.refetchQueries({ queryKey: sessionKey(), exact: true }));
        await settled();
        act(() => result.current.preview.setPreviewByArtifact(spec));

        await waitFor(() => expect(result.current.preview.previewArtifact?.filename).toBe("deck-spec.yaml"));
    });

    test("the shared list keeps what the gateway keeps: only an exact __working tag is stripped", async () => {
        const shouting: ArtifactInfo = { ...deck, filename: "shouting.md", tags: ["__WORKING"] };
        server.files = [deck, spec, shouting];
        await importWithToggle(true);
        const { result } = renderPanelAndSharedList();
        await settled();

        expect(names(result.current.shared.data)).toEqual(["deck.md", "shouting.md"]);
    });

    test("working files from one session don't carry into the next", async () => {
        await importWithToggle(true);
        const { result, rerender } = renderPanelAndSharedList();
        await waitFor(() => expect(names(result.current.panel.artifacts)).toContain("deck-spec.yaml"));

        server.files = [deck];
        scope = { sessionId: OTHER_SESSION_ID, projectId: null };
        rerender();
        await settled();

        expect(requestedUrls()).toContain(`/api/v1/artifacts/${OTHER_SESSION_ID}?visibility=all`);
        expect(names(result.current.panel.allArtifacts)).toEqual(["deck.md"]);
    });
});
