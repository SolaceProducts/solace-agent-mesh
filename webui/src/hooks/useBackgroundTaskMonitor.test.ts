// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { api } from "@/api/client";

import type { BackgroundTaskState } from "@/types/background-tasks";

import { selectTerminationCandidates, useBackgroundTaskMonitor } from "./useBackgroundTaskMonitor";

type ApiResponse = Response | Record<string, unknown>;

function makeFullResponse(body: unknown, ok = true, status = 200): Response {
    return {
        ok,
        status,
        json: async () => body,
    } as unknown as Response;
}

function makeActiveTask(overrides: Partial<{ id: string; sessionId: string | null; startTime: number; lastActivityTime: number | null }> = {}) {
    return {
        id: "task-1",
        userId: "user-1",
        parentTaskId: null,
        startTime: Date.now() - 60_000,
        endTime: null,
        status: "running",
        initialRequestText: null,
        executionMode: "background",
        lastActivityTime: Date.now(),
        backgroundExecutionEnabled: true,
        maxExecutionTimeMs: null,
        sessionId: "session-a",
        ...overrides,
    };
}

function makeStatusResponse(overrides: Partial<{ status: string; endTime: number | null }> = {}) {
    return {
        task: {
            id: "task-1",
            userId: "user-1",
            parentTaskId: null,
            startTime: Date.now() - 60_000,
            endTime: Date.now(),
            status: "completed",
            initialRequestText: null,
            executionMode: "background",
            lastActivityTime: Date.now(),
            backgroundExecutionEnabled: true,
            maxExecutionTimeMs: null,
            sessionId: "session-a",
            ...overrides,
        },
        isRunning: false,
        isBackground: true,
        canReconnect: false,
        errorMessage: null,
    };
}

describe("useBackgroundTaskMonitor", () => {
    let getSpy: ReturnType<typeof vi.spyOn>;
    let baseTime: number;

    beforeEach(() => {
        baseTime = 1_700_000_000_000;
        vi.useFakeTimers({ now: baseTime });
        localStorage.clear();
        getSpy = vi.spyOn(api.webui, "get");
    });

    afterEach(() => {
        getSpy.mockRestore();
        vi.useRealTimers();
        vi.restoreAllMocks();
    });

    async function flushAsync() {
        await act(async () => {
            await vi.advanceTimersByTimeAsync(0);
        });
    }

    async function registerAndSettle(result: { current: ReturnType<typeof useBackgroundTaskMonitor> }, taskId: string, sessionId = "session-a") {
        act(() => {
            result.current.registerBackgroundTask(taskId, sessionId);
        });
        await flushAsync();
    }

    test("single batch call per tick — no /status calls while all tasks are still running", async () => {
        getSpy.mockImplementation(async (endpoint: string) => {
            if (endpoint.includes("/tasks/background/active")) {
                return {
                    data: [makeActiveTask({ id: "task-1" }), makeActiveTask({ id: "task-2" })],
                    meta: { pagination: { pageNumber: 1, pageSize: 2, count: 2, totalPages: 1, nextPage: null } },
                };
            }
            throw new Error(`unexpected endpoint: ${endpoint}`);
        });

        const { result } = renderHook(() => useBackgroundTaskMonitor({ userId: "user-1", currentSessionId: "session-other" }));

        await registerAndSettle(result, "task-1");
        await registerAndSettle(result, "task-2");

        getSpy.mockClear();

        await act(async () => {
            await vi.advanceTimersByTimeAsync(10_000);
        });

        const calls = getSpy.mock.calls.map((c: unknown[]) => String(c[0]));
        const batchCalls = calls.filter((url: string) => url.includes("/tasks/background/active"));
        const statusCalls = calls.filter((url: string) => /\/tasks\/[^/]+\/status/.test(url));

        expect(batchCalls.length).toBe(1);
        expect(statusCalls.length).toBe(0);
    });

    test("no batch fetch while every tracked task is on the current session with fresh SSE", async () => {
        getSpy.mockImplementation(async (endpoint: string) => {
            if (endpoint.includes("/tasks/background/active")) {
                return {
                    data: [makeActiveTask({ id: "task-1" })],
                    meta: { pagination: { pageNumber: 1, pageSize: 1, count: 1, totalPages: 1, nextPage: null } },
                };
            }
            throw new Error(`unexpected endpoint: ${endpoint}`);
        });

        const { result } = renderHook(() => useBackgroundTaskMonitor({ userId: "user-1", currentSessionId: "session-a" }));

        await registerAndSettle(result, "task-1", "session-a");
        getSpy.mockClear();

        for (let i = 0; i < 3; i++) {
            act(() => {
                result.current.updateTaskTimestamp("task-1", Date.now());
            });
            await act(async () => {
                await vi.advanceTimersByTimeAsync(10_000);
            });
        }

        const batchCalls = getSpy.mock.calls.map((c: unknown[]) => String(c[0])).filter((url: string) => url.includes("/tasks/background/active"));
        expect(batchCalls.length).toBe(0);
    });

    test("polling resumes once SSE goes stale on the current session's task", async () => {
        getSpy.mockImplementation(async (endpoint: string) => {
            if (endpoint.includes("/tasks/background/active")) {
                return {
                    data: [makeActiveTask({ id: "task-1" })],
                    meta: { pagination: { pageNumber: 1, pageSize: 1, count: 1, totalPages: 1, nextPage: null } },
                };
            }
            throw new Error(`unexpected endpoint: ${endpoint}`);
        });

        const { result } = renderHook(() => useBackgroundTaskMonitor({ userId: "user-1", currentSessionId: "session-a" }));

        await registerAndSettle(result, "task-1", "session-a");
        getSpy.mockClear();

        await act(async () => {
            await vi.advanceTimersByTimeAsync(70_000);
        });

        const batchCalls = getSpy.mock.calls.map((c: unknown[]) => String(c[0])).filter((url: string) => url.includes("/tasks/background/active"));
        expect(batchCalls.length).toBeGreaterThanOrEqual(1);
    });

    test("task missing from batch response (past grace period) fires onTaskCompleted with session id", async () => {
        const onTaskCompleted = vi.fn();

        let tickCount = 0;
        getSpy.mockImplementation(async (endpoint: string) => {
            if (endpoint.includes("/tasks/background/active")) {
                tickCount += 1;
                if (tickCount === 1) {
                    return {
                        data: [makeActiveTask({ id: "task-1", sessionId: "session-other" })],
                        meta: { pagination: { pageNumber: 1, pageSize: 1, count: 1, totalPages: 1, nextPage: null } },
                    };
                }
                return { data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } };
            }
            if (/\/tasks\/task-1\/status/.test(endpoint)) {
                return makeFullResponse(makeStatusResponse({ status: "completed" })) as unknown as ApiResponse;
            }
            throw new Error(`unexpected endpoint: ${endpoint}`);
        });

        const { result } = renderHook(() => useBackgroundTaskMonitor({ userId: "user-1", currentSessionId: "session-viewing", onTaskCompleted }));

        act(() => {
            result.current.registerBackgroundTask("task-1", "session-other");
        });
        await act(async () => {
            await vi.advanceTimersByTimeAsync(11_000);
        });
        await flushAsync();

        expect(onTaskCompleted).toHaveBeenCalledWith("task-1", "session-other");
        expect(result.current.backgroundTasks.find(t => t.taskId === "task-1")).toBeUndefined();
    });

    test("task on currently-viewed session is not treated as terminated even when missing from batch", async () => {
        const onTaskCompleted = vi.fn();
        const onTaskFailed = vi.fn();

        getSpy.mockImplementation(async (endpoint: string) => {
            if (endpoint.includes("/tasks/background/active")) {
                return { data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } };
            }
            if (/\/tasks\/.+\/status/.test(endpoint)) {
                throw new Error("/status should not be called for tasks on the currently-viewed session");
            }
            throw new Error(`unexpected endpoint: ${endpoint}`);
        });

        const { result } = renderHook(() => useBackgroundTaskMonitor({ userId: "user-1", currentSessionId: "session-viewing", onTaskCompleted, onTaskFailed }));

        act(() => {
            result.current.registerBackgroundTask("task-on-active", "session-viewing");
        });
        await act(async () => {
            await vi.advanceTimersByTimeAsync(10_000);
        });

        expect(onTaskCompleted).not.toHaveBeenCalled();
        expect(onTaskFailed).not.toHaveBeenCalled();
        expect(result.current.backgroundTasks.some(t => t.taskId === "task-on-active")).toBe(true);
    });

    test("task on currently-viewed session is polled once SSE has gone stale", async () => {
        const onTaskCompleted = vi.fn();

        getSpy.mockImplementation(async (endpoint: string) => {
            if (endpoint.includes("/tasks/background/active")) {
                return { data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } };
            }
            if (/\/tasks\/task-stale\/status/.test(endpoint)) {
                return makeFullResponse(makeStatusResponse({ status: "completed" })) as unknown as ApiResponse;
            }
            throw new Error(`unexpected endpoint: ${endpoint}`);
        });

        const { result } = renderHook(() => useBackgroundTaskMonitor({ userId: "user-1", currentSessionId: "session-viewing", onTaskCompleted }));

        act(() => {
            result.current.registerBackgroundTask("task-stale", "session-viewing");
        });
        await act(async () => {
            await vi.advanceTimersByTimeAsync(70_000);
        });
        await flushAsync();

        expect(onTaskCompleted).toHaveBeenCalledWith("task-stale", "session-viewing");
        expect(result.current.backgroundTasks.find(t => t.taskId === "task-stale")).toBeUndefined();
    });

    test("task missing from batch but /status reports running is NOT treated as complete", async () => {
        const onTaskCompleted = vi.fn();
        const onTaskFailed = vi.fn();

        getSpy.mockImplementation(async (endpoint: string) => {
            if (endpoint.includes("/tasks/background/active")) {
                return { data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } };
            }
            if (/\/tasks\/task-1\/status/.test(endpoint)) {
                return makeFullResponse({
                    task: {
                        id: "task-1",
                        userId: "user-1",
                        parentTaskId: null,
                        startTime: new Date(Date.now() - 60_000).toISOString(),
                        endTime: null,
                        status: "running",
                        initialRequestText: null,
                        executionMode: "background",
                        lastActivityTime: new Date(Date.now()).toISOString(),
                        backgroundExecutionEnabled: true,
                        maxExecutionTimeMs: null,
                        sessionId: "session-other",
                    },
                    isRunning: true,
                    isBackground: true,
                    canReconnect: true,
                    errorMessage: null,
                }) as unknown as ApiResponse;
            }
            throw new Error(`unexpected endpoint: ${endpoint}`);
        });

        const { result } = renderHook(() => useBackgroundTaskMonitor({ userId: "user-1", currentSessionId: "session-viewing", onTaskCompleted, onTaskFailed }));

        act(() => {
            result.current.registerBackgroundTask("task-1", "session-other");
        });
        await act(async () => {
            await vi.advanceTimersByTimeAsync(11_000);
        });

        expect(onTaskCompleted).not.toHaveBeenCalled();
        expect(onTaskFailed).not.toHaveBeenCalled();
        expect(result.current.backgroundTasks.some(t => t.taskId === "task-1")).toBe(true);
    });

    test("server-reported failed task calls onTaskFailed with the error message", async () => {
        const onTaskCompleted = vi.fn();
        const onTaskFailed = vi.fn();

        getSpy.mockImplementation(async (endpoint: string) => {
            if (endpoint.includes("/tasks/background/active")) {
                return { data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } };
            }
            if (/\/tasks\/task-1\/status/.test(endpoint)) {
                return makeFullResponse({ ...makeStatusResponse({ status: "failed" }), errorMessage: "boom" }) as unknown as ApiResponse;
            }
            throw new Error(`unexpected endpoint: ${endpoint}`);
        });

        const { result } = renderHook(() => useBackgroundTaskMonitor({ userId: "user-1", currentSessionId: "session-viewing", onTaskCompleted, onTaskFailed }));
        act(() => {
            result.current.registerBackgroundTask("task-1", "session-other");
        });
        await act(async () => {
            await vi.advanceTimersByTimeAsync(11_000);
        });
        await flushAsync();

        expect(onTaskCompleted).not.toHaveBeenCalled();
        expect(onTaskFailed).toHaveBeenCalledWith("task-1", "boom", "session-other");
    });

    test("server-reported timeout calls onTaskFailed with the timeout message", async () => {
        const onTaskFailed = vi.fn();

        getSpy.mockImplementation(async (endpoint: string) => {
            if (endpoint.includes("/tasks/background/active")) {
                return { data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } };
            }
            if (/\/tasks\/task-1\/status/.test(endpoint)) {
                return makeFullResponse(makeStatusResponse({ status: "timeout" })) as unknown as ApiResponse;
            }
            throw new Error(`unexpected endpoint: ${endpoint}`);
        });

        const { result } = renderHook(() => useBackgroundTaskMonitor({ userId: "user-1", currentSessionId: "session-viewing", onTaskFailed }));
        act(() => {
            result.current.registerBackgroundTask("task-1", "session-other");
        });
        await act(async () => {
            await vi.advanceTimersByTimeAsync(11_000);
        });
        await flushAsync();

        expect(onTaskFailed).toHaveBeenCalledTimes(1);
        expect(onTaskFailed).toHaveBeenCalledWith("task-1", "Background task timed out", "session-other");
    });

    test("server-reported completed task NEVER calls onTaskFailed (server status is authoritative)", async () => {
        const onTaskCompleted = vi.fn();
        const onTaskFailed = vi.fn();

        getSpy.mockImplementation(async (endpoint: string) => {
            if (endpoint.includes("/tasks/background/active")) {
                return { data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } };
            }
            if (/\/tasks\/task-1\/status/.test(endpoint)) {
                return makeFullResponse(makeStatusResponse({ status: "completed" })) as unknown as ApiResponse;
            }
            throw new Error(`unexpected endpoint: ${endpoint}`);
        });

        const { result } = renderHook(() => useBackgroundTaskMonitor({ userId: "user-1", currentSessionId: "session-viewing", onTaskCompleted, onTaskFailed }));
        act(() => {
            result.current.registerBackgroundTask("task-1", "session-other");
        });
        await act(async () => {
            await vi.advanceTimersByTimeAsync(11_000);
        });
        await flushAsync();

        expect(onTaskFailed).not.toHaveBeenCalled();
        expect(onTaskCompleted).toHaveBeenCalledWith("task-1", "session-other");
    });

    describe("selectTerminationCandidates (pure function)", () => {
        const makeTrackedTask = (overrides: Partial<BackgroundTaskState> = {}): BackgroundTaskState => ({
            taskId: "task-1",
            sessionId: "session-other",
            lastEventTimestamp: 0,
            isBackground: true,
            startTime: 0,
            registeredAt: 0,
            ...overrides,
        });

        test("task within registration grace period is NOT returned", () => {
            const now = 2_000;
            const tracked = [makeTrackedTask({ taskId: "fresh", registeredAt: 0 })];
            const result = selectTerminationCandidates(tracked, new Set(), "active-session", now);
            expect(result).toHaveLength(0);
        });

        test("task past registration grace period AND missing from batch is returned", () => {
            const now = 10_000;
            const tracked = [makeTrackedTask({ taskId: "old", registeredAt: 0 })];
            const result = selectTerminationCandidates(tracked, new Set(), "active-session", now);
            expect(result.map(t => t.taskId)).toEqual(["old"]);
        });

        test("task present in the server's active set is NOT returned", () => {
            const now = 10_000;
            const tracked = [makeTrackedTask({ taskId: "in-batch", registeredAt: 0 })];
            const activeTaskIds = new Set(["in-batch"]);
            const result = selectTerminationCandidates(tracked, activeTaskIds, "active-session", now);
            expect(result).toHaveLength(0);
        });

        test("task on currently-viewed session with fresh SSE is NOT returned", () => {
            const now = 30_000;
            const tracked = [makeTrackedTask({ taskId: "same", sessionId: "active", registeredAt: 0, lastEventTimestamp: now - 5_000 })];
            const result = selectTerminationCandidates(tracked, new Set(), "active", now);
            expect(result).toHaveLength(0);
        });

        test("task on currently-viewed session with stale SSE IS returned", () => {
            const now = 120_000;
            const tracked = [makeTrackedTask({ taskId: "stale", sessionId: "active", registeredAt: 0, lastEventTimestamp: 0 })];
            const result = selectTerminationCandidates(tracked, new Set(), "active", now);
            expect(result.map(t => t.taskId)).toEqual(["stale"]);
        });
    });

    test("network error on batch request does NOT mark tasks complete", async () => {
        const onTaskCompleted = vi.fn();
        const onTaskFailed = vi.fn();

        let callIndex = 0;
        getSpy.mockImplementation(async (endpoint: string) => {
            if (endpoint.includes("/tasks/background/active")) {
                callIndex += 1;
                if (callIndex === 1) {
                    return { data: [], meta: { pagination: { pageNumber: 1, pageSize: 0, count: 0, totalPages: 1, nextPage: null } } };
                }
                throw new Error("network down");
            }
            if (/\/tasks\/.+\/status/.test(endpoint)) {
                throw new Error("status should not be called on batch failure");
            }
            throw new Error(`unexpected endpoint: ${endpoint}`);
        });

        const { result } = renderHook(() => useBackgroundTaskMonitor({ userId: "user-1", currentSessionId: "session-viewing", onTaskCompleted, onTaskFailed }));

        act(() => {
            result.current.registerBackgroundTask("task-1", "session-other");
        });
        await act(async () => {
            await vi.advanceTimersByTimeAsync(11_000);
        });

        expect(onTaskCompleted).not.toHaveBeenCalled();
        expect(onTaskFailed).not.toHaveBeenCalled();
        expect(result.current.backgroundTasks.some(t => t.taskId === "task-1")).toBe(true);
    });

    test("server-fetched tasks on mount preserve sessionId", async () => {
        getSpy.mockImplementation(async (endpoint: string) => {
            if (endpoint.includes("/tasks/background/active")) {
                return {
                    data: [makeActiveTask({ id: "task-from-server", sessionId: "session-zzz" })],
                    meta: { pagination: { pageNumber: 1, pageSize: 1, count: 1, totalPages: 1, nextPage: null } },
                };
            }
            throw new Error(`unexpected endpoint: ${endpoint}`);
        });

        const { result } = renderHook(() => useBackgroundTaskMonitor({ userId: "user-1", currentSessionId: "session-a" }));

        await flushAsync();

        expect(result.current.backgroundTasks.length).toBeGreaterThan(0);
        const serverTask = result.current.backgroundTasks.find(t => t.taskId === "task-from-server");
        expect(serverTask).toBeDefined();
        expect(serverTask!.sessionId).toBe("session-zzz");

        expect(result.current.getSessionBackgroundTasks("session-zzz")).toHaveLength(1);
    });
});
