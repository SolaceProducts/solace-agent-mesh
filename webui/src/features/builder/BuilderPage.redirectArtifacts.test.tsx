// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, waitFor } from "@testing-library/react";
import { delay, http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router-dom";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";

import type { ChatContextValue } from "@/providers/contexts/ChatContext";
import { queryClient } from "@/providers/query/QueryClient";

import { SESSION_STORAGE_KEYS } from "@/utils/sessionStorageKeys";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { BuilderPage } from "./BuilderPage";

class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
}
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = ResizeObserverStub;

const DEST_SESSION_ID = "dest-session-1";
const SOURCE_SESSION_ID = "src-session-1";

const server = setupServer(
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/sessions/:id/chatTasks", () => HttpResponse.json({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } })),
    http.get("*/api/v1/sessions/:id", () => new HttpResponse(null, { status: 404 })),
    http.get("*/api/v1/artifacts/:id", () => HttpResponse.json({ data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } }))
);

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterAll(() => server.close());

let postSpy: ReturnType<typeof vi.spyOn>;
let handleSwitchSession: ReturnType<typeof vi.fn>;
let addNotification: ReturnType<typeof vi.fn>;
let handleSubmit: ReturnType<typeof vi.fn>;
let setSelectedAgentName: ReturnType<typeof vi.fn>;

beforeEach(() => {
    sessionStorage.clear();
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    postSpy = vi.spyOn(api.webui, "post" as any).mockResolvedValue({} as any);
    handleSwitchSession = vi.fn().mockResolvedValue(true);
    addNotification = vi.fn();
    handleSubmit = vi.fn().mockResolvedValue(undefined);
    setSelectedAgentName = vi.fn();
});

afterEach(() => {
    server.resetHandlers();
    vi.restoreAllMocks();
    sessionStorage.clear();
});

function seedRedirect(pendingArtifacts: string | null) {
    sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT, "Edit workflow X");
    sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_SESSION_ID, DEST_SESSION_ID);
    if (pendingArtifacts !== null) {
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_ARTIFACTS, pendingArtifacts);
    }
}

function renderBuilder() {
    render(
        <MemoryRouter initialEntries={["/builder"]}>
            <StoryProvider chatContextValues={{ handleSwitchSession, addNotification, setSelectedAgentName, handleSubmit } as unknown as Partial<ChatContextValue>}>
                <BuilderPage />
            </StoryProvider>
        </MemoryRouter>
    );
}

const calls = () => postSpy.mock.calls as unknown[][];
const copyCalls = () => calls().filter(c => String(c[0]).endsWith("/artifacts/copy"));
const createCalls = () => calls().filter(c => c[0] === "/api/v1/sessions");

describe("BuilderPage redirect-artifact carry", () => {
    test("lazily creates the destination session, then copies the pending artifacts into it", async () => {
        seedRedirect(JSON.stringify({ destSessionId: DEST_SESSION_ID, sourceSessionId: SOURCE_SESSION_ID, filenames: ["a.txt", "b.txt"] }));
        renderBuilder();

        await waitFor(() => expect(copyCalls()).toHaveLength(1));

        expect(createCalls()[0]).toEqual(["/api/v1/sessions", { id: DEST_SESSION_ID }]);
        expect(copyCalls()[0]).toEqual([`/api/v1/sessions/${DEST_SESSION_ID}/artifacts/copy`, { sourceSessionId: SOURCE_SESSION_ID, filenames: ["a.txt", "b.txt"] }]);

        const createIdx = calls().findIndex(c => c[0] === "/api/v1/sessions");
        const copyIdx = calls().findIndex(c => String(c[0]).endsWith("/artifacts/copy"));
        expect(createIdx).toBeLessThan(copyIdx);

        await waitFor(() => expect(handleSwitchSession).toHaveBeenCalledWith(DEST_SESSION_ID));
        expect(addNotification).not.toHaveBeenCalled();
    });

    test("does NOT copy when the blob targets a different session (stale-blob self-invalidation)", async () => {
        seedRedirect(JSON.stringify({ destSessionId: "some-other-session", sourceSessionId: SOURCE_SESSION_ID, filenames: ["a.txt"] }));
        renderBuilder();

        await waitFor(() => expect(handleSwitchSession).toHaveBeenCalledWith(DEST_SESSION_ID));
        expect(copyCalls()).toHaveLength(0);
        expect(createCalls()).toHaveLength(0);
    });

    test("warns (does not silently drop) when the blob is missing sourceSessionId or filenames", async () => {
        seedRedirect(JSON.stringify({ destSessionId: DEST_SESSION_ID, filenames: ["a.txt"] }));
        renderBuilder();

        await waitFor(() => expect(addNotification).toHaveBeenCalled());
        expect(addNotification.mock.calls[0][1]).toBe("warning");
        expect(copyCalls()).toHaveLength(0);
    });

    test("targets the pending prefill at the destination session for ChatInputArea to consume", async () => {
        seedRedirect(null);
        renderBuilder();

        await waitFor(() => expect(handleSwitchSession).toHaveBeenCalledWith(DEST_SESSION_ID));
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT)).toBe("Edit workflow X");
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT_SESSION)).toBe(DEST_SESSION_ID);
    });

    test("drops the pending prefill and warns when the session switch throws outright", async () => {
        handleSwitchSession.mockRejectedValue(new Error("switch boom"));
        seedRedirect(null);
        renderBuilder();

        await waitFor(() => expect(addNotification).toHaveBeenCalledWith("Failed to load the builder session. Please try again.", "warning"));
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT)).toBeNull();
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT_SESSION)).toBeNull();
    });

    test("drops the pending prefill when the session switch does not land (would otherwise leak for the tab's lifetime)", async () => {
        handleSwitchSession.mockResolvedValue(false);
        seedRedirect(null);
        renderBuilder();

        await waitFor(() => expect(handleSwitchSession).toHaveBeenCalledWith(DEST_SESSION_ID));
        await waitFor(() => {
            expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT)).toBeNull();
            expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT_SESSION)).toBeNull();
        });
    });

    test("auto-submits to the Builder after copying artifacts and switching sessions", async () => {
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT, "Edit workflow X");
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_SESSION_ID, DEST_SESSION_ID);
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_AUTO_SUBMIT, "true");
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_DISPLAY_HTML, "<span>✨ Editing</span>");
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_ARTIFACTS, JSON.stringify({ destSessionId: DEST_SESSION_ID, sourceSessionId: SOURCE_SESSION_ID, filenames: ["a.txt"] }));
        renderBuilder();

        await waitFor(() => expect(copyCalls()).toHaveLength(1));
        await waitFor(() => expect(handleSwitchSession).toHaveBeenCalledWith(DEST_SESSION_ID));
        await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
        expect(setSelectedAgentName).toHaveBeenCalledWith("Builder");
        expect(addNotification).not.toHaveBeenCalled();
    });

    test("forwards the redirect chip as message metadata so the bubble renders as a tile", async () => {
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT, "Load the existing deployed workflow ...");
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_SESSION_ID, DEST_SESSION_ID);
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_AUTO_SUBMIT, "true");
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_CHIP, JSON.stringify({ label: "My Workflow", kind: "workflow" }));
        renderBuilder();

        await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
        const extraMetadata = handleSubmit.mock.calls[0][8];
        expect(extraMetadata).toMatchObject({ agent_name: "Builder", chip: { label: "My Workflow", kind: "workflow" } });
        expect(handleSubmit.mock.calls[0][2]).toContain("Load the existing deployed workflow");
        expect(sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CHIP)).toBeNull();
    });

    test("ignores a malformed redirect chip without blocking auto-submit", async () => {
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT, "Edit workflow X");
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_SESSION_ID, DEST_SESSION_ID);
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_AUTO_SUBMIT, "true");
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_CHIP, "{ not valid json");
        renderBuilder();

        await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
        const extraMetadata = handleSubmit.mock.calls[0][8];
        expect(extraMetadata).toEqual({ agent_name: "Builder" });
    });

    test("drops a well-formed chip that lacks a string label, but still auto-submits", async () => {
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT, "Edit workflow X");
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_SESSION_ID, DEST_SESSION_ID);
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_AUTO_SUBMIT, "true");
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_CHIP, JSON.stringify({ emoji: "🔀" }));
        renderBuilder();

        await waitFor(() => expect(handleSubmit).toHaveBeenCalled());
        const extraMetadata = handleSubmit.mock.calls[0][8];
        expect(extraMetadata).toEqual({ agent_name: "Builder" });
    });

    test("surfaces a warning when auto-submit fails", async () => {
        handleSubmit.mockRejectedValueOnce(new Error("submit boom"));
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT, "Edit workflow X");
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_SESSION_ID, DEST_SESSION_ID);
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_AUTO_SUBMIT, "true");
        renderBuilder();

        await waitFor(() => expect(addNotification).toHaveBeenCalled());
        expect(addNotification.mock.calls[0][1]).toBe("warning");
    });

    test("warns (does not silently drop) on a malformed pending-artifacts blob", async () => {
        seedRedirect("{ not valid json");
        renderBuilder();

        await waitFor(() => expect(addNotification).toHaveBeenCalled());
        expect(addNotification.mock.calls[0][1]).toBe("warning");
        expect(copyCalls()).toHaveLength(0);
    });

    test("still copies when the pending-artifacts key is cleared after mount but before the gated effect runs", async () => {
        queryClient.clear();
        server.use(
            http.get("*/api/v1/agentCards", async () => {
                await delay(30);
                return HttpResponse.json({ data: [] });
            })
        );
        seedRedirect(JSON.stringify({ destSessionId: DEST_SESSION_ID, sourceSessionId: SOURCE_SESSION_ID, filenames: ["a.txt"] }));
        renderBuilder();

        sessionStorage.removeItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_ARTIFACTS);

        await waitFor(() => expect(copyCalls()).toHaveLength(1));
        expect(copyCalls()[0]).toEqual([`/api/v1/sessions/${DEST_SESSION_ID}/artifacts/copy`, { sourceSessionId: SOURCE_SESSION_ID, filenames: ["a.txt"] }]);
        expect(addNotification).not.toHaveBeenCalled();
    });
});
