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

const SESSION = "session-away";
const OTHER_SESSION = "session-other";
const TASK = "task-away";
const OTHER_TASK = "task-other";
const READY = "Your build is ready!";
const STEPS = ["Receiving artifact config.yaml", "Saving artifact config.yaml"];
const SETTLE_MS = 400;

type BufferedEvent = { data: { data: string } };
type StoredTask = { id: string; messageBubbles: string; taskMetadata: string; createdAt: string };

function paginationMeta(count: number) {
    return { pagination: { pageNumber: 1, pageSize: 100, count, totalPages: 1, nextPage: null } };
}

function sse(result: Record<string, unknown>): BufferedEvent {
    return { data: { data: JSON.stringify({ jsonrpc: "2.0", result }) } };
}

function working(taskId: string, parts: Array<Record<string, unknown>>): BufferedEvent {
    return sse({ kind: "status-update", taskId, contextId: SESSION, final: false, status: { state: "working", message: { role: "agent", parts } } });
}

const stepEvent = (taskId: string, text: string) => working(taskId, [{ kind: "data", data: { type: "agent_progress_update", status_text: text } }]);
const textEvent = (taskId: string, text: string) => working(taskId, [{ kind: "text", text }]);
const endEvent = (taskId: string, state: "completed" | "failed", text?: string) => sse({ kind: "task", id: taskId, contextId: SESSION, status: { state, message: { role: "agent", parts: text ? [{ kind: "text", text }] : [] } } });

const userOnlyTask = (id: string, text: string): StoredTask => ({
    id,
    messageBubbles: JSON.stringify([{ id: `${id}-u`, type: "user", text, parts: [{ kind: "text", text }] }]),
    taskMetadata: JSON.stringify({ agentName: "Builder" }),
    createdAt: "2025-01-01T00:00:00.000+00:00",
});

const completeTask = (id: string, question: string, answer: string): StoredTask => ({
    id,
    messageBubbles: JSON.stringify([
        { id: `${id}-u`, type: "user", text: question, parts: [{ kind: "text", text: question }] },
        { id: `${id}-a`, type: "agent", text: answer, parts: [{ kind: "text", text: answer }] },
    ]),
    taskMetadata: JSON.stringify({ agentName: "Builder" }),
    createdAt: "2025-01-01T00:00:00.000+00:00",
});

interface GatewayState {
    tasks: Map<string, StoredTask[]>;
    buffers: Map<string, Map<string, BufferedEvent[]>>;
    gets: { chatTasks: string[]; unconsumed: string[] };
    saves: string[];
    unconsumedGate: { first: Promise<void> | null };
    failNextChatTasks: boolean;
    sessionGetDelayMs: Record<string, number>;
}

let gateway: GatewayState;

function resetGateway() {
    gateway = { tasks: new Map(), buffers: new Map(), gets: { chatTasks: [], unconsumed: [] }, saves: [], unconsumedGate: { first: null }, failNextChatTasks: false, sessionGetDelayMs: {} };
}

function seed(sessionId: string, tasks: StoredTask[], buffered: Record<string, BufferedEvent[]> = {}) {
    gateway.tasks.set(sessionId, tasks);
    gateway.buffers.set(sessionId, new Map(Object.entries(buffered)));
}

const handled = new Map<string, Promise<unknown>>();
function once<T>(requestId: string, work: () => Promise<T> | T): Promise<T> {
    if (!handled.has(requestId)) handled.set(requestId, Promise.resolve(work()));
    return handled.get(requestId) as Promise<T>;
}

const server = setupServer(
    http.get("*/api/v1/sessions/:id/chatTasks", async ({ params, requestId }) => {
        const id = params.id as string;
        const tasks = await once(requestId, () => {
            gateway.gets.chatTasks.push(id);
            if (gateway.failNextChatTasks) {
                gateway.failNextChatTasks = false;
                return null;
            }
            return gateway.tasks.get(id) ?? [];
        });
        if (tasks === null) return new HttpResponse(null, { status: 500 });
        return HttpResponse.json({ data: tasks, meta: paginationMeta(tasks.length) });
    }),
    http.get("*/api/v1/sessions/:id/events/unconsumed", async ({ params, requestId }) => {
        const id = params.id as string;
        const buffer = await once(requestId, async () => {
            gateway.gets.unconsumed.push(id);
            const gate = gateway.unconsumedGate.first;
            gateway.unconsumedGate.first = null;
            if (gate) await gate;
            return new Map(gateway.buffers.get(id) ?? []);
        });
        const eventsByTask = Object.fromEntries([...buffer].map(([taskId, events]) => [taskId, { eventsBuffered: true, events }]));
        return HttpResponse.json({ hasEvents: buffer.size > 0, taskIds: [...buffer.keys()], sessionId: id, eventsByTask });
    }),
    http.get("*/api/v1/sessions/:id", async ({ params }) => {
        const delayMs = gateway.sessionGetDelayMs[params.id as string] ?? 0;
        if (delayMs > 0) await new Promise(resolve => setTimeout(resolve, delayMs));
        return HttpResponse.json({ id: params.id, name: "Away", project: null });
    }),
    http.get("*/api/v1/artifacts/:id", () => HttpResponse.json({ data: [], meta: paginationMeta(0) })),
    http.get("*/api/v1/feedback", () => HttpResponse.json({ data: [], meta: paginationMeta(0) })),
    http.post("*/api/v1/sessions/:id/chatTasks", async ({ params, request, requestId }) => {
        const id = params.id as string;
        const body = (await request.clone().json()) as { id: string; messageBubbles: string; taskMetadata: string };
        await once(requestId, () => {
            gateway.saves.push(body.id);
            const others = (gateway.tasks.get(id) ?? []).filter(t => t.id !== body.id);
            gateway.tasks.set(id, [...others, { id: body.id, messageBubbles: body.messageBubbles, taskMetadata: body.taskMetadata, createdAt: "2025-01-01T00:00:01.000+00:00" }]);
            gateway.buffers.get(id)?.delete(body.id);
        });
        return HttpResponse.json({ id: body.id });
    }),
    http.delete("*/api/v1/tasks/:taskId/events/buffered", ({ params }) => {
        for (const buffer of gateway.buffers.values()) buffer.delete(params.taskId as string);
        return new HttpResponse(null, { status: 204 });
    })
);

beforeAll(() => {
    server.listen({ onUnhandledFrame: "bypass" });
    vi.spyOn(console, "debug").mockImplementation(() => {});
    vi.spyOn(console, "log").mockImplementation(() => {});
});
beforeEach(() => {
    localStorage.clear();
    resetGateway();
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

function transcriptOf(messages: Array<{ isUser?: boolean; parts?: Array<{ kind: string; text?: string }>; progressUpdates?: Array<{ text: string }> }>) {
    return messages
        .map(m => {
            const text = (m.parts ?? []).filter(p => p.kind === "text").map(p => p.text ?? "");
            const steps = (m.progressUpdates ?? []).map(u => u.text);
            return `${m.isUser ? "user" : "agent"}: ${text.join("")} [${steps.join(" / ")}]`;
        })
        .join("\n");
}

function Consumer() {
    const ctx = React.useContext(ChatContext);
    if (!ctx) return <div>no context</div>;
    return (
        <div>
            <pre data-testid="transcript">{transcriptOf(ctx.messages)}</pre>
            <span data-testid="loading">{ctx.isLoadingSession ? "loading" : "idle"}</span>
            <button data-testid="open-away" onClick={() => void ctx.handleSwitchSession(SESSION)} />
            <button data-testid="open-other" onClick={() => void ctx.handleSwitchSession(OTHER_SESSION)} />
            <button data-testid="new-session" onClick={() => void ctx.handleNewSession()} />
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

const transcript = () => screen.getByTestId("transcript").textContent ?? "";
const occurrences = (haystack: string, needle: string) => haystack.split(needle).length - 1;
const settle = () => new Promise(resolve => setTimeout(resolve, SETTLE_MS));
const click = (id: string) => fireEvent.click(screen.getByTestId(id));

async function waitForIdleWith(text: string) {
    await waitFor(() => expect(transcript()).toContain(text));
    await waitFor(() => expect(screen.getByTestId("loading")).toHaveTextContent("idle"));
    await settle();
}

interface Scenario {
    name: string;
    tasks: StoredTask[];
    buffered: Record<string, BufferedEvent[]>;
    shows: string[];
    saved: boolean;
}

const finishedWhileAway: Scenario = {
    name: "turn finished while away",
    tasks: [userOnlyTask(TASK, "build the agent")],
    buffered: { [TASK]: [textEvent(TASK, "Here is the plan."), ...STEPS.map(s => stepEvent(TASK, s)), textEvent(TASK, READY), endEvent(TASK, "completed")] },
    shows: ["Here is the plan.", READY, ...STEPS],
    saved: true,
};
const stillRunning: Scenario = {
    name: "turn still running",
    tasks: [userOnlyTask(TASK, "build the agent")],
    buffered: { [TASK]: [stepEvent(TASK, STEPS[0]), textEvent(TASK, "Working on it")] },
    shows: ["Working on it", STEPS[0]],
    saved: true,
};
const interrupted: Scenario = {
    name: "turn interrupted by an error",
    tasks: [userOnlyTask(TASK, "build the agent")],
    buffered: { [TASK]: [stepEvent(TASK, STEPS[0]), endEvent(TASK, "failed", "The build failed halfway")] },
    shows: ["The build failed halfway", STEPS[0]],
    saved: true,
};
const emptyBuffer: Scenario = {
    name: "nothing buffered",
    tasks: [completeTask(TASK, "build the agent", READY)],
    buffered: {},
    shows: [READY],
    saved: false,
};

const SCENARIOS = [finishedWhileAway, stillRunning, interrupted, emptyBuffer];

type Opening = { name: string; open: () => Promise<void> };

function releaseGate() {
    let release!: () => void;
    const gate = new Promise<void>(resolve => (release = resolve));
    return { gate, release };
}

const OPENINGS: Opening[] = [
    {
        name: "one open",
        open: async () => {
            click("open-away");
        },
    },
    {
        name: "two overlapping opens",
        open: async () => {
            click("open-away");
            click("open-away");
        },
    },
    {
        name: "a second open that starts while the first still waits for the buffer",
        open: async () => {
            const { gate, release } = releaseGate();
            gateway.unconsumedGate.first = gate;
            click("open-away");
            await waitFor(() => expect(gateway.gets.unconsumed).toHaveLength(1));
            click("open-away");
            await waitFor(() => expect(gateway.gets.unconsumed).toHaveLength(2));
            release();
        },
    },
];

function expectEachOnce(scenario: Scenario) {
    for (const text of scenario.shows) {
        expect(occurrences(transcript(), text), `"${text}" in:\n${transcript()}`).toBe(1);
    }
}

describe("ChatProvider session open", () => {
    describe.each(SCENARIOS)("$name", scenario => {
        test.each(OPENINGS)("shows each message and step once on the first and the second open: $name", async ({ open }) => {
            seed(SESSION, scenario.tasks, scenario.buffered);
            renderChatProvider();

            await open();
            await waitForIdleWith(scenario.shows[0]);
            expectEachOnce(scenario);
            expect(occurrences(transcript(), "user: build the agent")).toBe(1);

            if (scenario.saved) await waitFor(() => expect(gateway.saves).toContain(TASK));

            click("new-session");
            await waitFor(() => expect(transcript()).toBe(""));
            click("open-away");
            await waitForIdleWith(scenario.shows[0]);
            expectEachOnce(scenario);
            expect(occurrences(transcript(), "user: build the agent")).toBe(1);
        });
    });

    test("replays the buffered events of a task with no saved answer", async () => {
        seed(SESSION, finishedWhileAway.tasks, finishedWhileAway.buffered);
        renderChatProvider();

        click("open-away");
        await waitForIdleWith(READY);

        expect(transcript()).toContain("Here is the plan.");
        expect(gateway.saves).toEqual([TASK]);
    });

    test("opening another session right after the first does not mix the first one's replay into it", async () => {
        seed(SESSION, finishedWhileAway.tasks, finishedWhileAway.buffered);
        seed(OTHER_SESSION, [completeTask(OTHER_TASK, "other question", "other answer")]);
        renderChatProvider();

        click("open-away");
        await waitFor(() => expect(transcript()).toContain("user: build the agent"));
        click("open-other");
        await waitForIdleWith("other answer");

        expect(transcript()).not.toContain(READY);
        expect(transcript()).not.toContain(STEPS[0]);
        expect(occurrences(transcript(), "other answer")).toBe(1);
        expect(gateway.saves).toEqual([]);
    });

    test("opening another session whose lookup is slow does not replay the first one into the cleared chat or save it", async () => {
        seed(SESSION, finishedWhileAway.tasks, finishedWhileAway.buffered);
        seed(OTHER_SESSION, [completeTask(OTHER_TASK, "other question", "other answer")]);
        gateway.sessionGetDelayMs[OTHER_SESSION] = 300;
        renderChatProvider();

        click("open-away");
        await waitFor(() => expect(transcript()).toContain("user: build the agent"));
        click("open-other");
        await waitForIdleWith("other answer");

        expect(gateway.saves).toEqual([]);
        expect(transcript()).not.toContain(READY);
    });

    test("starting a new chat right after opening the session does not refill it with the replay", async () => {
        seed(SESSION, finishedWhileAway.tasks, finishedWhileAway.buffered);
        renderChatProvider();

        click("open-away");
        await waitFor(() => expect(transcript()).toContain("user: build the agent"));
        click("new-session");
        await settle();

        expect(transcript()).toBe("");
        expect(gateway.saves).toEqual([]);
    });

    test("starting a new chat while the session is still loading does not fill it with the session", async () => {
        seed(SESSION, finishedWhileAway.tasks, finishedWhileAway.buffered);
        renderChatProvider();
        const { gate, release } = releaseGate();
        gateway.unconsumedGate.first = gate;

        click("open-away");
        await waitFor(() => expect(gateway.gets.unconsumed).toHaveLength(1));
        click("new-session");
        release();
        await settle();

        expect(transcript()).toBe("");
        expect(gateway.saves).toEqual([]);
    });

    test("a slow first open that finishes after the second one does not overwrite it with stale data", async () => {
        seed(SESSION, finishedWhileAway.tasks, finishedWhileAway.buffered);
        renderChatProvider();
        const { gate, release } = releaseGate();
        gateway.unconsumedGate.first = gate;

        click("open-away");
        await waitFor(() => expect(gateway.gets.unconsumed).toHaveLength(1));
        click("open-away");
        await waitFor(() => expect(gateway.saves).toContain(TASK));
        release();
        await settle();

        expectEachOnce(finishedWhileAway);
    });

    test("a second open of the same session that fails does not blank the chat or lose the replay", async () => {
        seed(SESSION, finishedWhileAway.tasks, finishedWhileAway.buffered);
        renderChatProvider();
        const { gate, release } = releaseGate();
        gateway.unconsumedGate.first = gate;

        click("open-away");
        await waitFor(() => expect(gateway.gets.unconsumed).toHaveLength(1));
        gateway.failNextChatTasks = true;
        click("open-away");
        await waitFor(() => expect(gateway.gets.chatTasks).toHaveLength(2));
        release();

        await waitForIdleWith(READY);
        expect(transcript()).toContain("user: build the agent");
        expectEachOnce(finishedWhileAway);
        expect(gateway.saves).toEqual([TASK]);
    });
});
