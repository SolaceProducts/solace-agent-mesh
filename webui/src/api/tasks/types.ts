// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { TaskFE, TaskSubject, TaskTrigger } from "@/types";

export interface PersistedTaskRow {
    id: string;
    userId?: string;
    subject?: TaskSubject;
    trigger?: TaskTrigger;
    initialRequestText?: string;
    startTime?: string;
    lastActivityTime?: string;
    endTime?: string;
    parentTaskId?: string;
    sessionId?: string;
    sessionName?: string;
    status?: string;
    eventCount?: number;
    totalInputTokens?: number;
    totalOutputTokens?: number;
}

export interface TaskListFilters {
    userId?: string;
    sessionIds?: string[];
    search?: string;
}

export const recentTasksPath = (pageSize: number, filters: TaskListFilters = {}): string => {
    const params = new URLSearchParams({ pageSize: String(pageSize), sort: "lastActivityTime:desc", kind: "task", expand: "sessionName" });
    if (filters.userId) params.set("userId", filters.userId);
    if (filters.sessionIds?.length) params.set("sessionIds", filters.sessionIds.join(","));
    if (filters.search) params.set("search", filters.search);
    return `/api/v1/tasks?${params.toString()}`;
};

const gatewayTimestampPattern = /^Request received by gateway at:[^\n]*\n?/;

export const stripGatewayTimestamp = (text: string): string => text.replace(gatewayTimestampPattern, "").trim();

export const rowToTaskFE = (row: PersistedTaskRow): TaskFE => {
    const startTs = row.startTime ? new Date(row.startTime) : new Date();
    const lastTs = row.lastActivityTime ? new Date(row.lastActivityTime) : startTs;
    return {
        taskId: row.id,
        userId: row.userId,
        subject: row.subject,
        trigger: row.trigger,
        initialRequestText: stripGatewayTimestamp(row.initialRequestText ?? ""),
        events: [],
        firstSeen: startTs,
        lastUpdated: lastTs,
        parentTaskId: row.parentTaskId ?? null,
        sessionId: row.sessionId,
        sessionName: row.sessionName,
        status: row.status,
        endTime: row.endTime,
        eventCount: row.eventCount,
        inputTokens: row.totalInputTokens,
        outputTokens: row.totalOutputTokens,
        needsHistoricalHydration: true,
    };
};

const terminalTaskStatuses = new Set(["completed", "failed", "canceled", "rejected", "timeout"]);

export const isTaskRunning = (task?: { status?: string; endTime?: string }): boolean => !!task && !task.endTime && !terminalTaskStatuses.has(task.status ?? "");
