// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";

import type { TaskFE } from "@/types";

import { taskKeys } from "./keys";
import { getRecentTasks } from "./service";
import { isTaskRunning, type TaskListFilters } from "./types";

const DEFAULT_PAGE_SIZE = 100;
const STALE_TIME_MS = 5 * 60 * 1000;
export const RUNNING_TASKS_POLL_MS = 5 * 1000;
export const IDLE_TASKS_POLL_MS = 30 * 1000;

export const tasksPollInterval = (tasks: TaskFE[] | undefined): number => (tasks?.some(isTaskRunning) ? RUNNING_TASKS_POLL_MS : IDLE_TASKS_POLL_MS);

const NO_TASKS: TaskFE[] = [];

interface UseRecentTasksResult {
    tasks: TaskFE[];
    isLoading: boolean;
    isPlaceholderData: boolean;
    error: Error | null;
    refetch: () => Promise<unknown>;
}

export const keepIdenticalList = (oldData: unknown, newData: unknown): unknown => (oldData !== undefined && JSON.stringify(oldData) === JSON.stringify(newData) ? oldData : newData);

interface UseRecentTasksOptions extends TaskListFilters {
    pageSize?: number;
    enabled?: boolean;
    pollWhileOpen?: boolean;
}

export function useRecentTasks({ pageSize = DEFAULT_PAGE_SIZE, enabled = true, pollWhileOpen = false, userId, sessionIds, search }: UseRecentTasksOptions = {}): UseRecentTasksResult {
    const filters: TaskListFilters = { userId, sessionIds, search };
    const query = useQuery({
        queryKey: taskKeys.recent(pageSize, filters),
        queryFn: () => getRecentTasks(pageSize, filters),
        staleTime: STALE_TIME_MS,
        refetchOnMount: "always",
        refetchOnWindowFocus: false,
        refetchInterval: pollWhileOpen ? q => tasksPollInterval(q.state.data) : false,
        structuralSharing: keepIdenticalList,
        retry: false,
        enabled,
        placeholderData: keepPreviousData,
    });

    return {
        tasks: query.data ?? NO_TASKS,
        isLoading: query.isLoading,
        isPlaceholderData: query.isPlaceholderData,
        error: query.error,
        refetch: () => query.refetch(),
    };
}

const uniqueSessionIds = (tasks: TaskFE[]): string[] => [...new Set(tasks.map(task => task.sessionId).filter((id): id is string => !!id))];

const mergeNewestFirst = (sessionTasks: TaskFE[], hits: TaskFE[]): TaskFE[] => {
    const byId = new Map<string, TaskFE>();
    for (const task of [...sessionTasks, ...hits]) {
        if (!byId.has(task.taskId)) byId.set(task.taskId, task);
    }
    return [...byId.values()].sort((a, b) => b.lastUpdated.getTime() - a.lastUpdated.getTime());
};

export function useActivityTasks({ pageSize = DEFAULT_PAGE_SIZE, enabled = true, pollWhileOpen = false, userId, search }: Omit<UseRecentTasksOptions, "sessionIds"> = {}): UseRecentTasksResult {
    const hits = useRecentTasks({ pageSize, enabled, pollWhileOpen, userId, search });
    const landedHits = hits.isPlaceholderData ? NO_TASKS : hits.tasks;
    const hitSessionIds = useMemo(() => (search ? uniqueSessionIds(landedHits) : []), [search, landedHits]);
    const expandSessions = hitSessionIds.length > 0;
    const sessions = useRecentTasks({ pageSize, enabled: enabled && expandSessions, pollWhileOpen, userId, sessionIds: hitSessionIds });
    const landedSessionTasks = sessions.isPlaceholderData ? NO_TASKS : sessions.tasks;

    const tasks = useMemo(() => (expandSessions ? mergeNewestFirst(landedSessionTasks, hits.tasks) : hits.tasks), [expandSessions, landedSessionTasks, hits.tasks]);
    return {
        tasks,
        isLoading: hits.isLoading || (expandSessions && sessions.isLoading),
        isPlaceholderData: hits.isPlaceholderData,
        error: hits.error ?? sessions.error,
        refetch: () => Promise.all([hits.refetch(), expandSessions ? sessions.refetch() : undefined]),
    };
}
