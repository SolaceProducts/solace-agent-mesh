// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { setupServer } from "msw/node";
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from "vitest";

import { useSSEContext } from "@/providers/contexts";
import { SSEProvider, useSSESubscription } from "@/providers/SSEProvider";

import type { SSEConnectionState } from "@/types";

const server = setupServer();

const CONNECTING = 0;
const OPEN = 1;
const CLOSED = 2;

class MockEventSource {
    static instances: MockEventSource[] = [];

    url: string;
    withCredentials: boolean;
    readyState: number = CONNECTING;
    onopen: ((event: Event) => void) | null = null;
    onmessage: ((event: MessageEvent) => void) | null = null;
    onerror: ((event: Event) => void) | null = null;
    listeners = new Map<string, Set<EventListener>>();

    constructor(url: string, options?: { withCredentials?: boolean }) {
        this.url = url;
        this.withCredentials = options?.withCredentials ?? false;
        MockEventSource.instances.push(this);
    }

    close() {
        this.readyState = CLOSED;
    }

    addEventListener(type: string, listener: EventListener) {
        const existing = this.listeners.get(type);
        if (existing) {
            existing.add(listener);
        } else {
            this.listeners.set(type, new Set([listener]));
        }
    }

    removeEventListener(type: string, listener: EventListener) {
        this.listeners.get(type)?.delete(listener);
    }

    simulateOpen() {
        this.readyState = OPEN;
        this.onopen?.(new Event("open"));
    }

    simulateMessage(data: string) {
        this.onmessage?.(new MessageEvent("message", { data }));
    }

    simulateNamedEvent(type: string, data: string) {
        const event = new MessageEvent(type, { data });
        this.listeners.get(type)?.forEach(listener => listener(event));
    }

    simulateError() {
        this.onerror?.(new Event("error"));
    }

    static clear() {
        MockEventSource.instances = [];
    }

    static getLatest(): MockEventSource | undefined {
        return MockEventSource.instances[MockEventSource.instances.length - 1];
    }
}

const originalEventSource = global.EventSource;

beforeAll(() => server.listen());
afterAll(() => server.close());

beforeEach(() => {
    global.EventSource = MockEventSource as unknown as typeof EventSource;
    MockEventSource.clear();
    sessionStorage.clear();
    localStorage.clear();
    localStorage.setItem("access_token", "mock-token");
    vi.clearAllMocks();
    server.resetHandlers();
});

afterEach(() => {
    global.EventSource = originalEventSource;
});

function TestSubscriber({
    endpoint,
    taskId,
    eventType,
    onStateChange,
    onMessage,
    onError,
    onTaskAlreadyCompleted,
}: {
    endpoint: string | null;
    taskId?: string;
    eventType?: string;
    onStateChange?: (state: SSEConnectionState) => void;
    onMessage?: (event: MessageEvent) => void;
    onError?: (event: Event) => void;
    onTaskAlreadyCompleted?: () => void;
}) {
    const { connectionState } = useSSESubscription({
        endpoint,
        taskId,
        eventType,
        onMessage,
        onError,
        onTaskAlreadyCompleted,
    });

    if (onStateChange) {
        onStateChange(connectionState);
    }

    return <div data-testid="connection-state">{connectionState}</div>;
}

function TestTaskRegistry({ onTasksChange }: { onTasksChange?: (tasks: ReturnType<typeof useSSEContext>["getTasks"]) => void }) {
    const { registerTask, unregisterTask, getTask, getTasks, getTasksByMetadata } = useSSEContext();

    return (
        <div>
            <button
                data-testid="register-task"
                onClick={() =>
                    registerTask({
                        taskId: "task-123",
                        sseUrl: "http://localhost/sse/task-123",
                        metadata: { projectId: "project-1" },
                    })
                }
            >
                Register Task
            </button>
            <button data-testid="unregister-task" onClick={() => unregisterTask("task-123")}>
                Unregister Task
            </button>
            <button data-testid="get-tasks" onClick={() => onTasksChange?.(getTasks)}>
                Get Tasks
            </button>
            <div data-testid="task-count">{getTasks().length}</div>
            <div data-testid="task-by-id">{getTask("task-123")?.taskId ?? "none"}</div>
            <div data-testid="tasks-by-metadata">{getTasksByMetadata("projectId", "project-1").length}</div>
        </div>
    );
}

describe("SSEProvider", () => {
    describe("useSSESubscription", () => {
        test("starts disconnected when endpoint is null", () => {
            render(
                <SSEProvider>
                    <TestSubscriber endpoint={null} />
                </SSEProvider>
            );

            expect(screen.getByTestId("connection-state")).toHaveTextContent("disconnected");
            expect(MockEventSource.instances).toHaveLength(0);
        });

        test("creates EventSource when endpoint is provided", async () => {
            render(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" />
                </SSEProvider>
            );

            await waitFor(() => {
                expect(MockEventSource.instances).toHaveLength(1);
            });

            const eventSource = MockEventSource.getLatest();
            expect(eventSource?.url).toContain("http://localhost/sse");
            expect(eventSource?.url).toContain("token=mock-token");
            expect(eventSource?.withCredentials).toBe(true);
        });

        test("transitions to connected state on open", async () => {
            render(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" />
                </SSEProvider>
            );

            await waitFor(() => {
                expect(MockEventSource.instances).toHaveLength(1);
            });

            act(() => {
                MockEventSource.getLatest()?.simulateOpen();
            });

            await waitFor(() => {
                expect(screen.getByTestId("connection-state")).toHaveTextContent("connected");
            });
        });

        test("calls onMessage callback when message received", async () => {
            const onMessage = vi.fn();

            render(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" onMessage={onMessage} />
                </SSEProvider>
            );

            await waitFor(() => {
                expect(MockEventSource.instances).toHaveLength(1);
            });

            act(() => {
                MockEventSource.getLatest()?.simulateOpen();
            });

            act(() => {
                MockEventSource.getLatest()?.simulateMessage('{"type":"test","data":"hello"}');
            });

            expect(onMessage).toHaveBeenCalledTimes(1);
            expect(onMessage).toHaveBeenCalledWith(expect.objectContaining({ data: '{"type":"test","data":"hello"}' }));
        });

        test("closes EventSource when endpoint becomes null", async () => {
            const { rerender } = render(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" />
                </SSEProvider>
            );

            await waitFor(() => {
                expect(MockEventSource.instances).toHaveLength(1);
            });

            const eventSource = MockEventSource.getLatest();

            rerender(
                <SSEProvider>
                    <TestSubscriber endpoint={null} />
                </SSEProvider>
            );

            await waitFor(() => {
                expect(eventSource?.readyState).toBe(2);
            });
        });

        test("deduplicates connections for same endpoint", async () => {
            render(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" />
                    <TestSubscriber endpoint="http://localhost/sse" />
                </SSEProvider>
            );

            await waitFor(() => {
                expect(MockEventSource.instances).toHaveLength(1);
            });
        });

        test("broadcasts messages to all subscribers on same endpoint", async () => {
            const onMessage1 = vi.fn();
            const onMessage2 = vi.fn();

            render(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" onMessage={onMessage1} />
                    <TestSubscriber endpoint="http://localhost/sse" onMessage={onMessage2} />
                </SSEProvider>
            );

            await waitFor(() => {
                expect(MockEventSource.instances).toHaveLength(1);
            });

            act(() => {
                MockEventSource.getLatest()?.simulateOpen();
            });

            act(() => {
                MockEventSource.getLatest()?.simulateMessage('{"type":"broadcast"}');
            });

            expect(onMessage1).toHaveBeenCalledTimes(1);
            expect(onMessage2).toHaveBeenCalledTimes(1);
        });

        test("keeps connection open when one subscriber unmounts but another remains", async () => {
            let setShowSecond: (show: boolean) => void = () => {};

            function TestWrapper() {
                const [showSecond, _setShowSecond] = React.useState(true);
                setShowSecond = _setShowSecond;
                return (
                    <>
                        <TestSubscriber endpoint="http://localhost/sse" />
                        {showSecond && <TestSubscriber endpoint="http://localhost/sse" />}
                    </>
                );
            }

            render(
                <SSEProvider>
                    <TestWrapper />
                </SSEProvider>
            );

            await waitFor(() => {
                expect(MockEventSource.instances).toHaveLength(1);
            });

            const eventSource = MockEventSource.getLatest();

            act(() => {
                setShowSecond(false);
            });

            expect(eventSource?.readyState).not.toBe(2);
        });
    });

    describe("Task Registry", () => {
        test("registers a task", async () => {
            const user = userEvent.setup();

            render(
                <SSEProvider>
                    <TestTaskRegistry />
                </SSEProvider>
            );

            expect(screen.getByTestId("task-count")).toHaveTextContent("0");

            await user.click(screen.getByTestId("register-task"));

            expect(screen.getByTestId("task-count")).toHaveTextContent("1");
            expect(screen.getByTestId("task-by-id")).toHaveTextContent("task-123");
        });

        test("unregisters a task", async () => {
            const user = userEvent.setup();

            render(
                <SSEProvider>
                    <TestTaskRegistry />
                </SSEProvider>
            );

            await user.click(screen.getByTestId("register-task"));
            expect(screen.getByTestId("task-count")).toHaveTextContent("1");

            await user.click(screen.getByTestId("unregister-task"));
            expect(screen.getByTestId("task-count")).toHaveTextContent("0");
        });

        test("queries tasks by metadata", async () => {
            const user = userEvent.setup();

            render(
                <SSEProvider>
                    <TestTaskRegistry />
                </SSEProvider>
            );

            await user.click(screen.getByTestId("register-task"));

            expect(screen.getByTestId("tasks-by-metadata")).toHaveTextContent("1");
        });

        test("persists tasks to sessionStorage", async () => {
            const user = userEvent.setup();

            render(
                <SSEProvider>
                    <TestTaskRegistry />
                </SSEProvider>
            );

            await user.click(screen.getByTestId("register-task"));

            const stored = sessionStorage.getItem("sam_sse_tasks");
            expect(stored).not.toBeNull();

            const parsed = JSON.parse(stored!);
            expect(parsed).toHaveLength(1);
            expect(parsed[0].taskId).toBe("task-123");
        });

        test("loads tasks from sessionStorage on mount", async () => {
            sessionStorage.setItem(
                "sam_sse_tasks",
                JSON.stringify([
                    {
                        taskId: "persisted-task",
                        sseUrl: "http://localhost/sse/persisted",
                        metadata: {},
                        registeredAt: Date.now(),
                    },
                ])
            );

            render(
                <SSEProvider>
                    <TestTaskRegistry />
                </SSEProvider>
            );

            await waitFor(() => {
                expect(screen.getByTestId("task-count")).toHaveTextContent("1");
            });
        });

        test("removes stale tasks on mount", async () => {
            const staleTime = Date.now() - 2 * 60 * 60 * 1000;
            sessionStorage.setItem(
                "sam_sse_tasks",
                JSON.stringify([
                    {
                        taskId: "stale-task",
                        sseUrl: "http://localhost/sse/stale",
                        metadata: {},
                        registeredAt: staleTime,
                    },
                ])
            );

            render(
                <SSEProvider>
                    <TestTaskRegistry />
                </SSEProvider>
            );

            await waitFor(() => {
                expect(screen.getByTestId("task-count")).toHaveTextContent("0");
            });
        });

        test("does not add duplicate tasks", async () => {
            const user = userEvent.setup();

            render(
                <SSEProvider>
                    <TestTaskRegistry />
                </SSEProvider>
            );

            await user.click(screen.getByTestId("register-task"));
            await user.click(screen.getByTestId("register-task"));

            expect(screen.getByTestId("task-count")).toHaveTextContent("1");
        });
    });

    describe("Status check on reconnect", () => {
        test("checks task status before connecting when taskId provided", async () => {
            let statusCheckCalled = false;

            server.use(
                http.get("*/api/v1/tasks/task-123/status", () => {
                    statusCheckCalled = true;
                    return HttpResponse.json({ isRunning: true });
                })
            );

            render(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" taskId="task-123" />
                </SSEProvider>
            );

            await waitFor(() => {
                expect(statusCheckCalled).toBe(true);
            });

            await waitFor(() => {
                expect(MockEventSource.instances).toHaveLength(1);
            });
        });

        test("calls onTaskAlreadyCompleted when task is not running", async () => {
            server.use(
                http.get("*/api/v1/tasks/completed-task/status", () => {
                    return HttpResponse.json({ isRunning: false });
                })
            );

            const onTaskAlreadyCompleted = vi.fn();

            render(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" taskId="completed-task" onTaskAlreadyCompleted={onTaskAlreadyCompleted} />
                </SSEProvider>
            );

            await waitFor(
                () => {
                    expect(onTaskAlreadyCompleted).toHaveBeenCalledTimes(1);
                },
                { timeout: 3000 }
            );

            expect(MockEventSource.instances).toHaveLength(0);
        });

        test("proceeds with SSE if status check fails", async () => {
            server.use(
                http.get("*/api/v1/tasks/task-123/status", () => {
                    return HttpResponse.error();
                })
            );

            render(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" taskId="task-123" />
                </SSEProvider>
            );

            await waitFor(() => {
                expect(MockEventSource.instances).toHaveLength(1);
            });
        });
    });

    describe("Error handling", () => {
        test("throws error when useSSESubscription used outside provider", () => {
            const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});

            expect(() => {
                render(<TestSubscriber endpoint="http://localhost/sse" />);
            }).toThrow("useSSESubscription must be used within SSEProvider");

            consoleSpy.mockRestore();
        });

        test("throws error when useSSEContext used outside provider", () => {
            const consoleSpy = vi.spyOn(console, "error").mockImplementation(() => {});

            expect(() => {
                render(<TestTaskRegistry />);
            }).toThrow("useSSEContext must be used within SSEProvider");

            consoleSpy.mockRestore();
        });
    });

    describe("reconnection", () => {
        const BEYOND_MAX_BACKOFF_MS = 60_000;
        const MAX_RETRY_ATTEMPTS = 20;

        beforeEach(() => {
            vi.useFakeTimers({ shouldAdvanceTime: true });
        });

        afterEach(() => {
            vi.useRealTimers();
        });

        function failCurrentStreamAndLetItRetry() {
            act(() => {
                MockEventSource.getLatest()?.simulateError();
            });
            act(() => {
                vi.advanceTimersByTime(BEYOND_MAX_BACKOFF_MS);
            });
        }

        function exhaustRetries() {
            for (let attempt = 0; attempt <= MAX_RETRY_ATTEMPTS; attempt++) {
                failCurrentStreamAndLetItRetry();
            }
        }

        test("closes the stream that failed instead of stranding it", () => {
            render(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" />
                </SSEProvider>
            );

            failCurrentStreamAndLetItRetry();
            failCurrentStreamAndLetItRetry();
            failCurrentStreamAndLetItRetry();

            expect(MockEventSource.instances).toHaveLength(4);

            const superseded = MockEventSource.instances.slice(0, -1);
            const stranded = superseded.filter(es => es.readyState !== CLOSED);
            expect(stranded).toHaveLength(0);
        });

        test("keeps delivering messages after a reconnect", () => {
            const onMessage = vi.fn();
            render(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" onMessage={onMessage} />
                </SSEProvider>
            );

            failCurrentStreamAndLetItRetry();
            act(() => {
                MockEventSource.getLatest()?.simulateOpen();
                MockEventSource.getLatest()?.simulateMessage("after-reconnect");
            });

            expect(onMessage).toHaveBeenCalledWith(expect.objectContaining({ data: "after-reconnect" }));
            expect(onMessage).toHaveBeenCalledTimes(1);
        });

        test("gives up after the maximum number of retries", () => {
            render(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" />
                </SSEProvider>
            );

            for (let attempt = 0; attempt < MAX_RETRY_ATTEMPTS; attempt++) {
                failCurrentStreamAndLetItRetry();
            }
            expect(MockEventSource.instances).toHaveLength(MAX_RETRY_ATTEMPTS + 1);
            const instancesBeforeGivingUp = MockEventSource.instances.length;

            failCurrentStreamAndLetItRetry();

            expect(MockEventSource.instances).toHaveLength(instancesBeforeGivingUp);
            expect(screen.getByTestId("connection-state")).toHaveTextContent("error");
        });

        test("a successful open restores the full retry budget", () => {
            render(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" />
                </SSEProvider>
            );

            for (let attempt = 0; attempt < MAX_RETRY_ATTEMPTS - 1; attempt++) {
                failCurrentStreamAndLetItRetry();
            }
            act(() => {
                MockEventSource.getLatest()?.simulateOpen();
            });
            for (let attempt = 0; attempt < MAX_RETRY_ATTEMPTS - 1; attempt++) {
                failCurrentStreamAndLetItRetry();
            }

            expect(screen.getByTestId("connection-state")).toHaveTextContent("reconnecting");
        });

        test("re-attaches named event listeners after a reconnect", () => {
            const onMessage = vi.fn();
            render(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" eventType="chat_shared" onMessage={onMessage} />
                </SSEProvider>
            );

            failCurrentStreamAndLetItRetry();
            act(() => {
                MockEventSource.getLatest()?.simulateOpen();
                MockEventSource.getLatest()?.simulateNamedEvent("chat_shared", "after-reconnect");
            });

            expect(onMessage).toHaveBeenCalledWith(expect.objectContaining({ data: "after-reconnect" }));
            expect(onMessage).toHaveBeenCalledTimes(1);
        });

        test("one subscriber throwing in onError does not cancel the retry", () => {
            const thrower = vi.fn(() => {
                throw new Error("subscriber blew up");
            });
            render(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" onError={thrower} />
                </SSEProvider>
            );

            failCurrentStreamAndLetItRetry();

            expect(thrower).toHaveBeenCalled();
            expect(MockEventSource.instances).toHaveLength(2);
        });

        test("a subscriber joining after retries are exhausted revives the connection", () => {
            const lateOnMessage = vi.fn();
            const { rerender } = render(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" />
                </SSEProvider>
            );

            exhaustRetries();
            expect(screen.getByTestId("connection-state")).toHaveTextContent("error");
            const deadStream = MockEventSource.getLatest();

            rerender(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" />
                    <TestSubscriber endpoint="http://localhost/sse" onMessage={lateOnMessage} />
                </SSEProvider>
            );

            const revivedStream = MockEventSource.getLatest();
            expect(revivedStream).not.toBe(deadStream);

            act(() => {
                revivedStream?.simulateOpen();
                revivedStream?.simulateMessage("revived");
            });

            for (const state of screen.getAllByTestId("connection-state")) {
                expect(state).toHaveTextContent("connected");
            }
            expect(lateOnMessage).toHaveBeenCalledWith(expect.objectContaining({ data: "revived" }));
        });

        describe.each([
            ["focus", () => globalThis.dispatchEvent(new Event("focus"))],
            ["online", () => globalThis.dispatchEvent(new Event("online"))],
            ["visibilitychange", () => document.dispatchEvent(new Event("visibilitychange"))],
        ])("revives an exhausted connection on %s", (_name, fireEvent) => {
            test("reopens the stream and resumes delivery", () => {
                const onMessage = vi.fn();
                render(
                    <SSEProvider>
                        <TestSubscriber endpoint="http://localhost/sse" eventType="chat_shared" onMessage={onMessage} />
                    </SSEProvider>
                );

                exhaustRetries();
                expect(screen.getByTestId("connection-state")).toHaveTextContent("error");
                const deadStream = MockEventSource.getLatest();

                act(() => {
                    fireEvent();
                });

                const revivedStream = MockEventSource.getLatest();
                expect(revivedStream).not.toBe(deadStream);

                act(() => {
                    revivedStream?.simulateOpen();
                    revivedStream?.simulateNamedEvent("chat_shared", "revived");
                });

                expect(screen.getByTestId("connection-state")).toHaveTextContent("connected");
                expect(onMessage).toHaveBeenCalledWith(expect.objectContaining({ data: "revived" }));
            });
        });

        test("leaves a healthy connection alone when the window regains focus", () => {
            render(
                <SSEProvider>
                    <TestSubscriber endpoint="http://localhost/sse" />
                </SSEProvider>
            );

            act(() => {
                MockEventSource.getLatest()?.simulateOpen();
            });
            const healthyStream = MockEventSource.getLatest();

            act(() => {
                globalThis.dispatchEvent(new Event("focus"));
            });

            expect(MockEventSource.getLatest()).toBe(healthyStream);
            expect(healthyStream?.readyState).toBe(OPEN);
        });
    });
});
