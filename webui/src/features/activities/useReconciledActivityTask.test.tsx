// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, test, vi } from "vitest";

import { TaskContext, type TaskContextValue } from "@/providers/contexts/TaskContext";

import type { A2AEventSSEPayload, TaskFE } from "@/types";

import { useReconciledActivityTask } from "./useReconciledActivityTask";

const TASK_ID = "019e8e57-c851-73ad-af71-cf26e05597dd";

const NO_LIVE: Record<string, TaskFE> = {};

function ev(direction: string, timestamp: string, source = "AgentA"): A2AEventSSEPayload {
    return {
        event_type: "a2a_message",
        timestamp,
        solace_topic: `ns/a2a/v1/gateway/status/gw/${TASK_ID}`,
        direction,
        source_entity: source,
        target_entity: "unknown",
        task_id: TASK_ID,
        payload_summary: {},
        full_payload: { result: { status: { state: direction === "task" ? "completed" : "working" } } },
    };
}

function task(events: A2AEventSSEPayload[], extra: Partial<TaskFE> = {}): TaskFE {
    return {
        taskId: TASK_ID,
        initialRequestText: "req",
        events,
        firstSeen: new Date("2026-06-03T16:36:18.000Z"),
        lastUpdated: new Date(events[events.length - 1]?.timestamp ?? "2026-06-03T16:36:18.000Z"),
        ...extra,
    };
}

function wrapper(ctx: Partial<TaskContextValue>) {
    const value: TaskContextValue = {
        monitoredTasks: {},
        monitoredTaskOrder: [],
        unavailableTaskIds: new Set<string>(),
        failedTaskIds: new Set<string>(),
        highlightedStepId: null,
        setHighlightedStepId: () => {},
        loadTaskFromBackend: async () => null,
        loadRecentTasks: async () => {},
        registerTaskEarly: () => {},
        ...ctx,
    };
    return ({ children }: { children: React.ReactNode }) => <TaskContext.Provider value={value}>{children}</TaskContext.Provider>;
}

describe("useReconciledActivityTask", () => {
    test("returns no visualization and skips fetch when taskId is null", () => {
        const loadTaskFromBackend = vi.fn().mockResolvedValue(null);
        const { result } = renderHook(() => useReconciledActivityTask(null, NO_LIVE), { wrapper: wrapper({ loadTaskFromBackend }) });
        expect(result.current.visualizedTask).toBeNull();
        expect(loadTaskFromBackend).not.toHaveBeenCalled();
    });

    test("renders from the live stream and reconciles against the backfill once", async () => {
        const loadTaskFromBackend = vi.fn().mockResolvedValue(null);
        const live = { [TASK_ID]: task([ev("status_update", "2026-06-03T16:36:18.100Z")]) };
        const { result } = renderHook(() => useReconciledActivityTask(TASK_ID, live), { wrapper: wrapper({ loadTaskFromBackend }) });

        await waitFor(() => expect(result.current.visualizedTask).not.toBeNull());
        await waitFor(() => expect(loadTaskFromBackend).toHaveBeenCalledWith(TASK_ID));
    });

    test("merges live and backfill events so a frame missing from one source survives", async () => {
        const loadTaskFromBackend = vi.fn().mockResolvedValue(null);
        const backfill = task([ev("request", "2026-06-03T16:36:18.000Z")], { needsHistoricalHydration: false });
        const live = { [TASK_ID]: task([ev("status_update", "2026-06-03T16:36:18.200Z")]) };
        const { result } = renderHook(() => useReconciledActivityTask(TASK_ID, live), {
            wrapper: wrapper({ monitoredTasks: { [TASK_ID]: backfill }, loadTaskFromBackend }),
        });

        await waitFor(() => expect(result.current.visualizedTask).not.toBeNull());
        const steps = result.current.visualizedTask?.steps ?? [];
        expect(steps.length).toBeGreaterThan(0);
    });

    test("reload triggers a fresh backfill fetch", async () => {
        const loadTaskFromBackend = vi.fn().mockResolvedValue(null);
        const backfill = task([ev("request", "2026-06-03T16:36:18.000Z")], { needsHistoricalHydration: false });
        const { result } = renderHook(() => useReconciledActivityTask(TASK_ID, NO_LIVE), {
            wrapper: wrapper({ monitoredTasks: { [TASK_ID]: backfill }, loadTaskFromBackend }),
        });

        await waitFor(() => expect(loadTaskFromBackend).toHaveBeenCalledWith(TASK_ID));
        loadTaskFromBackend.mockClear();
        act(() => result.current.reload());
        await waitFor(() => expect(loadTaskFromBackend).toHaveBeenCalledWith(TASK_ID));
    });

    test("re-hydrates a hydrated-but-incomplete backfill until it carries a terminal frame", async () => {
        const incomplete = task([ev("request", "2026-06-03T16:36:18.000Z"), ev("status_update", "2026-06-03T16:36:18.100Z")], { needsHistoricalHydration: false });
        const loadTaskFromBackend = vi.fn().mockResolvedValue(null);
        renderHook(() => useReconciledActivityTask(TASK_ID, NO_LIVE), {
            wrapper: wrapper({ monitoredTasks: { [TASK_ID]: incomplete }, loadTaskFromBackend }),
        });

        await waitFor(() => expect(loadTaskFromBackend.mock.calls.length).toBeGreaterThanOrEqual(2), { timeout: 4000 });
    });

    test("does not re-hydrate once the backfill carries a terminal frame", async () => {
        const complete = task([ev("request", "2026-06-03T16:36:18.000Z"), ev("task", "2026-06-03T16:36:19.000Z")], { needsHistoricalHydration: false });
        const loadTaskFromBackend = vi.fn().mockResolvedValue(null);
        renderHook(() => useReconciledActivityTask(TASK_ID, NO_LIVE), {
            wrapper: wrapper({ monitoredTasks: { [TASK_ID]: complete }, loadTaskFromBackend }),
        });

        await waitFor(() => expect(loadTaskFromBackend).toHaveBeenCalledWith(TASK_ID));
        const callsAfterInitial = loadTaskFromBackend.mock.calls.length;
        await new Promise(resolve => setTimeout(resolve, 1500));
        expect(loadTaskFromBackend.mock.calls.length).toBe(callsAfterInitial);
    });

    test("does not re-hydrate while the task is still streaming live, because every live task would otherwise re-walk the full-tree events endpoint", async () => {
        const liveInProgress = task([ev("request", "2026-06-03T16:36:18.000Z"), ev("status_update", "2026-06-03T16:36:18.100Z")]);
        const incompleteBackfill = task([ev("request", "2026-06-03T16:36:18.000Z")], { needsHistoricalHydration: false });
        const loadTaskFromBackend = vi.fn().mockResolvedValue(null);
        const live = { [TASK_ID]: liveInProgress };
        renderHook(() => useReconciledActivityTask(TASK_ID, live), {
            wrapper: wrapper({ monitoredTasks: { [TASK_ID]: incompleteBackfill }, loadTaskFromBackend }),
        });

        await waitFor(() => expect(loadTaskFromBackend).toHaveBeenCalledWith(TASK_ID));
        const callsAfterInitial = loadTaskFromBackend.mock.calls.length;
        await new Promise(resolve => setTimeout(resolve, 1500));
        expect(loadTaskFromBackend.mock.calls.length).toBe(callsAfterInitial);
    });

    test("does not re-hydrate an active task before its first live frame arrives", async () => {
        const incompleteBackfill = task([ev("request", "2026-06-03T16:36:18.000Z")], { needsHistoricalHydration: false });
        const loadTaskFromBackend = vi.fn().mockResolvedValue(null);
        renderHook(() => useReconciledActivityTask(TASK_ID, NO_LIVE, true), {
            wrapper: wrapper({ monitoredTasks: { [TASK_ID]: incompleteBackfill }, loadTaskFromBackend }),
        });

        await waitFor(() => expect(loadTaskFromBackend).toHaveBeenCalledWith(TASK_ID));
        const callsAfterInitial = loadTaskFromBackend.mock.calls.length;
        await new Promise(resolve => setTimeout(resolve, 1500));
        expect(loadTaskFromBackend.mock.calls.length).toBe(callsAfterInitial);
    });

    test("resumes re-hydration when the task deactivates with an incomplete backfill", async () => {
        const incompleteBackfill = task([ev("request", "2026-06-03T16:36:18.000Z")], { needsHistoricalHydration: false });
        const loadTaskFromBackend = vi.fn().mockResolvedValue(null);
        const { rerender } = renderHook(({ active }: { active: boolean }) => useReconciledActivityTask(TASK_ID, NO_LIVE, active), {
            initialProps: { active: true },
            wrapper: wrapper({ monitoredTasks: { [TASK_ID]: incompleteBackfill }, loadTaskFromBackend }),
        });

        await waitFor(() => expect(loadTaskFromBackend).toHaveBeenCalledWith(TASK_ID));
        rerender({ active: false });
        await waitFor(() => expect(loadTaskFromBackend.mock.calls.length).toBeGreaterThanOrEqual(2), { timeout: 4000 });
    });

    test("re-hydrates an incomplete backfill once the live stream shows the task finished", async () => {
        const liveDone = task([ev("request", "2026-06-03T16:36:18.000Z"), ev("task", "2026-06-03T16:36:19.000Z")]);
        const incompleteBackfill = task([ev("request", "2026-06-03T16:36:18.000Z")], { needsHistoricalHydration: false });
        const loadTaskFromBackend = vi.fn().mockResolvedValue(null);
        const live = { [TASK_ID]: liveDone };
        renderHook(() => useReconciledActivityTask(TASK_ID, live), {
            wrapper: wrapper({ monitoredTasks: { [TASK_ID]: incompleteBackfill }, loadTaskFromBackend }),
        });

        await waitFor(() => expect(loadTaskFromBackend.mock.calls.length).toBeGreaterThanOrEqual(2), { timeout: 4000 });
    });
});
