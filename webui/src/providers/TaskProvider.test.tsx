// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/client";

import { useTaskContext } from "@/providers/contexts/TaskContext";
import { TaskProvider } from "@/providers/TaskProvider";

import type { TaskFE } from "@/types";

const wrapper = ({ children }: { children: React.ReactNode }) => <TaskProvider>{children}</TaskProvider>;

const eventsUrl = (taskId: string) => `/api/v1/tasks/${taskId}/events`;

function eventsCallCount(spy: ReturnType<typeof vi.spyOn>, taskId: string): number {
    return spy.mock.calls.filter((call: unknown[]) => call[0] === eventsUrl(taskId)).length;
}

describe("TaskProvider.loadTaskFromBackend", () => {
    let getSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        getSpy = vi.spyOn(api.webui, "get").mockResolvedValue({ data: [] } as unknown as Response);
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    it("coalesces concurrent loads of the same task into one request", async () => {
        let resolveEvents!: (value: unknown) => void;
        getSpy.mockImplementation((url: unknown) => {
            if (typeof url === "string" && url.endsWith("/events")) {
                return new Promise(resolve => {
                    resolveEvents = resolve;
                });
            }
            return Promise.resolve({ data: [] });
        });

        const { result } = renderHook(() => useTaskContext(), { wrapper });

        let first!: Promise<TaskFE | null>;
        let second!: Promise<TaskFE | null>;
        act(() => {
            first = result.current.loadTaskFromBackend("task-1");
            second = result.current.loadTaskFromBackend("task-1");
        });

        expect(eventsCallCount(getSpy, "task-1")).toBe(1);

        resolveEvents({ data: [{ taskId: "task-1", events: [], initialRequestText: "hello" }] });
        const [a, b] = await act(() => Promise.all([first, second]));
        expect(a?.taskId).toBe("task-1");
        expect(b).toBe(a);

        getSpy.mockResolvedValue({ data: [] } as unknown as Response);
        await act(async () => {
            await result.current.loadTaskFromBackend("task-1");
        });
        expect(eventsCallCount(getSpy, "task-1")).toBe(2);
    });

    it("hydrates every task in the response tree and clears needsHistoricalHydration", async () => {
        getSpy.mockImplementation((url: unknown) => {
            if (typeof url === "string" && url.endsWith("/events")) {
                return Promise.resolve({
                    data: [
                        {
                            taskId: "parent-task",
                            events: [{ timestamp: "2026-01-01T00:00:00.000Z" }, { timestamp: "2026-01-01T00:00:05.000Z" }],
                            initialRequestText: "summarize the doc",
                            sessionId: "sess-1",
                        },
                        { taskId: "sub-task", events: [] },
                        { events: [] },
                    ],
                });
            }
            return Promise.resolve({ data: [] });
        });

        const { result } = renderHook(() => useTaskContext(), { wrapper });

        const loaded = await act(() => result.current.loadTaskFromBackend("parent-task"));

        expect(loaded?.taskId).toBe("parent-task");
        expect(loaded?.initialRequestText).toBe("summarize the doc");
        expect(loaded?.sessionId).toBe("sess-1");
        expect(loaded?.events).toHaveLength(2);

        await waitFor(() => {
            expect(result.current.monitoredTasks["parent-task"]?.needsHistoricalHydration).toBe(false);
            expect(result.current.monitoredTasks["sub-task"]?.initialRequestText).toBe("Task loaded from history");
            expect(result.current.monitoredTaskOrder).toContain("parent-task");
        });
    });

    it("skips the backend entirely for manual-compaction pseudo-tasks", async () => {
        const { result } = renderHook(() => useTaskContext(), { wrapper });

        const loaded = await act(() => result.current.loadTaskFromBackend("manual-compaction-abc"));

        expect(loaded).toBeNull();
        expect(eventsCallCount(getSpy, "manual-compaction-abc")).toBe(0);
    });

    it("returns null on fetch failure and releases the in-flight slot for retries", async () => {
        getSpy.mockImplementation((url: unknown) => {
            if (typeof url === "string" && url.endsWith("/events")) {
                return Promise.reject(new Error("backend down"));
            }
            return Promise.resolve({ data: [] });
        });

        const { result } = renderHook(() => useTaskContext(), { wrapper });

        const loaded = await act(() => result.current.loadTaskFromBackend("task-err"));
        expect(loaded).toBeNull();
        expect(result.current.failedTaskIds.has("task-err")).toBe(true);
        expect(result.current.unavailableTaskIds.has("task-err")).toBe(false);

        await act(async () => {
            await result.current.loadTaskFromBackend("task-err");
        });
        expect(eventsCallCount(getSpy, "task-err")).toBe(2);
    });

    it("marks a refused load unavailable and clears it when the next load starts", async () => {
        let fail = true;
        getSpy.mockImplementation((url: unknown) => {
            if (typeof url === "string" && url.endsWith("/events")) {
                return fail ? Promise.reject(Object.assign(new Error("task not found"), { status: 404 })) : Promise.resolve({ data: [{ taskId: "task-flaky", events: [] }] });
            }
            return Promise.resolve({ data: [] });
        });

        const { result } = renderHook(() => useTaskContext(), { wrapper });

        await act(() => result.current.loadTaskFromBackend("task-flaky"));
        expect(result.current.unavailableTaskIds.has("task-flaky")).toBe(true);
        expect(result.current.failedTaskIds.has("task-flaky")).toBe(false);

        fail = false;
        await act(() => result.current.loadTaskFromBackend("task-flaky"));
        expect(result.current.unavailableTaskIds.has("task-flaky")).toBe(false);
    });
});
