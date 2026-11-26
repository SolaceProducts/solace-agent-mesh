// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { TaskFE } from "@/types";

import { IDLE_TASKS_POLL_MS, keepIdenticalList, RUNNING_TASKS_POLL_MS, tasksPollInterval, useRecentTasks } from "./hooks";
import * as tasksService from "./service";
import { rowToTaskFE } from "./types";

const task = (fields: Partial<TaskFE>): TaskFE => ({ taskId: "t", initialRequestText: "", events: [], firstSeen: new Date(0), lastUpdated: new Date(0), ...fields });

function wrapper(client: QueryClient) {
    return ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("tasksPollInterval", () => {
    it("polls quickly while any listed task runs", () => {
        expect(tasksPollInterval([task({ status: "completed", endTime: "2026-01-01T00:00:05Z" }), task({})])).toBe(RUNNING_TASKS_POLL_MS);
    });

    it("polls slowly when every listed task has ended, so a newly started one still appears", () => {
        expect(tasksPollInterval([task({ status: "completed", endTime: "2026-01-01T00:00:05Z" })])).toBe(IDLE_TASKS_POLL_MS);
        expect(tasksPollInterval([])).toBe(IDLE_TASKS_POLL_MS);
        expect(tasksPollInterval(undefined)).toBe(IDLE_TASKS_POLL_MS);
    });
});

describe("keepIdenticalList", () => {
    it("keeps the old list when a poll brings back the same rows, Date fields included", () => {
        const old = [task({ taskId: "t-1", lastUpdated: new Date(5) })];
        expect(keepIdenticalList(old, [task({ taskId: "t-1", lastUpdated: new Date(5) })])).toBe(old);
        const changed = [task({ taskId: "t-1", lastUpdated: new Date(6) })];
        expect(keepIdenticalList(old, changed)).toBe(changed);
        expect(keepIdenticalList(undefined, changed)).toBe(changed);
    });
});

describe("useRecentTasks", () => {
    afterEach(() => vi.restoreAllMocks());

    it("hands back the same list when a re-read brings back the same rows", async () => {
        const getRecentTasks = vi.spyOn(tasksService, "getRecentTasks").mockImplementation(() => Promise.resolve([rowToTaskFE({ id: "t-1", initialRequestText: "Run", startTime: "2026-01-01T00:00:00Z", lastActivityTime: "2026-01-01T00:00:05Z" })]));
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

        const { result } = renderHook(() => useRecentTasks({ pollWhileOpen: true }), { wrapper: wrapper(client) });
        await waitFor(() => {
            expect(result.current.tasks).toHaveLength(1);
            expect(client.isFetching()).toBe(0);
        });
        const first = result.current.tasks;
        const readsBefore = getRecentTasks.mock.calls.length;
        await act(async () => {
            await result.current.refetch();
        });

        expect(getRecentTasks).toHaveBeenCalledTimes(readsBefore + 1);
        expect(client.getQueryCache().getAll()[0].state.data).toBe(first);
    });
});
