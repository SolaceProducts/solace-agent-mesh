// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import type { CreateScheduledTaskRequest, TaskExecution, UpdateScheduledTaskRequest } from "@/types/scheduled-tasks";
import { IN_PROGRESS_STATUSES } from "@/types/scheduled-tasks";

import { scheduledTaskKeys } from "./keys";
import type { ConflictValidationRequest, ConflictValidationResult } from "./service";
import * as scheduledTaskService from "./service";

export function useScheduledTasks(pageNumber: number = 1, pageSize: number = 100, enabledOnly: boolean = false, ownership: "mine" | "namespace" | "all" = "all") {
    return useQuery({
        queryKey: scheduledTaskKeys.list({ pageNumber, pageSize, enabledOnly, ownership }),
        queryFn: () => scheduledTaskService.fetchTasks(pageNumber, pageSize, enabledOnly, ownership),
        refetchOnMount: "always",
    });
}

export function useScheduledTask(taskId: string) {
    return useQuery({
        queryKey: scheduledTaskKeys.detail(taskId),
        queryFn: () => scheduledTaskService.fetchTask(taskId),
        enabled: !!taskId,
    });
}

export function useCreateScheduledTask() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (taskData: CreateScheduledTaskRequest) => scheduledTaskService.createTask(taskData),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: scheduledTaskKeys.lists() });
        },
    });
}

export function useUpdateScheduledTask() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: ({ taskId, updates }: { taskId: string; updates: UpdateScheduledTaskRequest }) => scheduledTaskService.updateTask(taskId, updates),
        onSuccess: (_, { taskId }) => {
            queryClient.invalidateQueries({ queryKey: scheduledTaskKeys.lists() });
            queryClient.invalidateQueries({ queryKey: scheduledTaskKeys.detail(taskId) });
        },
    });
}

export function useDeleteScheduledTask() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (taskId: string) => scheduledTaskService.deleteTask(taskId),
        onSuccess: (_, taskId) => {
            queryClient.invalidateQueries({ queryKey: scheduledTaskKeys.lists() });
            queryClient.invalidateQueries({ queryKey: scheduledTaskKeys.detail(taskId) });
        },
    });
}

export function useEnableScheduledTask() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (taskId: string) => scheduledTaskService.enableTask(taskId),
        onSuccess: (_, taskId) => {
            queryClient.invalidateQueries({ queryKey: scheduledTaskKeys.lists() });
            queryClient.invalidateQueries({ queryKey: scheduledTaskKeys.detail(taskId) });
        },
    });
}

export function useDisableScheduledTask() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (taskId: string) => scheduledTaskService.disableTask(taskId),
        onSuccess: (_, taskId) => {
            queryClient.invalidateQueries({ queryKey: scheduledTaskKeys.lists() });
            queryClient.invalidateQueries({ queryKey: scheduledTaskKeys.detail(taskId) });
        },
    });
}

export function useRunScheduledTaskNow() {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (taskId: string) => scheduledTaskService.runTaskNow(taskId),
        onSuccess: (_, taskId) => {
            queryClient.invalidateQueries({ queryKey: scheduledTaskKeys.executions(taskId) });
            queryClient.invalidateQueries({ queryKey: scheduledTaskKeys.detail(taskId) });
            queryClient.invalidateQueries({ queryKey: scheduledTaskKeys.lists() });
        },
    });
}

const ACTIVE_REFETCH_MS = 5_000;
const IDLE_REFETCH_MS = 30_000;

interface UseTaskExecutionsOptions {
    poll?: boolean;
}

export function useTaskExecutions(taskId: string, pageNumber: number = 1, pageSize: number = 20, startDate?: string | null, endDate?: string | null, options: UseTaskExecutionsOptions = {}) {
    return useQuery({
        queryKey: [...scheduledTaskKeys.executionList(taskId, { pageNumber, pageSize }), { startDate, endDate }],
        queryFn: () => scheduledTaskService.fetchExecutions(taskId, pageNumber, pageSize, startDate, endDate),
        enabled: !!taskId,
        refetchInterval: options.poll
            ? query => {
                  const data = query.state.data;
                  const hasActive = data?.data?.some((e: TaskExecution) => IN_PROGRESS_STATUSES.has(e.status));
                  return hasActive ? ACTIVE_REFETCH_MS : IDLE_REFETCH_MS;
              }
            : false,
        refetchOnWindowFocus: options.poll ? "always" : false,
    });
}

export function useExecution(executionId: string | null, options: { poll?: boolean } = {}) {
    return useQuery({
        queryKey: executionId ? scheduledTaskKeys.execution(executionId) : [...scheduledTaskKeys.all, "execution", "empty"],
        queryFn: () => scheduledTaskService.fetchExecution(executionId!),
        enabled: !!executionId,
        refetchInterval: options.poll
            ? query => {
                  const data = query.state.data;
                  if (!data) return IDLE_REFETCH_MS;
                  return IN_PROGRESS_STATUSES.has(data.status) ? ACTIVE_REFETCH_MS : IDLE_REFETCH_MS;
              }
            : false,
        refetchOnWindowFocus: options.poll ? "always" : false,
    });
}

export function useDeleteExecution(taskId: string) {
    const queryClient = useQueryClient();

    return useMutation({
        mutationFn: (executionId: string) => scheduledTaskService.deleteExecution(executionId),
        onMutate: executionId => {
            const sessionId = queryClient.getQueryData<TaskExecution>(scheduledTaskKeys.execution(executionId))?.sessionId;
            return { sessionId };
        },
        onSuccess: (_, executionId, context) => {
            queryClient.invalidateQueries({ queryKey: scheduledTaskKeys.executions(taskId) });
            queryClient.invalidateQueries({ queryKey: scheduledTaskKeys.detail(taskId) });
            queryClient.invalidateQueries({ queryKey: scheduledTaskKeys.lists() });
            queryClient.removeQueries({ queryKey: scheduledTaskKeys.execution(executionId) });
            if (context?.sessionId) {
                queryClient.removeQueries({ queryKey: scheduledTaskKeys.executionArtifacts(context.sessionId) });
            }
        },
    });
}

export function useExecutionArtifacts(sessionId: string | null | undefined) {
    return useQuery({
        queryKey: sessionId ? scheduledTaskKeys.executionArtifacts(sessionId) : ["execution-artifacts", "empty"],
        queryFn: () => scheduledTaskService.fetchExecutionArtifacts(sessionId!),
        enabled: !!sessionId,
    });
}

export function useRecentExecutions(pageSize: number = 50) {
    return useQuery({
        queryKey: scheduledTaskKeys.recentExecutions(pageSize),
        queryFn: () => scheduledTaskService.fetchRecentExecutions(pageSize),
    });
}

export function useSchedulerStatus() {
    return useQuery({
        queryKey: scheduledTaskKeys.schedulerStatus(),
        queryFn: scheduledTaskService.fetchSchedulerStatus,
    });
}

export function useValidateTaskConflict() {
    return useMutation<ConflictValidationResult, Error, ConflictValidationRequest & { signal?: AbortSignal }>({
        mutationFn: ({ signal, ...input }) => scheduledTaskService.validateTaskConflict(input, signal),
    });
}
