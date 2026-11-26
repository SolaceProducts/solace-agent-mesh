// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export interface BackgroundTaskState {
    taskId: string;
    sessionId: string;
    lastEventTimestamp: number;
    isBackground: boolean;
    startTime: number;
    registeredAt: number;
    agentName?: string;
}

export interface BackgroundTaskStatusResponse {
    task: {
        id: string;
        userId: string;
        parentTaskId: string | null;
        startTime: string;
        endTime: string | null;
        status: string | null;
        initialRequestText: string | null;
        executionMode: string | null;
        sessionId: string | null;
    };
    isRunning: boolean;
    isBackground: boolean;
    canReconnect: boolean;
    errorMessage: string | null;
}

export interface BackgroundTaskEvent {
    id: string;
    task_id: string;
    user_id: string | null;
    created_time: number;
    topic: string;
    direction: string;
    payload: Record<string, unknown>;
}

export interface BackgroundTaskEventsResponse {
    task: BackgroundTaskStatusResponse["task"];
    events: BackgroundTaskEvent[];
    totalEvents: number;
    hasMore: boolean;
}

export interface ActiveBackgroundTasksResponse {
    data: BackgroundTaskStatusResponse["task"][];
    meta: {
        pagination: {
            pageNumber: number;
            pageSize: number;
            count: number;
            totalPages: number;
            nextPage: number | null;
        };
    };
}

export interface BackgroundTaskNotification {
    taskId: string;
    type: "completed" | "failed" | "timeout";
    message: string;
    timestamp: number;
}
