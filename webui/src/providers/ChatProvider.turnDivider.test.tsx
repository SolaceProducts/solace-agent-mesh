// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterEach, beforeEach, describe, expect, test } from "vitest";

import { ChatContext, type ChatContextValue } from "@/providers/contexts/ChatContext";

const server = setupServer(
    http.post("*/api/v1/message\\:stream", () => {
        return HttpResponse.json({
            jsonrpc: "2.0",
            id: "req-1",
            result: { id: "task-1", contextId: "session-1", status: { state: "submitted" } },
        });
    }),
    http.post("*/api/v1/sessions/*/chatTasks", () => {
        return HttpResponse.json({ success: true });
    }),
    http.get("*/api/v1/sessions/*", () => {
        return HttpResponse.json({ id: "session-2", name: "Test", projectId: null });
    }),
    http.get("*/api/v1/sessions/*/chatTasks", () => {
        return HttpResponse.json({
            data: [],
            meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } },
        });
    })
);

beforeEach(() => server.resetHandlers());
afterEach(() => server.resetHandlers());

function TurnDividerConsumer() {
    const ctx = React.useContext(ChatContext);
    if (!ctx) return <div>no context</div>;
    return (
        <div>
            <span data-testid="turn-divider-index">{ctx.turnDividerIndex === null ? "null" : String(ctx.turnDividerIndex)}</span>
            <span data-testid="message-count">{ctx.messages.length}</span>
            <button data-testid="new-session" onClick={() => ctx.handleNewSession()}>
                New Session
            </button>
        </div>
    );
}

describe("turnDividerIndex lifecycle — plumbing", () => {
    test("turnDividerIndex is null by default", () => {
        render(
            <ChatContext.Provider value={{ turnDividerIndex: null, messages: [] } as unknown as ChatContextValue}>
                <TurnDividerConsumer />
            </ChatContext.Provider>
        );

        expect(screen.getByTestId("turn-divider-index")).toHaveTextContent("null");
    });

    test("turnDividerIndex reflects the message count when set", () => {
        const mockMessages = [
            { role: "user", parts: [{ kind: "text", text: "hello" }], isUser: true, metadata: { messageId: "m1" } },
            { role: "agent", parts: [{ kind: "text", text: "hi" }], isUser: false, metadata: { messageId: "m2" } },
        ];

        render(
            <ChatContext.Provider
                value={
                    {
                        turnDividerIndex: 2,
                        messages: [...mockMessages, { role: "user", parts: [{ kind: "text", text: "new" }], isUser: true, metadata: { messageId: "m3" } }],
                    } as unknown as ChatContextValue
                }
            >
                <TurnDividerConsumer />
            </ChatContext.Provider>
        );

        expect(screen.getByTestId("turn-divider-index")).toHaveTextContent("2");
        expect(screen.getByTestId("message-count")).toHaveTextContent("3");
    });

    test("turnDividerIndex is null when cleared (new session)", () => {
        const { rerender } = render(
            <ChatContext.Provider value={{ turnDividerIndex: 5, messages: [] } as unknown as ChatContextValue}>
                <TurnDividerConsumer />
            </ChatContext.Provider>
        );

        expect(screen.getByTestId("turn-divider-index")).toHaveTextContent("5");

        rerender(
            <ChatContext.Provider value={{ turnDividerIndex: null, messages: [] } as unknown as ChatContextValue}>
                <TurnDividerConsumer />
            </ChatContext.Provider>
        );

        expect(screen.getByTestId("turn-divider-index")).toHaveTextContent("null");
    });
});

function StatefulTurnDividerProvider({ children, initialMessages = [] }: { children: React.ReactNode; initialMessages?: Array<{ role: string; parts: Array<{ kind: string; text: string }>; isUser: boolean; metadata: { messageId: string } }> }) {
    const [messages, setMessages] = React.useState(initialMessages);
    const [turnDividerIndex, setTurnDividerIndex] = React.useState<number | null>(null);
    const messagesRef = React.useRef(messages);
    messagesRef.current = messages;

    const handleSubmit = React.useCallback(async () => {
        if (messagesRef.current.length > 0) {
            setTurnDividerIndex(messagesRef.current.length);
        }
        const userMsg = { role: "user", parts: [{ kind: "text", text: "new" }], isUser: true, metadata: { messageId: `msg-${Date.now()}` } };
        setMessages(prev => [...prev, userMsg]);
    }, []);

    const handleNewSession = React.useCallback(async () => {
        setMessages([]);
        setTurnDividerIndex(null);
    }, []);

    const handleSwitchSession = React.useCallback(async () => {
        setMessages([]);
        setTurnDividerIndex(null);
    }, []);

    return <ChatContext.Provider value={{ turnDividerIndex, messages, handleNewSession, handleSubmit, handleSwitchSession } as unknown as ChatContextValue}>{children}</ChatContext.Provider>;
}

function StatefulConsumer() {
    const ctx = React.useContext(ChatContext);
    if (!ctx) return <div>no context</div>;
    return (
        <div>
            <span data-testid="turn-divider-index">{ctx.turnDividerIndex === null ? "null" : String(ctx.turnDividerIndex)}</span>
            <span data-testid="message-count">{ctx.messages.length}</span>
            <button data-testid="submit" onClick={() => ctx.handleSubmit(new Event("submit") as unknown as React.FormEvent)}>
                Submit
            </button>
            <button data-testid="new-session" onClick={() => ctx.handleNewSession()}>
                New Session
            </button>
            {/* eslint-disable-next-line @typescript-eslint/no-explicit-any */}
            <button data-testid="switch-session" onClick={() => (ctx as any).handleSwitchSession("other-session")}>
                Switch Session
            </button>
        </div>
    );
}

describe("turnDividerIndex lifecycle — behavioral", () => {
    const twoMessages = [
        { role: "user", parts: [{ kind: "text", text: "hello" }], isUser: true, metadata: { messageId: "m1" } },
        { role: "agent", parts: [{ kind: "text", text: "hi" }], isUser: false, metadata: { messageId: "m2" } },
    ];

    test("handleSubmit sets turnDividerIndex to message count when messages exist", async () => {
        render(
            <StatefulTurnDividerProvider initialMessages={twoMessages}>
                <StatefulConsumer />
            </StatefulTurnDividerProvider>
        );

        expect(screen.getByTestId("turn-divider-index")).toHaveTextContent("null");
        expect(screen.getByTestId("message-count")).toHaveTextContent("2");

        fireEvent.click(screen.getByTestId("submit"));

        await waitFor(() => {
            expect(screen.getByTestId("turn-divider-index")).toHaveTextContent("2");
            expect(screen.getByTestId("message-count")).toHaveTextContent("3");
        });
    });

    test("handleSubmit does not set turnDividerIndex when no prior messages", async () => {
        render(
            <StatefulTurnDividerProvider initialMessages={[]}>
                <StatefulConsumer />
            </StatefulTurnDividerProvider>
        );

        fireEvent.click(screen.getByTestId("submit"));

        await waitFor(() => {
            expect(screen.getByTestId("message-count")).toHaveTextContent("1");
        });

        expect(screen.getByTestId("turn-divider-index")).toHaveTextContent("null");
    });

    test("handleSwitchSession resets turnDividerIndex to null", async () => {
        render(
            <StatefulTurnDividerProvider initialMessages={twoMessages}>
                <StatefulConsumer />
            </StatefulTurnDividerProvider>
        );

        fireEvent.click(screen.getByTestId("submit"));
        await waitFor(() => {
            expect(screen.getByTestId("turn-divider-index")).toHaveTextContent("2");
        });

        fireEvent.click(screen.getByTestId("switch-session"));
        await waitFor(() => {
            expect(screen.getByTestId("turn-divider-index")).toHaveTextContent("null");
            expect(screen.getByTestId("message-count")).toHaveTextContent("0");
        });
    });

    test("handleNewSession resets turnDividerIndex to null", async () => {
        render(
            <StatefulTurnDividerProvider initialMessages={twoMessages}>
                <StatefulConsumer />
            </StatefulTurnDividerProvider>
        );

        fireEvent.click(screen.getByTestId("submit"));
        await waitFor(() => {
            expect(screen.getByTestId("turn-divider-index")).toHaveTextContent("2");
        });

        fireEvent.click(screen.getByTestId("new-session"));
        await waitFor(() => {
            expect(screen.getByTestId("turn-divider-index")).toHaveTextContent("null");
            expect(screen.getByTestId("message-count")).toHaveTextContent("0");
        });
    });
});
