// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "@/api/client";
import type { PaginatedResponse } from "@/api/common";

import type { CreateScheduledTaskRequest, ExecutionListResponse, ScheduledTask, ScheduledTaskListResponse, SchedulerStatus, TaskExecution, UpdateScheduledTaskRequest } from "@/types/scheduled-tasks";
import { fillScheduledTaskDerived } from "@/types/scheduled-tasks";

import type { ArtifactInfo } from "@/types";

export const fetchExecutionArtifacts = async (sessionId: string): Promise<ArtifactInfo[]> => {
    const resp = await api.webui.get<PaginatedResponse<ArtifactInfo>>(`/api/v1/artifacts/${sessionId}`);
    return resp?.data ?? [];
};

const EMPTY_TASKS: ScheduledTaskListResponse = {
    data: [],
    meta: { pagination: { pageNumber: 1, count: 0, pageSize: 20, nextPage: null, totalPages: 1 } },
};

export const fetchTasks = async (pageNumber: number = 1, pageSize: number = 20, enabledOnly: boolean = false, ownership: "mine" | "namespace" | "all" = "all"): Promise<ScheduledTaskListResponse> => {
    const params = new URLSearchParams({
        pageNumber: pageNumber.toString(),
        pageSize: pageSize.toString(),
        enabledOnly: enabledOnly.toString(),
        ownership,
    });

    const response = await api.webui.get(`/api/v1/scheduledTasks/?${params.toString()}`, { fullResponse: true });
    if (response.status === 404 || response.status === 501) {
        return EMPTY_TASKS;
    }
    if (!response.ok) {
        throw new Error(`Failed to fetch scheduled tasks (${response.status})`);
    }
    const data: ScheduledTaskListResponse = await response.json();
    return {
        ...data,
        data: data.data.map(fillScheduledTaskDerived),
    };
};

export const fetchTask = async (taskId: string): Promise<ScheduledTask> => {
    const data = await api.webui.get(`/api/v1/scheduledTasks/${taskId}`);
    return fillScheduledTaskDerived(data);
};

export const createTask = async (taskData: CreateScheduledTaskRequest): Promise<ScheduledTask> => {
    const data = await api.webui.post(`/api/v1/scheduledTasks/`, taskData);
    return fillScheduledTaskDerived(data);
};

export const updateTask = async (taskId: string, updates: UpdateScheduledTaskRequest): Promise<ScheduledTask> => {
    const data = await api.webui.patch(`/api/v1/scheduledTasks/${taskId}`, updates);
    return fillScheduledTaskDerived(data);
};

export const deleteTask = async (taskId: string): Promise<void> => {
    await api.webui.delete(`/api/v1/scheduledTasks/${taskId}`);
};

export const enableTask = async (taskId: string): Promise<void> => {
    await api.webui.post(`/api/v1/scheduledTasks/${taskId}/enable`);
};

export const disableTask = async (taskId: string): Promise<void> => {
    await api.webui.post(`/api/v1/scheduledTasks/${taskId}/disable`);
};

export const runTaskNow = async (taskId: string): Promise<void> => {
    await api.webui.post(`/api/v1/scheduledTasks/${taskId}/run`);
};

export const fetchExecutions = async (taskId: string, pageNumber: number = 1, pageSize: number = 20, startDate?: string | null, endDate?: string | null): Promise<ExecutionListResponse> => {
    const params = new URLSearchParams({
        pageNumber: pageNumber.toString(),
        pageSize: pageSize.toString(),
    });
    if (startDate) params.set("startDate", startDate);
    if (endDate) params.set("endDate", endDate);

    return api.webui.get(`/api/v1/scheduledTasks/${taskId}/executions?${params.toString()}`) as Promise<ExecutionListResponse>;
};

export const fetchExecution = async (executionId: string): Promise<TaskExecution> => {
    return api.webui.get(`/api/v1/scheduledTasks/executions/${executionId}`) as Promise<TaskExecution>;
};

export const deleteExecution = async (executionId: string): Promise<void> => {
    await api.webui.delete(`/api/v1/scheduledTasks/executions/${executionId}`);
};

export const fetchRecentExecutions = async (pageSize: number = 50): Promise<ExecutionListResponse> => {
    return api.webui.get(`/api/v1/scheduledTasks/executions/recent?pageSize=${pageSize}`) as Promise<ExecutionListResponse>;
};

export interface ConflictValidationRequest {
    instructions: string;
    scheduleType: string;
    scheduleExpression: string;
    timezone: string;
    targetAgent?: string | null;
}

export interface ConflictValidationResult {
    conflict: boolean;
    reason: string | null;
    affectedFields: Array<"instructions" | "schedule">;
}

export const validateTaskConflict = async (input: ConflictValidationRequest, signal?: AbortSignal): Promise<ConflictValidationResult> => {
    const data = await api.webui.post(
        `/api/v1/scheduledTasks/builder/validate-conflict`,
        {
            instructions: input.instructions,
            scheduleType: input.scheduleType,
            scheduleExpression: input.scheduleExpression,
            timezone: input.timezone,
            targetAgent: input.targetAgent ?? null,
        },
        signal ? { signal } : undefined
    );
    return {
        conflict: !!data.conflict,
        reason: data.reason ?? null,
        affectedFields: Array.isArray(data.affectedFields) ? data.affectedFields.filter((f: string) => f === "instructions" || f === "schedule") : [],
    };
};

export const fetchSchedulerStatus = async (): Promise<SchedulerStatus> => {
    return api.webui.get(`/api/v1/scheduledTasks/scheduler/status`) as Promise<SchedulerStatus>;
};
