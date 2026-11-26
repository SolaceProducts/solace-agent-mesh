// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { render, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import type { ChatContextValue } from "@/providers/contexts/ChatContext";

class ResizeObserverStub {
    observe() {}
    unobserve() {}
    disconnect() {}
}
(globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = ResizeObserverStub;

const BUILD_SESSION = "build-session-opened-from-sidebar";
const TEST_SESSION = "test-session";
const PARENT_BUILD_SESSION = "parent-build-session";
const emptyPage = { data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } };

let requests: string[] = [];
const server = setupServer(
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/sessions/:id/chatTasks", () => HttpResponse.json(emptyPage)),
    http.get("*/api/internal/platform/builder/test-agents/lookup", () => HttpResponse.json({ builderSessionId: PARENT_BUILD_SESSION, componentName: "TideAgent", componentKind: "agent" })),
    http.get("*/api/v1/sessions/:id", ({ params }) => HttpResponse.json({ id: params.id, agentId: params.id === TEST_SESSION ? "agent_test" : "Builder" })),
    http.get("*/api/v1/artifacts/:id", () => HttpResponse.json(emptyPage))
);
server.events.on("request:start", ({ request }) => {
    requests.push(new URL(request.url).pathname);
});

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
afterAll(() => server.close());

let handleSwitchSession: ReturnType<typeof vi.fn>;

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

const BUILDER_TEST_TIMEOUT_MS = 20_000;
const waitLong = { timeout: 15_000 };
const settle = () => new Promise(resolve => setTimeout(resolve, 150));

async function renderBuilderWithRedirector() {
    window.location.hash = "#/builder";
    vi.resetModules();
    const { BuilderPage } = await import("./BuilderPage");
    const { BuilderSessionRedirector } = await import("@/providers/builder/BuilderEffects");
    const { StoryProvider } = await import("@/testing/providers/StoryProvider");
    const chatContext = { handleSwitchSession, addNotification: vi.fn(), setSelectedAgentName: vi.fn(), handleSubmit: vi.fn() } as unknown as Partial<ChatContextValue>;
    render(
        <MemoryRouter initialEntries={["/builder"]}>
            <StoryProvider chatContextValues={chatContext}>
                <BuilderSessionRedirector navigate={vi.fn()} />
                <Routes>
                    <Route path="/builder" element={<BuilderPage />} />
                </Routes>
            </StoryProvider>
        </MemoryRouter>
    );
    await waitFor(() => expect(requests).toContain("/api/v1/agentCards"), waitLong);
    await settle();
}

function clickSidebarSession(detail: { sessionId: string; route: string; agentId: string; isEphemeralTest: boolean }) {
    window.dispatchEvent(new CustomEvent("agent-session-navigate", { detail }));
}

describe("Builder sidebar session click", { timeout: BUILDER_TEST_TIMEOUT_MS }, () => {
    test("switches to a Builder session exactly once", async () => {
        await renderBuilderWithRedirector();

        clickSidebarSession({ sessionId: BUILD_SESSION, route: "/builder", agentId: "Builder", isEphemeralTest: false });

        await waitFor(() => expect(handleSwitchSession).toHaveBeenCalledWith(BUILD_SESSION), waitLong);
        await settle();
        expect(handleSwitchSession).toHaveBeenCalledTimes(1);
    });

    test("switches once for each click on a different Builder session", async () => {
        await renderBuilderWithRedirector();

        clickSidebarSession({ sessionId: BUILD_SESSION, route: "/builder", agentId: "Builder", isEphemeralTest: false });
        clickSidebarSession({ sessionId: "another-build-session", route: "/builder", agentId: "Builder", isEphemeralTest: false });

        await waitFor(() => expect(handleSwitchSession).toHaveBeenCalledTimes(2), waitLong);
        await settle();
        expect(handleSwitchSession.mock.calls.map(call => call[0])).toEqual([BUILD_SESSION, "another-build-session"]);
    });

    test("opens the parent build session of an ephemeral test session, never the test session itself", async () => {
        await renderBuilderWithRedirector();

        clickSidebarSession({ sessionId: TEST_SESSION, route: "/builder", agentId: "agent_test", isEphemeralTest: true });

        await waitFor(() => expect(handleSwitchSession).toHaveBeenCalledWith(PARENT_BUILD_SESSION), waitLong);
        await settle();
        expect(handleSwitchSession).not.toHaveBeenCalledWith(TEST_SESSION);
    });
});
