// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router-dom";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test } from "vitest";

import { BuilderContext, type BuilderContextValue } from "@/providers/contexts/BuilderContext";
import { ChatContext } from "@/providers/contexts/ChatContext";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { ChatProvider } from "./ChatProvider";

const SESSION_ID = "session-1";
const CHAT_TASK_ID = "task-1";

function paginationMeta(count: number) {
    return { pagination: { pageNumber: 1, pageSize: 100, count, totalPages: 1, nextPage: null } };
}

function chatTasksResponse() {
    return {
        data: [
            {
                id: CHAT_TASK_ID,
                messageBubbles: JSON.stringify([
                    { id: "m1", type: "user", text: "hello", parts: [{ kind: "text", text: "hello" }] },
                    { id: "m2", type: "agent", text: "hi there", parts: [{ kind: "text", text: "hi there" }] },
                ]),
                taskMetadata: JSON.stringify({ agentName: "TestAgent" }),
                createdAt: "2025-01-01T00:00:00.000+00:00",
            },
        ],
        meta: paginationMeta(1),
    };
}

const server = setupServer(
    http.get("*/api/v1/sessions/:id/chatTasks", () => HttpResponse.json(chatTasksResponse())),
    http.get("*/api/v1/sessions/:id", () => HttpResponse.json({ id: SESSION_ID, name: "Test", project: null })),
    http.get("*/api/v1/artifacts/:id", () => HttpResponse.json({ data: [], meta: paginationMeta(0) })),
    http.get("*/api/v1/feedback", () => HttpResponse.json({ data: [], meta: paginationMeta(0) }))
);

beforeAll(() => server.listen({ onUnhandledFrame: "bypass" }));
beforeEach(() => localStorage.clear());
afterEach(() => server.resetHandlers());
afterAll(() => server.close());

const builderContextValue: BuilderContextValue = {
    builderMode: false,
    setBuilderMode: () => {},
    builderCreationState: { isBuilding: false, components: [], overallStatus: "idle" },
    setBuilderCreationState: () => {},
    selectedBuilderNode: null,
    setSelectedBuilderNode: () => {},
    builderTabContent: null,
};

function FeedbackConsumer() {
    const ctx = React.useContext(ChatContext);
    const [submitError, setSubmitError] = React.useState("");
    if (!ctx) return <div>no context</div>;
    return (
        <div>
            <span data-testid="feedback-json">{JSON.stringify(ctx.submittedFeedback)}</span>
            <span data-testid="submit-error">{submitError}</span>
            <button data-testid="submit-up" onClick={() => ctx.handleFeedbackSubmit(CHAT_TASK_ID, "up", "").catch((error: { status?: number }) => setSubmitError(String(error.status)))}>
                Submit Up
            </button>
            <span data-testid="message-count">{ctx.messages.length}</span>
            <button data-testid="switch-session" onClick={() => ctx.handleSwitchSession(SESSION_ID)}>
                Switch Session
            </button>
        </div>
    );
}

function renderChatProvider() {
    return render(
        <MemoryRouter initialEntries={["/"]}>
            <StoryProvider userContextValues={{ userHasCapability: () => false }}>
                <BuilderContext.Provider value={builderContextValue}>
                    <ChatProvider disableBuilderBridge>
                        <FeedbackConsumer />
                    </ChatProvider>
                </BuilderContext.Provider>
            </StoryProvider>
        </MemoryRouter>
    );
}

function readFeedback(): Record<string, { type: string; text: string }> {
    return JSON.parse(screen.getByTestId("feedback-json").textContent || "{}");
}

describe("ChatProvider feedback submission body", () => {
    test("submits no content-sharing consent flag, because RBAC alone decides who can read a comment and its conversation", async () => {
        let body: Record<string, unknown> = {};
        server.use(
            http.post("*/api/v1/feedback", async ({ request }) => {
                body = (await request.json()) as Record<string, unknown>;
                return HttpResponse.json({}, { status: 201 });
            })
        );
        renderChatProvider();
        fireEvent.click(screen.getByTestId("switch-session"));
        await waitFor(() => expect(screen.getByTestId("message-count").textContent).not.toBe("0"));

        fireEvent.click(screen.getByTestId("submit-up"));

        await waitFor(() => expect(Object.keys(body)).not.toHaveLength(0));
        expect(body).not.toHaveProperty("shareTaskContent");
        expect(Object.keys(body).sort()).toEqual(["feedbackText", "feedbackType", "sessionId", "taskId"]);
    });
});

describe("ChatProvider feedback rehydration", () => {
    test("rehydrates submittedFeedback from the persisted feedback store on session load", async () => {
        server.use(http.get("*/api/v1/feedback", () => HttpResponse.json({ data: [{ taskId: CHAT_TASK_ID, rating: "down", comment: "bad" }], meta: paginationMeta(1) })));

        renderChatProvider();
        fireEvent.click(screen.getByTestId("switch-session"));

        await waitFor(() => {
            expect(readFeedback()).toEqual({ [CHAT_TASK_ID]: { type: "down", text: "bad" } });
        });
    });

    test("leaves submittedFeedback empty when there is no persisted feedback", async () => {
        server.use(http.get("*/api/v1/feedback", () => HttpResponse.json({ data: [], meta: paginationMeta(0) })));

        renderChatProvider();
        fireEvent.click(screen.getByTestId("switch-session"));

        await waitFor(() => {
            expect(screen.getByTestId("message-count")).toHaveTextContent("2");
        });
        expect(readFeedback()).toEqual({});
    });

    test("degrades gracefully when the feedback fetch fails (chat still loads, not blanked)", async () => {
        server.use(http.get("*/api/v1/feedback", () => new HttpResponse(null, { status: 500 })));

        renderChatProvider();
        fireEvent.click(screen.getByTestId("switch-session"));

        await waitFor(() => {
            expect(screen.getByTestId("message-count")).toHaveTextContent("2");
        });
        expect(readFeedback()).toEqual({});
    });

    test("newest-first row wins when duplicate rows exist for a task", async () => {
        server.use(
            http.get("*/api/v1/feedback", () =>
                HttpResponse.json({
                    data: [
                        { taskId: "t1", rating: "up", comment: "new" },
                        { taskId: "t1", rating: "down", comment: "old" },
                    ],
                    meta: paginationMeta(2),
                })
            )
        );

        renderChatProvider();
        fireEvent.click(screen.getByTestId("switch-session"));

        await waitFor(() => {
            expect(readFeedback().t1).toEqual({ type: "up", text: "new" });
        });
    });

    test("loads the recorded rating when a submit conflicts with it", async () => {
        server.use(
            http.post("*/api/v1/feedback", () => HttpResponse.json({ message: "already submitted", errorId: "e1" }, { status: 409 })),
            http.get("*/api/v1/feedback", ({ request }) => {
                const taskId = new URL(request.url).searchParams.get("taskId");
                const rows = taskId === CHAT_TASK_ID ? [{ taskId: CHAT_TASK_ID, rating: "down", comment: "first" }] : [];
                return HttpResponse.json({ data: rows, meta: paginationMeta(rows.length) });
            })
        );

        renderChatProvider();
        fireEvent.click(screen.getByTestId("switch-session"));
        await waitFor(() => expect(screen.getByTestId("message-count")).toHaveTextContent("2"));

        fireEvent.click(screen.getByTestId("submit-up"));

        await waitFor(() => expect(screen.getByTestId("submit-error")).toHaveTextContent("409"));
        expect(readFeedback()[CHAT_TASK_ID]).toEqual({ type: "down", text: "first" });
    });

    test("drops rows whose rating is outside the up/down set", async () => {
        server.use(http.get("*/api/v1/feedback", () => HttpResponse.json({ data: [{ taskId: "t-neutral", rating: "neutral", comment: "meh" }], meta: paginationMeta(1) })));

        renderChatProvider();
        fireEvent.click(screen.getByTestId("switch-session"));

        await waitFor(() => {
            expect(screen.getByTestId("message-count")).toHaveTextContent("2");
        });
        expect(readFeedback()["t-neutral"]).toBeUndefined();
        expect(readFeedback()).toEqual({});
    });
});
