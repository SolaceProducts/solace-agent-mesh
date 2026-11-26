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

import { useFreeModelStatus } from "@/api/activation";

import { BuilderContext, type BuilderContextValue } from "@/providers/contexts/BuilderContext";
import { ChatContext } from "@/providers/contexts/ChatContext";

import { getPaginatedMeta } from "@/testing/helpers";
import { StoryProvider } from "@/testing/providers/StoryProvider";

import { ChatProvider } from "./ChatProvider";

const AGENT_NAME = "TestAgent";
const SESSION_ID = "0199b2c1-0000-7000-8000-000000000003";
const ALLOWANCE_USED_UP = "The free model's allowance is used up. Add a model to replace it.";
const REPLAYED_SESSION_ID = "0199b2c1-0000-7000-8000-0000000000aa";
const REPLAYED_TASK_ID = "0199b2c1-0000-7000-8000-0000000000ab";
const USED_UP_MARK_KEY = "sam-free-credits-used-up-at";

let taskCounter = 0;
let lastTaskId = "";
function nextTaskId(): string {
    taskCounter += 1;
    lastTaskId = `0199b2c1-0000-7000-8000-${taskCounter.toString().padStart(12, "0")}`;
    return lastTaskId;
}

const server = setupServer(
    http.post("*/api/v1/message\\:stream", () => HttpResponse.json({ jsonrpc: "2.0", id: "req-1", result: { id: nextTaskId(), contextId: SESSION_ID, kind: "task" } })),
    http.post("*/api/v1/sessions/:id/chatTasks", () => HttpResponse.json({ id: lastTaskId })),
    http.patch("*/api/v1/sessions/:id", () => HttpResponse.json({ id: SESSION_ID })),
    http.get("*/api/v1/freeModel", () => HttpResponse.json({ status: "available", remainingPercent: 13, models: [] })),
    http.get("*/api/v2/platform/agents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/connectors", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.get("*/api/v1/sessions/:id/chatTasks", () =>
        HttpResponse.json({
            data: [{ id: REPLAYED_TASK_ID, messageBubbles: JSON.stringify([{ id: "u1", type: "user", text: "hello", parts: [{ kind: "text", text: "hello" }] }]), createdAt: "2026-11-24T00:00:00.000+00:00" }],
            meta: getPaginatedMeta(1),
        })
    ),
    http.get("*/api/v1/sessions/:id/events/unconsumed", () => HttpResponse.json({ hasEvents: true, eventsByTask: { [REPLAYED_TASK_ID]: { eventsBuffered: true, events: [{ data: { data: JSON.stringify(replayedRefusal) } }] } } })),
    http.get("*/api/v1/sessions/:id", () => HttpResponse.json({ id: REPLAYED_SESSION_ID, name: "Refused earlier", project: null })),
    http.get("*/api/v1/artifacts/:id", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v1/feedback", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.delete("*/api/v1/tasks/:taskId/events/buffered", () => new HttpResponse(null, { status: 204 }))
);

let replayedRefusal: object = {};

function rpcRefusalOf(taskId: string) {
    return { jsonrpc: "2.0", id: taskId, error: { code: -32603, message: ALLOWANCE_USED_UP, data: { llm_error_category: "allowance_exhausted" } } };
}

function refusalOf(taskId: string, contextId: string) {
    return {
        jsonrpc: "2.0",
        id: "evt-1",
        result: { kind: "task", id: taskId, contextId, status: { state: "failed", message: { role: "agent", parts: [{ kind: "text", text: ALLOWANCE_USED_UP }] } } },
    };
}

const streams: MockEventSource[] = [];

class MockEventSource {
    url: string;
    onopen: ((event: Event) => void) | null = null;
    onerror: ((event: Event) => void) | null = null;
    private readonly listeners = new Map<string, (event: MessageEvent) => void>();
    private readonly taskId = lastTaskId;

    constructor(url: string) {
        this.url = url;
        streams.push(this);
    }

    addEventListener(type: string, handler: (event: MessageEvent) => void) {
        this.listeners.set(type, handler);
    }
    removeEventListener(type: string) {
        this.listeners.delete(type);
    }
    close() {}

    emitFailure(text: string) {
        const payload = refusalOf(this.taskId, SESSION_ID);
        payload.result.status.message.parts = [{ kind: "text", text }];
        this.listeners.get("final_response")?.({ data: JSON.stringify(payload) } as MessageEvent);
    }

    emitRpcError(message: string) {
        const payload = rpcRefusalOf(this.taskId);
        payload.error.message = message;
        this.listeners.get("final_response")?.({ data: JSON.stringify(payload) } as MessageEvent);
    }
}

const originalEventSource = global.EventSource;

beforeAll(() => {
    server.listen({ onUnhandledFrame: "bypass" });
    vi.spyOn(console, "log").mockImplementation(() => {});
    vi.spyOn(console, "debug").mockImplementation(() => {});
});
beforeEach(() => {
    localStorage.clear();
    streams.length = 0;
    global.EventSource = MockEventSource as unknown as typeof EventSource;
});
afterEach(() => {
    global.EventSource = originalEventSource;
    server.resetHandlers();
    localStorage.clear();
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
    const { data: credits } = useFreeModelStatus(true);
    if (!ctx) return <div>no context</div>;
    return (
        <div>
            <output data-testid="remaining">{credits?.remainingPercent ?? "unread"}</output>
            <output data-testid="message-count">{ctx.messages.length}</output>
            <button data-testid="open-refused-session" onClick={() => ctx.handleSwitchSession(REPLAYED_SESSION_ID)}>
                Open Refused Session
            </button>
            <button data-testid="pick-agent" onClick={() => ctx.setSelectedAgentName(AGENT_NAME)}>
                Pick Agent
            </button>
            <button data-testid="submit" onClick={() => ctx.handleSubmit({ preventDefault: () => {} } as unknown as React.FormEvent, null, "hello")}>
                Submit
            </button>
        </div>
    );
}

function renderChatProvider() {
    return render(
        <MemoryRouter initialEntries={["/"]}>
            <StoryProvider shouldIsolateQueryClient userContextValues={{ userHasCapability: () => false }} configContextValues={{ persistenceEnabled: true }}>
                <BuilderContext.Provider value={builderContextValue}>
                    <ChatProvider disableBuilderBridge>
                        <Consumer />
                    </ChatProvider>
                </BuilderContext.Provider>
            </StoryProvider>
        </MemoryRouter>
    );
}

type Failure = "failedTask" | "rpcError";

async function failOnce(text: string, failure: Failure = "failedTask") {
    await act(async () => {
        fireEvent.click(screen.getByTestId("pick-agent"));
    });
    await act(async () => {
        fireEvent.click(screen.getByTestId("submit"));
    });
    await waitFor(() => expect(streams.length).toBeGreaterThan(0));
    await act(async () => {
        const stream = streams.at(-1)!;
        if (failure === "rpcError") stream.emitRpcError(text);
        else stream.emitFailure(text);
    });
}

describe("a chat the free model refuses for its budget", () => {
    test.each<Failure>(["failedTask", "rpcError"])("shows the free credits used up although the allowance still reports some left (%s)", async failure => {
        renderChatProvider();
        await waitFor(() => expect(screen.getByTestId("remaining")).toHaveTextContent("13"));

        await failOnce(ALLOWANCE_USED_UP, failure);

        await waitFor(() => expect(screen.getByTestId("remaining")).toHaveTextContent("0"));
    });

    test("another failure leaves the credits as reported", async () => {
        renderChatProvider();
        await waitFor(() => expect(screen.getByTestId("remaining")).toHaveTextContent("13"));

        await failOnce("LLM error: upstream connect error");

        await new Promise(resolve => setTimeout(resolve, 50));
        expect(screen.getByTestId("remaining")).toHaveTextContent("13");
    });

    test.each<Failure>(["failedTask", "rpcError"])("a refusal replayed when its chat is opened later leaves the credits as reported (%s)", async failure => {
        replayedRefusal = failure === "rpcError" ? rpcRefusalOf(REPLAYED_TASK_ID) : refusalOf(REPLAYED_TASK_ID, REPLAYED_SESSION_ID);
        renderChatProvider();
        await waitFor(() => expect(screen.getByTestId("remaining")).toHaveTextContent("13"));

        await act(async () => {
            fireEvent.click(screen.getByTestId("open-refused-session"));
        });
        await waitFor(() => expect(Number(screen.getByTestId("message-count").textContent)).toBeGreaterThanOrEqual(2));

        await new Promise(resolve => setTimeout(resolve, 100));
        expect(screen.getByTestId("remaining")).toHaveTextContent("13");
        expect(localStorage.getItem(USED_UP_MARK_KEY)).toBeNull();
    });
});
