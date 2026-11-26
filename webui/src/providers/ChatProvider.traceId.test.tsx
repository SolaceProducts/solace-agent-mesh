// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { MemoryRouter } from "react-router-dom";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import { BuilderContext, type BuilderContextValue } from "@/providers/contexts/BuilderContext";
import { ChatContext } from "@/providers/contexts/ChatContext";

import { productAnalyticsClient } from "@/common/product-analytics/client";

import { StoryProvider } from "@/testing/providers/StoryProvider";

import { takeSubmittedTrace } from "./chat/submittedTrace";
import { ChatProvider } from "./ChatProvider";

const AGENT_NAME = "TestAgent";
const TASK_ID = "0199b2c1-0000-7000-8000-000000000001";
const SESSION_ID = "0199b2c1-0000-7000-8000-000000000002";
const TRACE_ID = "0199b2c1-0000-7000-8000-000000000003";

const server = setupServer(
    http.post("*/api/v1/message:stream", () => HttpResponse.json({ jsonrpc: "2.0", id: "req-1", result: { id: TASK_ID, contextId: SESSION_ID, kind: "task", traceId: TRACE_ID } })),
    http.post("*/api/v1/sessions/:id/chatTasks", () => HttpResponse.json({ id: TASK_ID })),
    http.patch("*/api/v1/sessions/:id", () => HttpResponse.json({ id: SESSION_ID }))
);

let openedEventSource: MockEventSource | null = null;

function rememberEventSource(eventSource: MockEventSource) {
    openedEventSource = eventSource;
}

class MockEventSource {
    static readonly CONNECTING = 0;
    static readonly OPEN = 1;
    static readonly CLOSED = 2;

    url: string;
    readyState = MockEventSource.OPEN;
    onopen: ((event: Event) => void) | null = null;
    onmessage: ((event: MessageEvent) => void) | null = null;
    onerror: ((event: Event) => void) | null = null;

    constructor(url: string) {
        this.url = url;
        rememberEventSource(this);
    }

    listeners = new Map<string, ((event: MessageEvent) => void)[]>();

    addEventListener(type: string, handler: (event: MessageEvent) => void) {
        this.listeners.set(type, [...(this.listeners.get(type) ?? []), handler]);
    }
    removeEventListener() {}
    close() {}

    deliver(type: string, payload: unknown) {
        const event = new MessageEvent(type, { data: JSON.stringify(payload) });
        for (const handler of this.listeners.get(type) ?? []) handler(event);
    }

    dropForGood() {
        this.readyState = MockEventSource.CLOSED;
        this.onerror?.(new Event("error"));
    }
}

const originalEventSource = global.EventSource;
let trackSpy: ReturnType<typeof vi.spyOn>;

function errorDisplayedProperties(): Record<string, unknown> | null {
    const calls = trackSpy.mock.calls as [string, Record<string, unknown>][];
    return calls.find(([name]) => name === "error:displayed")?.[1] ?? null;
}

beforeAll(() => {
    server.listen({ onUnhandledFrame: "bypass" });
    vi.spyOn(console, "log").mockImplementation(() => {});
    vi.spyOn(console, "debug").mockImplementation(() => {});
});
beforeEach(() => {
    localStorage.clear();
    openedEventSource = null;
    global.EventSource = MockEventSource as unknown as typeof EventSource;
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
});
afterEach(() => {
    global.EventSource = originalEventSource;
    server.resetHandlers();
    trackSpy.mockRestore();
});
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
    const traceBearing = Object.entries(ctx as unknown as Record<string, unknown>)
        .filter(([key, value]) => /trace/i.test(key) || value === TRACE_ID)
        .map(([key]) => key);
    return (
        <div>
            <span data-testid="task-id">{ctx.currentTaskId ?? "none"}</span>
            <span data-testid="trace-bearing">{traceBearing.join(",") || "none"}</span>
            <button data-testid="pick-agent" onClick={() => ctx.setSelectedAgentName(AGENT_NAME)}>
                Pick Agent
            </button>
            <button data-testid="submit" onClick={() => void ctx.handleSubmit({ preventDefault: () => {} } as unknown as React.FormEvent, null, "hello")}>
                Submit
            </button>
            <button data-testid="create-agent" onClick={() => ctx.handleCreateAgent({ name: "a" } as never)}>
                Create
            </button>
            <button data-testid="regenerate-agent" onClick={() => ctx.handleCreateAgent({ name: "a" } as never, "prompt", true)}>
                Regenerate
            </button>
            <button data-testid="new-session" onClick={() => void ctx.handleNewSession()}>
                New Session
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

describe("ChatProvider task traceId", () => {
    test("keeps the gateway's trace id off the chat context", async () => {
        renderChatProvider();
        await act(async () => {
            fireEvent.click(screen.getByTestId("pick-agent"));
        });
        await act(async () => {
            fireEvent.click(screen.getByTestId("submit"));
        });

        await waitFor(() => expect(screen.getByTestId("task-id").textContent).toBe(TASK_ID));
        expect(screen.getByTestId("trace-bearing").textContent).toBe("none");
    });
});

async function click(testid: string) {
    await act(async () => {
        fireEvent.click(screen.getByTestId(testid));
    });
}

describe("the chat failure the user sees names its run", () => {
    test("a lost connection reports the trace of the task it interrupted", async () => {
        renderChatProvider();
        await click("pick-agent");
        await click("submit");
        await waitFor(() => expect(openedEventSource).not.toBeNull());

        await act(async () => {
            openedEventSource?.dropForGood();
        });

        await waitFor(() => expect(errorDisplayedProperties()).not.toBeNull());
        expect(errorDisplayedProperties()).toMatchObject({ traceId: TRACE_ID });
    });

    test("a submit the gateway rejected reports no trace at all", async () => {
        renderChatProvider();
        await click("pick-agent");
        await click("submit");
        await waitFor(() => expect(screen.getByTestId("task-id").textContent).toBe(TASK_ID));

        await click("new-session");
        server.use(http.post("*/api/v1/message:stream", () => HttpResponse.json({ message: "Too Many Requests" }, { status: 429 })));
        await click("pick-agent");
        await click("submit");

        await waitFor(() => expect(errorDisplayedProperties()).not.toBeNull());
        expect(errorDisplayedProperties()).not.toHaveProperty("traceId");
    });
});

describe("a chat that fails", () => {
    function trackedNames(): string[] {
        return (trackSpy.mock.calls as [string, unknown][]).map(([name]) => name);
    }

    async function failTheTask() {
        renderChatProvider();
        await click("pick-agent");
        await click("submit");
        await waitFor(() => expect(openedEventSource).not.toBeNull());
        await act(async () => {
            openedEventSource?.deliver("final_response", {
                jsonrpc: "2.0",
                result: {
                    kind: "task",
                    id: TASK_ID,
                    status: { state: "failed", message: { role: "agent", parts: [{ kind: "text", text: "429 rate limit exceeded" }] } },
                },
            });
        });
    }

    test("reports the error the user saw, bucketed and located", async () => {
        await failTheTask();

        await waitFor(() => expect(errorDisplayedProperties()).not.toBeNull());
        expect(errorDisplayedProperties()).toMatchObject({ errorCategory: "rate_limit", traceId: TRACE_ID });
    });

    test("does not count a failure as the first chat response", async () => {
        await failTheTask();

        await waitFor(() => expect(errorDisplayedProperties()).not.toBeNull());
        expect(trackedNames()).not.toContain("chat:first_response");
    });
});

describe("takeSubmittedTrace", () => {
    test("reports nothing when no task supplied a trace", () => {
        expect(takeSubmittedTrace({ current: null })).toEqual({});
    });

    test("hands the trace over once and then forgets it", () => {
        const pending = { current: { taskId: TASK_ID, traceId: TRACE_ID } };

        expect(takeSubmittedTrace(pending)).toEqual({ traceId: TRACE_ID });
        expect(pending.current).toBeNull();
        expect(takeSubmittedTrace(pending)).toEqual({});
    });
});

describe("agent:creation_started", () => {
    function reportedNames(): string[] {
        return (trackSpy.mock.calls as [string, unknown][]).map(([name]) => name);
    }

    test("counts a fresh create", async () => {
        renderChatProvider();
        await click("create-agent");

        expect(reportedNames()).toContain("agent:creation_started");
    });

    test("does not count a regenerate", async () => {
        renderChatProvider();
        await click("regenerate-agent");

        expect(reportedNames()).not.toContain("agent:creation_started");
    });
});
