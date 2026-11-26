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

import { getPaginatedMeta } from "@/testing/helpers";
import { StoryProvider } from "@/testing/providers/StoryProvider";
import type { Agent } from "@/types";

import { ChatProvider } from "./ChatProvider";

const AGENT_NAME = "TestAgent";
const SESSION_ID = "0199b2c1-0000-7000-8000-000000000002";

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
    http.get("*/api/v2/platform/agents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/remoteAgents", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v2/platform/connectors", () => HttpResponse.json({ data: [], meta: getPaginatedMeta(0) })),
    http.get("*/api/v1/agentCards", () => HttpResponse.json({ data: [] })),
    http.delete("*/api/v2/platform/agents/:id", () => new HttpResponse(null, { status: 204 }))
);

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

    emitFinalResponse() {
        const payload = {
            jsonrpc: "2.0",
            id: "evt-1",
            result: {
                kind: "task",
                id: this.taskId,
                contextId: SESSION_ID,
                status: { state: "completed", message: { role: "agent", parts: [{ kind: "text", text: "all done" }] } },
            },
        };
        this.listeners.get("final_response")?.({ data: JSON.stringify(payload) } as MessageEvent);
    }
}

const originalEventSource = global.EventSource;
let trackSpy: ReturnType<typeof vi.spyOn>;
let firstEverSpy: ReturnType<typeof vi.spyOn>;

beforeAll(() => {
    server.listen({ onUnhandledFrame: "bypass" });
    vi.spyOn(console, "log").mockImplementation(() => {});
    vi.spyOn(console, "debug").mockImplementation(() => {});
});
beforeEach(() => {
    localStorage.clear();
    streams.length = 0;
    global.EventSource = MockEventSource as unknown as typeof EventSource;
    trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
    firstEverSpy = vi.spyOn(productAnalyticsClient, "trackFirstEver");
    productAnalyticsClient.setFeatureEnabled(true);
    productAnalyticsClient.setConsent(true);
});
afterEach(() => {
    global.EventSource = originalEventSource;
    server.resetHandlers();
    productAnalyticsClient.setConsent(false);
    productAnalyticsClient.setFeatureEnabled(false);
    productAnalyticsClient.destroy();
    vi.restoreAllMocks();
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
    return (
        <div>
            <button data-testid="pick-agent" onClick={() => ctx.setSelectedAgentName(AGENT_NAME)}>
                Pick Agent
            </button>
            <button data-testid="pick-builder-agent" onClick={() => ctx.setSelectedAgentName("Builder")}>
                Pick Builder Agent
            </button>
            <button data-testid="submit" onClick={() => void ctx.handleSubmit({ preventDefault: () => {} } as unknown as React.FormEvent, null, "hello")}>
                Submit
            </button>
            <button data-testid="create-agent" onClick={() => ctx.handleCreateAgent({ name: "Draft" } as Agent)}>
                Create Agent
            </button>
            <button data-testid="delete-agent" onClick={() => void ctx.handleDeleteAgent({ id: "agent-1" } as Agent)}>
                Delete Agent
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

async function respondOnce() {
    await act(async () => {
        fireEvent.click(screen.getByTestId("pick-agent"));
    });
    await act(async () => {
        fireEvent.click(screen.getByTestId("submit"));
    });
    await waitFor(() => expect(streams.length).toBeGreaterThan(0));
    await act(async () => {
        streams.at(-1)!.emitFinalResponse();
    });
}

function eventsNamed(name: string): unknown[][] {
    return (trackSpy.mock.calls as unknown[][]).filter(call => call[0] === name);
}

describe("chat:first_response", () => {
    test("is reported when the first response completes", async () => {
        renderChatProvider();

        await respondOnce();

        expect(firstEverSpy).toHaveBeenCalledWith("chat:first_response", {});
        await waitFor(() => expect(eventsNamed("chat:first_response")).toHaveLength(1));
    });

    test("is not reported again for a second response in the same session", async () => {
        renderChatProvider();

        await respondOnce();
        await waitFor(() => expect(eventsNamed("chat:first_response")).toHaveLength(1));
        await respondOnce();

        expect(firstEverSpy.mock.calls.length).toBeGreaterThan(1);
        expect(eventsNamed("chat:first_response")).toHaveLength(1);
    });

    test("is not reported again after a reload, because the milestone outlives the page", async () => {
        const first = renderChatProvider();
        await respondOnce();
        await waitFor(() => expect(eventsNamed("chat:first_response")).toHaveLength(1));
        first.unmount();

        streams.length = 0;
        productAnalyticsClient.destroy();
        productAnalyticsClient.setFeatureEnabled(true);
        productAnalyticsClient.setConsent(true);
        renderChatProvider();
        await respondOnce();

        expect(eventsNamed("chat:first_response")).toHaveLength(1);
    });

    test("is reported by a genuinely new install", async () => {
        renderChatProvider();
        await respondOnce();
        await waitFor(() => expect(eventsNamed("chat:first_response")).toHaveLength(1));

        localStorage.clear();
        streams.length = 0;
        await respondOnce();

        await waitFor(() => expect(eventsNamed("chat:first_response")).toHaveLength(2));
    });
});

describe("agent lifecycle from the chat context", () => {
    test("starting an agent creation is reported when either dialog hands off", async () => {
        renderChatProvider();

        await act(async () => {
            fireEvent.click(screen.getByTestId("create-agent"));
        });

        expect(eventsNamed("agent:creation_started")).toEqual([["agent:creation_started", {}]]);
    });

    test("deleting an agent reports nothing from the browser", async () => {
        renderChatProvider();

        await act(async () => {
            fireEvent.click(screen.getByTestId("delete-agent"));
        });

        expect(eventsNamed("agent:deleted")).toHaveLength(0);
    });
});

describe("submitting to the Builder without access", () => {
    test("reports the refusal and never streams a message", async () => {
        renderChatProvider();

        await act(async () => {
            fireEvent.click(screen.getByTestId("pick-builder-agent"));
        });
        await act(async () => {
            fireEvent.click(screen.getByTestId("submit"));
        });

        expect(eventsNamed("error:displayed")).toEqual([["error:displayed", { errorCategory: expect.any(String), errorSource: "client", page: expect.any(String) }]]);
        expect(streams).toHaveLength(0);
    });
});
