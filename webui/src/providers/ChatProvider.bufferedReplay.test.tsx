// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router-dom";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import { BuilderContext, type BuilderContextValue } from "@/providers/contexts/BuilderContext";
import { ChatContext } from "@/providers/contexts/ChatContext";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { ChatProvider } from "./ChatProvider";

const SESSION_ID = "session-buffered";
const TASK_REPLAY = "task-replay";
const TASK_CLEANUP = "task-cleanup";

function paginationMeta(count: number) {
    return { pagination: { pageNumber: 1, pageSize: 100, count, totalPages: 1, nextPage: null } };
}

function chatTasksResponse() {
    return {
        data: [
            {
                id: TASK_REPLAY,
                messageBubbles: JSON.stringify([{ id: "u1", type: "user", text: "run it", parts: [{ kind: "text", text: "run it" }] }]),
                taskMetadata: JSON.stringify({ agentName: "ReplayAgent" }),
                createdAt: "2025-01-01T00:00:00.000+00:00",
            },
            {
                id: TASK_CLEANUP,
                messageBubbles: JSON.stringify([
                    { id: "u2", type: "user", text: "done one", parts: [{ kind: "text", text: "done one" }] },
                    { id: "a2", type: "agent", text: "all set", parts: [{ kind: "text", text: "all set" }] },
                ]),
                taskMetadata: JSON.stringify({ agentName: "CleanupAgent" }),
                createdAt: "2025-01-01T00:00:01.000+00:00",
            },
        ],
        meta: paginationMeta(2),
    };
}

function unconsumedResponse() {
    return {
        hasEvents: true,
        eventsByTask: {
            [TASK_REPLAY]: { eventsBuffered: true, events: [{ data: {} }] },
            [TASK_CLEANUP]: { eventsBuffered: true, events: [{ data: {} }] },
        },
    };
}

const deletedBuffers: string[] = [];
const savedTasks: string[] = [];

const server = setupServer(
    http.get("*/api/v1/sessions/:id/chatTasks", () => HttpResponse.json(chatTasksResponse())),
    http.get("*/api/v1/sessions/:id/events/unconsumed", () => HttpResponse.json(unconsumedResponse())),
    http.get("*/api/v1/sessions/:id", () => HttpResponse.json({ id: SESSION_ID, name: "Buffered", project: null })),
    http.get("*/api/v1/artifacts/:id", () => HttpResponse.json({ data: [], meta: paginationMeta(0) })),
    http.get("*/api/v1/feedback", () => HttpResponse.json({ data: [], meta: paginationMeta(0) })),
    http.post("*/api/v1/sessions/:id/chatTasks", async ({ request }) => {
        const body = (await request.json()) as { id: string };
        savedTasks.push(body.id);
        return HttpResponse.json({ id: body.id });
    }),
    http.delete("*/api/v1/tasks/:taskId/events/buffered", ({ params }) => {
        deletedBuffers.push(params.taskId as string);
        return new HttpResponse(null, { status: 204 });
    })
);

beforeAll(() => {
    server.listen({ onUnhandledFrame: "bypass" });
    vi.spyOn(console, "debug").mockImplementation(() => {});
});
beforeEach(() => {
    localStorage.clear();
    deletedBuffers.length = 0;
    savedTasks.length = 0;
});
afterEach(() => server.resetHandlers());
afterAll(() => {
    server.close();
    vi.restoreAllMocks();
});

const builderContextValue: BuilderContextValue = {
    builderMode: false,
    setBuilderMode: () => {},
    builderCreationState: { isBuilding: false, components: [], overallStatus: "idle" },
    setBuilderCreationState: () => {},
    selectedBuilderNode: null,
    setSelectedBuilderNode: () => {},
    builderTabContent: null,
};

function Consumer() {
    const ctx = React.useContext(ChatContext);
    if (!ctx) return <div>no context</div>;
    return (
        <div>
            <span data-testid="message-count">{ctx.messages.length}</span>
            <span data-testid="agent-name">{ctx.selectedAgentName || "none"}</span>
            <button data-testid="switch-session" onClick={() => ctx.handleSwitchSession(SESSION_ID)}>
                Switch Session
            </button>
        </div>
    );
}

function renderChatProvider() {
    return render(
        <MemoryRouter initialEntries={["/"]}>
            <StoryProvider userContextValues={{ userHasCapability: () => false }} configContextValues={{ persistenceEnabled: true }}>
                <BuilderContext.Provider value={builderContextValue}>
                    <ChatProvider disableBuilderBridge>
                        <Consumer />
                    </ChatProvider>
                </BuilderContext.Provider>
            </StoryProvider>
        </MemoryRouter>
    );
}

describe("ChatProvider buffered-events replay", () => {
    test("replays a task with no saved agent response and cleans up a task that already has one", async () => {
        renderChatProvider();
        fireEvent.click(screen.getByTestId("switch-session"));

        await waitFor(() => expect(Number(screen.getByTestId("message-count").textContent)).toBeGreaterThanOrEqual(3));

        await waitFor(() => expect(deletedBuffers).toContain(TASK_CLEANUP));
        await waitFor(() => expect(savedTasks).toContain(TASK_REPLAY));

        expect(deletedBuffers).not.toContain(TASK_REPLAY);
    });

    test("shows the saved messages without replaying when the buffered fetch fails", async () => {
        server.use(http.get("*/api/v1/sessions/:id/events/unconsumed", () => new HttpResponse(null, { status: 500 })));

        renderChatProvider();
        fireEvent.click(screen.getByTestId("switch-session"));

        await waitFor(() => expect(Number(screen.getByTestId("message-count").textContent)).toBeGreaterThanOrEqual(3));
        await new Promise(resolve => setTimeout(resolve, 200));
        expect(savedTasks).toEqual([]);
        expect(deletedBuffers).toEqual([]);
    });
});
