// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, render, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import type { ChatContextValue } from "@/providers/contexts/ChatContext";

class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
}
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = ResizeObserverStub;

const ACTIVE_KEY = "sam_builder_active_session";
const STORED_SESSION = "builder-session-before-reload";
const emptyPage = { data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } };

const builderSession = () => HttpResponse.json({ id: STORED_SESSION, agentId: "Builder" });

const server = setupServer(
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/sessions/:id/chatTasks", () => HttpResponse.json(emptyPage)),
    http.get("*/api/v1/sessions/:id", ({ params }) => (params.id === STORED_SESSION ? builderSession() : new HttpResponse(null, { status: 404 }))),
    http.get("*/api/v1/artifacts/:id", () => HttpResponse.json(emptyPage))
);

let requests: string[] = [];
server.events.on("request:start", ({ request }) => {
    requests.push(new URL(request.url).pathname);
});

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterAll(() => server.close());

let handleSwitchSession: ReturnType<typeof vi.fn>;
let navigateTo: (path: string) => void;

beforeEach(() => {
    sessionStorage.clear();
    requests = [];
    handleSwitchSession = vi.fn().mockResolvedValue(true);
});

afterEach(() => {
    server.resetHandlers();
    vi.restoreAllMocks();
    sessionStorage.clear();
    window.location.hash = "";
});

function Navigator() {
    navigateTo = useNavigate();
    return null;
}

function gate() {
    let release!: () => void;
    const opened = new Promise<void>(resolve => (release = resolve));
    return { opened, release };
}

const RELOAD_TEST_TIMEOUT_MS = 20_000;
const waitLong = { timeout: 15_000 };
const tick = () => new Promise(resolve => setTimeout(resolve, 150));
const sessionGets = () => requests.filter(p => p === `/api/v1/sessions/${STORED_SESSION}`).length;

async function afterAgentCardsLoad() {
    await waitFor(() => expect(requests).toContain("/api/v1/agentCards"), waitLong);
    await tick();
}

async function loadPageAt(hash: string, chatContext: Partial<ChatContextValue> = {}) {
    window.location.hash = hash;
    vi.resetModules();
    const { BuilderPage } = await import("./BuilderPage");
    const { StoryProvider } = await import("@/testing/providers/StoryProvider");
    const tree = (context: Partial<ChatContextValue>) => (
        <MemoryRouter initialEntries={["/builder"]}>
            <StoryProvider chatContextValues={{ handleSwitchSession, addNotification: vi.fn(), setSelectedAgentName: vi.fn(), handleSubmit: vi.fn(), ...context } as unknown as Partial<ChatContextValue>}>
                <Navigator />
                <Routes>
                    <Route path="/builder" element={<BuilderPage />} />
                    <Route path="/chat" element={<div>chat</div>} />
                </Routes>
            </StoryProvider>
        </MemoryRouter>
    );
    const view = render(tree(chatContext));
    return { rerenderWith: (context: Partial<ChatContextValue>) => view.rerender(tree(context)) };
}

describe("BuilderPage reload restore", { timeout: RELOAD_TEST_TIMEOUT_MS }, () => {
    test("reopens the active build session after a reload on /builder", async () => {
        sessionStorage.setItem(ACTIVE_KEY, STORED_SESSION);
        await loadPageAt("#/builder");

        await waitFor(() => expect(handleSwitchSession).toHaveBeenCalledWith(STORED_SESSION), waitLong);
    });

    test("treats a builder hash with a query as a reload on /builder", async () => {
        sessionStorage.setItem(ACTIVE_KEY, STORED_SESSION);
        await loadPageAt("#/builder?tab=test");

        await waitFor(() => expect(handleSwitchSession).toHaveBeenCalledWith(STORED_SESSION), waitLong);
    });

    test.each(["#/chat", "#/builder/elsewhere"])("starts fresh when the page load started on %s", async hash => {
        sessionStorage.setItem(ACTIVE_KEY, STORED_SESSION);
        await loadPageAt(hash);

        await afterAgentCardsLoad();
        expect(sessionGets()).toBe(0);
        expect(handleSwitchSession).not.toHaveBeenCalled();
    });

    test("an explicit sidebar target wins over the remembered session", async () => {
        sessionStorage.setItem(ACTIVE_KEY, STORED_SESSION);
        sessionStorage.setItem("sam_agent_session_redirect", "clicked-session");
        await loadPageAt("#/builder");

        await waitFor(() => expect(handleSwitchSession).toHaveBeenCalledWith("clicked-session"), waitLong);
        await afterAgentCardsLoad();
        expect(handleSwitchSession).not.toHaveBeenCalledWith(STORED_SESSION);
    });

    test.each(["sam_test_session_pin", "sam_redirect_context"])("a pending %s suppresses the restore", async key => {
        sessionStorage.setItem(ACTIVE_KEY, STORED_SESSION);
        sessionStorage.setItem(key, "pending-target");
        await loadPageAt("#/builder");

        await afterAgentCardsLoad();
        expect(sessionGets()).toBe(0);
        expect(handleSwitchSession).not.toHaveBeenCalledWith(STORED_SESSION);
    });

    test("a leftover redirect session id from another route does not suppress the restore", async () => {
        sessionStorage.setItem(ACTIVE_KEY, STORED_SESSION);
        sessionStorage.setItem("sam_redirect_session_id", "redirect-to-prompts");
        await loadPageAt("#/builder");

        await waitFor(() => expect(handleSwitchSession).toHaveBeenCalledWith(STORED_SESSION), waitLong);
    });

    test.each([
        ["a deleted session", () => new HttpResponse(null, { status: 404 })],
        ["a non-Builder session", () => HttpResponse.json({ id: STORED_SESSION, agentId: "OrchestratorAgent" })],
    ])("forgets %s instead of reopening it", async (_label, respond) => {
        server.use(http.get("*/api/v1/sessions/:id", respond));
        sessionStorage.setItem(ACTIVE_KEY, STORED_SESSION);
        await loadPageAt("#/builder");

        await waitFor(() => expect(sessionStorage.getItem(ACTIVE_KEY)).toBeNull(), waitLong);
        expect(handleSwitchSession).not.toHaveBeenCalled();
    });

    test.each([
        ["a server error", () => new HttpResponse(null, { status: 500 })],
        ["an expired login", () => new HttpResponse(null, { status: 401 })],
        ["a network failure", () => HttpResponse.error()],
    ])("keeps the remembered session when the check hits %s", async (_label, respond) => {
        server.use(http.get("*/api/v1/sessions/:id", respond));
        sessionStorage.setItem(ACTIVE_KEY, STORED_SESSION);
        await loadPageAt("#/builder");

        await waitFor(() => expect(sessionGets()).toBeGreaterThan(0), waitLong);
        await tick();
        expect(sessionStorage.getItem(ACTIVE_KEY)).toBe(STORED_SESSION);
        expect(handleSwitchSession).not.toHaveBeenCalled();
    });

    test("keeps the remembered session when the switch does not land", async () => {
        handleSwitchSession.mockResolvedValue(false);
        sessionStorage.setItem(ACTIVE_KEY, STORED_SESSION);
        await loadPageAt("#/builder");

        await waitFor(() => expect(handleSwitchSession).toHaveBeenCalledWith(STORED_SESSION), waitLong);
        await tick();
        expect(sessionStorage.getItem(ACTIVE_KEY)).toBe(STORED_SESSION);
    });

    test("keeps the remembered id stored while the restore is still pending", async () => {
        const cards = gate();
        server.use(
            http.get("*/api/v1/agentCards", async () => {
                await cards.opened;
                return HttpResponse.json({ data: [] });
            })
        );
        sessionStorage.setItem(ACTIVE_KEY, STORED_SESSION);
        await loadPageAt("#/builder");
        await waitFor(() => expect(requests).toContain("/api/v1/agentCards"), waitLong);

        expect(sessionStorage.getItem(ACTIVE_KEY)).toBe(STORED_SESSION);
        expect(sessionGets()).toBe(0);
        cards.release();
        await waitFor(() => expect(handleSwitchSession).toHaveBeenCalledWith(STORED_SESSION), waitLong);
    });

    test("does not reopen after the user left /builder during the check", async () => {
        const check = gate();
        server.use(
            http.get("*/api/v1/sessions/:id", async () => {
                await check.opened;
                return builderSession();
            })
        );
        sessionStorage.setItem(ACTIVE_KEY, STORED_SESSION);
        await loadPageAt("#/builder");
        await waitFor(() => expect(sessionGets()).toBeGreaterThan(0), waitLong);

        act(() => navigateTo("/chat"));
        check.release();
        await tick();
        expect(handleSwitchSession).not.toHaveBeenCalled();
    });

    test.each([
        ["started a new build", { sessionId: "new-build" }],
        ["is sending a first message", { isResponding: true }],
        ["is opening another session", { isLoadingSession: true }],
    ])("keeps the builder as is when the user %s during the check", async (_label, context) => {
        const check = gate();
        server.use(
            http.get("*/api/v1/sessions/:id", async () => {
                await check.opened;
                return builderSession();
            })
        );
        sessionStorage.setItem(ACTIVE_KEY, STORED_SESSION);
        const { rerenderWith } = await loadPageAt("#/builder");
        await waitFor(() => expect(sessionGets()).toBeGreaterThan(0), waitLong);

        rerenderWith(context as Partial<ChatContextValue>);
        check.release();
        await tick();
        expect(handleSwitchSession).not.toHaveBeenCalledWith(STORED_SESSION);
    });

    test("restores once even when handleSwitchSession changes identity during the check", async () => {
        const check = gate();
        server.use(
            http.get("*/api/v1/sessions/:id", async () => {
                await check.opened;
                return builderSession();
            })
        );
        sessionStorage.setItem(ACTIVE_KEY, STORED_SESSION);
        const { rerenderWith } = await loadPageAt("#/builder");
        await waitFor(() => expect(sessionGets()).toBeGreaterThan(0), waitLong);

        const nextSwitch = vi.fn().mockResolvedValue(true);
        rerenderWith({ handleSwitchSession: nextSwitch } as unknown as Partial<ChatContextValue>);
        await tick();
        check.release();

        await waitFor(() => expect(handleSwitchSession.mock.calls.length + nextSwitch.mock.calls.length).toBe(1), waitLong);
        const getsAfterRestore = sessionGets();
        await tick();
        expect(sessionGets()).toBe(getsAfterRestore);
        expect(handleSwitchSession.mock.calls.length + nextSwitch.mock.calls.length).toBe(1);
    });

    test("remembers the session the builder is showing and forgets it when the builder clears it", async () => {
        const { rerenderWith } = await loadPageAt("#/chat", { sessionId: "current-build" } as Partial<ChatContextValue>);
        await waitFor(() => expect(sessionStorage.getItem(ACTIVE_KEY)).toBe("current-build"), waitLong);

        rerenderWith({ sessionId: "" } as Partial<ChatContextValue>);
        await waitFor(() => expect(sessionStorage.getItem(ACTIVE_KEY)).toBeNull(), waitLong);
    });

    test("restores only once per page load, not on a later in-app visit", async () => {
        sessionStorage.setItem(ACTIVE_KEY, STORED_SESSION);
        await loadPageAt("#/builder");
        await waitFor(() => expect(handleSwitchSession).toHaveBeenCalledWith(STORED_SESSION), waitLong);
        sessionStorage.setItem(ACTIVE_KEY, STORED_SESSION);
        const getsAfterRestore = sessionGets();

        act(() => navigateTo("/chat"));
        act(() => navigateTo("/builder"));
        await afterAgentCardsLoad();

        expect(handleSwitchSession).toHaveBeenCalledTimes(1);
        expect(sessionGets()).toBe(getsAfterRestore);
    });
});
