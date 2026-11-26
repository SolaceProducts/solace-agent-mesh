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

const TARGET_SESSION_ID = "not-yet-created-session";

function paginationMeta(count: number) {
    return { pagination: { pageNumber: 1, pageSize: 100, count, totalPages: 1, nextPage: null } };
}

const server = setupServer(
    http.get("*/api/v1/sessions/:id/chatTasks", () => new HttpResponse(null, { status: 404 })),
    http.get("*/api/v1/sessions/:id", () => new HttpResponse(null, { status: 404 })),
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

function SwitchConsumer() {
    const ctx = React.useContext(ChatContext);
    if (!ctx) return <div>no context</div>;
    return (
        <div>
            <span data-testid="session-id">{ctx.sessionId || "none"}</span>
            <span data-testid="loading">{ctx.isLoadingSession ? "loading" : "idle"}</span>
            <span data-testid="message-count">{ctx.messages.length}</span>
            <button data-testid="switch-session" onClick={() => ctx.handleSwitchSession(TARGET_SESSION_ID)}>
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
                        <SwitchConsumer />
                    </ChatProvider>
                </BuilderContext.Provider>
            </StoryProvider>
        </MemoryRouter>
    );
}

describe("ChatProvider session switch — 404 tolerance", () => {
    test("switching to a not-yet-created session (metadata + chatTasks 404) does NOT surface an error dialog", async () => {
        renderChatProvider();
        fireEvent.click(screen.getByTestId("switch-session"));

        await waitFor(() => expect(screen.getByTestId("session-id")).toHaveTextContent(TARGET_SESSION_ID));
        await waitFor(() => expect(screen.getByTestId("loading")).toHaveTextContent("idle"));

        expect(screen.getByTestId("message-count")).toHaveTextContent("0");
        expect(screen.queryByText("Switching Chats Failed")).not.toBeInTheDocument();
    });

    test("a genuine server error (chatTasks 500) still surfaces the error dialog", async () => {
        server.use(http.get("*/api/v1/sessions/:id/chatTasks", () => new HttpResponse(null, { status: 500 })));

        renderChatProvider();
        fireEvent.click(screen.getByTestId("switch-session"));

        expect(await screen.findByText("Switching Chats Failed")).toBeInTheDocument();
    });
});
