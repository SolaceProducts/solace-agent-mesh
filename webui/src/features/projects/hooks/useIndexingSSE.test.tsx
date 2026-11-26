// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { useSSEContext } from "@/providers/contexts";
import { SSEProvider } from "@/providers/SSEProvider";

import { useIndexingSSE, useStartIndexing } from "./useIndexingSSE";

class MockEventSource {
    static instances: MockEventSource[] = [];

    url: string;
    withCredentials: boolean;
    readyState: number = 0;
    onopen: ((event: Event) => void) | null = null;
    onmessage: ((event: MessageEvent) => void) | null = null;
    onerror: ((event: Event) => void) | null = null;
    private eventListeners: Map<string, EventListener[]> = new Map();

    constructor(url: string, options?: { withCredentials?: boolean }) {
        this.url = url;
        this.withCredentials = options?.withCredentials ?? false;
        MockEventSource.instances.push(this);
    }

    close() {
        this.readyState = 2;
    }

    addEventListener(type: string, listener: EventListener) {
        const listeners = this.eventListeners.get(type) || [];
        listeners.push(listener);
        this.eventListeners.set(type, listeners);
    }

    removeEventListener(type: string, listener: EventListener) {
        const listeners = this.eventListeners.get(type) || [];
        this.eventListeners.set(
            type,
            listeners.filter(l => l !== listener)
        );
    }

    simulateOpen() {
        this.readyState = 1;
        this.onopen?.(new Event("open"));
    }

    simulateCustomEvent(type: string, data: string) {
        const listeners = this.eventListeners.get(type) || [];
        const event = new MessageEvent(type, { data });
        listeners.forEach(listener => listener(event));
    }

    static clear() {
        MockEventSource.instances = [];
    }

    static getLatest(): MockEventSource | undefined {
        return MockEventSource.instances[MockEventSource.instances.length - 1];
    }
}

const originalEventSource = global.EventSource;

beforeEach(() => {
    global.EventSource = MockEventSource as unknown as typeof EventSource;
    MockEventSource.clear();
    sessionStorage.clear();
    localStorage.clear();
    localStorage.setItem("access_token", "mock-token");
    vi.clearAllMocks();
});

afterEach(() => {
    global.EventSource = originalEventSource;
});

function TestStartIndexing({ metadataKey }: { metadataKey?: string }) {
    const startIndexing = useStartIndexing(metadataKey);
    const { getTasks } = useSSEContext();

    return (
        <div>
            <button data-testid="start" onClick={() => startIndexing("/api/v1/sse/subscribe/task-abc", "project-1", "upload")}>
                Start
            </button>
            <div data-testid="task-count">{getTasks().length}</div>
            <div data-testid="task-data">{JSON.stringify(getTasks())}</div>
        </div>
    );
}

function TestIndexingSSE({
    resourceId,
    metadataKey,
    onComplete,
    onEvent,
}: {
    resourceId: string;
    metadataKey?: string;
    onComplete?: (failedFiles: string[], errors: string[]) => void;
    onEvent?: (event: { type: string; [key: string]: unknown }) => void;
}) {
    const { isIndexing, connectionState, latestEvent, startIndexing } = useIndexingSSE({
        resourceId,
        metadataKey,
        onComplete,
        onEvent,
    });

    return (
        <div>
            <div data-testid="is-indexing">{String(isIndexing)}</div>
            <div data-testid="connection-state">{connectionState}</div>
            <div data-testid="latest-event">{latestEvent ? JSON.stringify(latestEvent) : "none"}</div>
            <button data-testid="start-indexing" onClick={() => startIndexing("/api/v1/sse/subscribe/task-xyz", resourceId, "upload")}>
                Start Indexing
            </button>
        </div>
    );
}

describe("useStartIndexing", () => {
    test("extracts taskId from URL, stores sseUrl, and uses default metadataKey", async () => {
        const user = userEvent.setup();

        render(
            <SSEProvider>
                <TestStartIndexing />
            </SSEProvider>
        );

        await user.click(screen.getByTestId("start"));

        const tasks = JSON.parse(screen.getByTestId("task-data").textContent!);
        expect(tasks).toHaveLength(1);
        expect(tasks[0].taskId).toBe("task-abc");
        expect(tasks[0].sseUrl).toBe("/api/v1/sse/subscribe/task-abc");
        expect(tasks[0].metadata).toEqual({ resourceId: "project-1", operation: "upload" });
    });

    test("uses custom metadataKey when provided", async () => {
        const user = userEvent.setup();

        render(
            <SSEProvider>
                <TestStartIndexing metadataKey="projectId" />
            </SSEProvider>
        );

        await user.click(screen.getByTestId("start"));

        const tasks = JSON.parse(screen.getByTestId("task-data").textContent!);
        expect(tasks[0].metadata).toEqual({ projectId: "project-1", operation: "upload" });
    });
});

describe("useIndexingSSE", () => {
    test("calls onComplete and unregisters task on task_completed", async () => {
        const user = userEvent.setup();
        const onComplete = vi.fn();
        const onEvent = vi.fn();

        render(
            <SSEProvider>
                <TestIndexingSSE resourceId="project-1" onComplete={onComplete} onEvent={onEvent} />
            </SSEProvider>
        );

        expect(screen.getByTestId("is-indexing")).toHaveTextContent("false");

        await user.click(screen.getByTestId("start-indexing"));
        expect(screen.getByTestId("is-indexing")).toHaveTextContent("true");

        await waitFor(() => {
            expect(MockEventSource.instances).toHaveLength(1);
        });

        const eventSource = MockEventSource.getLatest()!;
        act(() => eventSource.simulateOpen());

        act(() => eventSource.simulateCustomEvent("index_message", JSON.stringify({ type: "progress", percent: 50 })));
        expect(onEvent).toHaveBeenCalledWith({ type: "progress", percent: 50 });

        await waitFor(() => {
            expect(screen.getByTestId("latest-event")).toHaveTextContent('"type":"progress"');
        });

        act(() => eventSource.simulateCustomEvent("index_message", JSON.stringify({ type: "task_completed" })));

        expect(onComplete).toHaveBeenCalledWith([], []);
        await waitFor(() => {
            expect(screen.getByTestId("is-indexing")).toHaveTextContent("false");
            expect(screen.getByTestId("latest-event")).toHaveTextContent("none");
        });
    });

    test("accumulates conversion_failed errors and reports on task_completed", async () => {
        const user = userEvent.setup();
        const onComplete = vi.fn();

        render(
            <SSEProvider>
                <TestIndexingSSE resourceId="project-1" onComplete={onComplete} />
            </SSEProvider>
        );

        await user.click(screen.getByTestId("start-indexing"));
        await waitFor(() => expect(MockEventSource.instances).toHaveLength(1));

        const eventSource = MockEventSource.getLatest()!;
        act(() => eventSource.simulateOpen());

        act(() => eventSource.simulateCustomEvent("index_message", JSON.stringify({ type: "conversion_failed", file: "doc.pdf", error: "Failed to convert 'doc.pdf'" })));
        expect(screen.getByTestId("is-indexing")).toHaveTextContent("true");
        expect(onComplete).not.toHaveBeenCalled();

        act(() => eventSource.simulateCustomEvent("index_message", JSON.stringify({ type: "task_completed" })));
        expect(onComplete).toHaveBeenCalledWith(["doc.pdf"], []);
        await waitFor(() => expect(screen.getByTestId("is-indexing")).toHaveTextContent("false"));
    });

    test("accumulates indexing_failed with fallback label and reports on task_completed", async () => {
        const user = userEvent.setup();
        const onComplete = vi.fn();

        render(
            <SSEProvider>
                <TestIndexingSSE resourceId="project-1" onComplete={onComplete} />
            </SSEProvider>
        );

        await user.click(screen.getByTestId("start-indexing"));
        await waitFor(() => expect(MockEventSource.instances).toHaveLength(1));

        const eventSource = MockEventSource.getLatest()!;
        act(() => eventSource.simulateOpen());

        act(() => eventSource.simulateCustomEvent("index_message", JSON.stringify({ type: "indexing_failed", error: { code: 500 } })));
        expect(screen.getByTestId("is-indexing")).toHaveTextContent("true");

        act(() => eventSource.simulateCustomEvent("index_message", JSON.stringify({ type: "task_completed" })));
        expect(onComplete).toHaveBeenCalledWith([], ["Indexing failed"]);
        await waitFor(() => expect(screen.getByTestId("is-indexing")).toHaveTextContent("false"));
    });

    test("task_error is terminal and reports error immediately", async () => {
        const user = userEvent.setup();
        const onComplete = vi.fn();
        const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});

        render(
            <SSEProvider>
                <TestIndexingSSE resourceId="project-1" onComplete={onComplete} />
            </SSEProvider>
        );

        await user.click(screen.getByTestId("start-indexing"));
        await waitFor(() => expect(MockEventSource.instances).toHaveLength(1));

        const eventSource = MockEventSource.getLatest()!;
        act(() => eventSource.simulateOpen());

        act(() => eventSource.simulateCustomEvent("index_message", JSON.stringify({ type: "task_error", error: "Background task crashed" })));
        expect(onComplete).toHaveBeenCalledWith([], ["Background task crashed"]);
        await waitFor(() => expect(screen.getByTestId("is-indexing")).toHaveTextContent("false"));

        consoleSpy.mockRestore();
    });

    test("handles malformed JSON without crashing", async () => {
        const user = userEvent.setup();
        const onEvent = vi.fn();
        const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});

        render(
            <SSEProvider>
                <TestIndexingSSE resourceId="project-1" onEvent={onEvent} />
            </SSEProvider>
        );

        await user.click(screen.getByTestId("start-indexing"));
        await waitFor(() => expect(MockEventSource.instances).toHaveLength(1));

        const eventSource = MockEventSource.getLatest()!;
        act(() => eventSource.simulateOpen());
        act(() => eventSource.simulateCustomEvent("index_message", "not valid json{{{"));

        expect(onEvent).not.toHaveBeenCalled();
        expect(screen.getByTestId("is-indexing")).toHaveTextContent("true");

        consoleSpy.mockRestore();
    });

    test("picks up existing task from sessionStorage on mount", async () => {
        sessionStorage.setItem(
            "sam_sse_tasks",
            JSON.stringify([
                {
                    taskId: "existing-task",
                    sseUrl: "/api/v1/sse/subscribe/existing-task",
                    metadata: { resourceId: "project-1", operation: "upload" },
                    registeredAt: Date.now(),
                },
            ])
        );

        render(
            <SSEProvider>
                <TestIndexingSSE resourceId="project-1" />
            </SSEProvider>
        );

        await waitFor(() => {
            expect(screen.getByTestId("is-indexing")).toHaveTextContent("true");
        });

        await waitFor(() => {
            expect(MockEventSource.instances).toHaveLength(1);
        });
    });

    test("ignores tasks registered under a different resourceId", () => {
        sessionStorage.setItem(
            "sam_sse_tasks",
            JSON.stringify([
                {
                    taskId: "other-task",
                    sseUrl: "/api/v1/sse/subscribe/other-task",
                    metadata: { resourceId: "project-2", operation: "upload" },
                    registeredAt: Date.now(),
                },
            ])
        );

        render(
            <SSEProvider>
                <TestIndexingSSE resourceId="project-1" />
            </SSEProvider>
        );

        expect(screen.getByTestId("is-indexing")).toHaveTextContent("false");
        expect(MockEventSource.instances).toHaveLength(0);
    });
});
