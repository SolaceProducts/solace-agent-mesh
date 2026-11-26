// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const scheduledTaskKeys = {
    all: ["scheduled-tasks"] as const,
    lists: () => [...scheduledTaskKeys.all, "list"] as const,
    list: (filters?: { pageNumber?: number; pageSize?: number; enabledOnly?: boolean; ownership?: string }) => [...scheduledTaskKeys.lists(), { filters }] as const,
    details: () => [...scheduledTaskKeys.all, "detail"] as const,
    detail: (id: string) => [...scheduledTaskKeys.details(), id] as const,
    executions: (taskId: string) => [...scheduledTaskKeys.detail(taskId), "executions"] as const,
    executionList: (taskId: string, filters?: { pageNumber?: number; pageSize?: number }) => [...scheduledTaskKeys.executions(taskId), { filters }] as const,
    execution: (executionId: string) => [...scheduledTaskKeys.all, "execution", executionId] as const,
    recentExecutions: (pageSize: number) => [...scheduledTaskKeys.all, "recent-executions", pageSize] as const,
    executionArtifacts: (sessionId: string) => [...scheduledTaskKeys.all, "execution-artifacts", sessionId] as const,
    schedulerStatus: () => [...scheduledTaskKeys.all, "scheduler-status"] as const,
};
