// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { PaginatedResponse } from "@/api/common";

export type ScheduleType = "cron" | "interval" | "one_time";
export type TaskStatus = "active" | "paused" | "error";
export type TargetType = "agent" | "workflow";
export type ExecutionStatus = "pending" | "running" | "completed" | "quiet" | "failed" | "timeout" | "cancelled" | "skipped";
export type TriggerType = "scheduled" | "manual";
export type TaskSource = "ui" | "config" | "chat";

export const IN_PROGRESS_STATUSES: ReadonlySet<ExecutionStatus> = new Set<ExecutionStatus>(["pending", "running"]);

export const STATUS_LABELS: Record<string, string> = {
    completed: "Completed",
    failed: "Failed",
    pending: "Pending",
    running: "Running",
    timeout: "Timeout",
};

export interface MessagePart {
    type: "text" | "file";
    text?: string;
    uri?: string;
}

export interface NotificationChannel {
    type: "sse" | "webhook" | "email" | "broker_topic";
    config: Record<string, unknown>;
}

export interface NotificationConfig {
    channels: NotificationChannel[];
    onSuccess: boolean;
    onFailure: boolean;
    includeArtifacts: boolean;
}

export interface ScheduledTask {
    type?: "scheduledTask";
    id: string;
    name: string;
    description?: string;

    namespace: string;
    userId?: string;
    createdBy: string;

    scheduleType: ScheduleType;
    scheduleExpression: string;
    timezone: string;

    targetAgentName: string;
    targetType: TargetType;
    taskMessage: MessagePart[];
    taskMetadata?: Record<string, unknown>;

    enabled: boolean;
    status: TaskStatus;
    maxRetries: number;
    retryDelaySeconds: number;
    timeoutSeconds: number;

    source?: TaskSource;
    consecutiveFailureCount: number;
    runCount: number;

    notificationConfig?: NotificationConfig;

    createdAt: string;
    updatedAt: string;
    nextRunTime?: string | null;
    lastRunTime?: string | null;

    lastExecution?: LastExecutionSummary;
    lastCompletedExecution?: LastExecutionSummary;
}

export interface LastExecutionSummary {
    id: string;
    status: ExecutionStatus;
    scheduledFor: string;
    startedAt?: string;
    completedAt?: string;
    durationMs?: number;
    errorMessage?: string;
    triggerType?: TriggerType;
}

export interface ArtifactInfo {
    name: string;
    uri: string;
}

export interface TaskExecution {
    type?: "scheduledTaskExecution";
    id: string;
    scheduledTaskId: string;

    status: ExecutionStatus;
    a2aTaskId?: string;

    scheduledFor: string;
    startedAt?: string;
    completedAt?: string;
    durationMs?: number;

    resultSummary?: {
        agentResponse?: string;
        agentResponseFull?: string;
        messages?: Array<{ role: string; text: string }>;
        artifacts?: Array<{ name?: string; uri?: string; type?: string }>;
        metadata?: Record<string, unknown>;
        taskStatus?: string;
        errorCode?: number;
        errorData?: unknown;
        ragData?: unknown[];
    };
    errorMessage?: string;
    retryCount: number;

    triggerType?: TriggerType;
    triggeredBy?: string;

    sessionId?: string;

    artifacts?: Array<string | ArtifactInfo>;
    notificationsSent?: Array<{
        type: string;
        status: string;
        sentAt: string;
        error?: string;
    }>;

    taskSnapshot?: TaskExecutionSnapshot | null;
}

export interface TaskExecutionSnapshot {
    name: string;
    description?: string | null;
    scheduleType: ScheduleType;
    scheduleExpression: string;
    timezone: string;
    targetAgentName: string;
    targetType: "agent" | "workflow";
    taskMessage: MessagePart[];
}

export type ScheduledTaskListResponse = PaginatedResponse<ScheduledTask>;

export type ExecutionListResponse = PaginatedResponse<TaskExecution>;

export interface SchedulerStatus {
    instanceId: string;
    namespace: string;
    isLeader?: boolean;
    activeTasksCount: number;
    runningExecutionsCount: number;
    pendingResultsCount?: number;
    schedulerRunning: boolean;
    leaderInfo?: {
        leaderId: string;
        leaderNamespace: string;
        acquiredAt: number;
        expiresAt: number;
        heartbeatAt: number;
        isExpired: boolean;
        isSelf: boolean;
    };
}

export interface CreateScheduledTaskRequest {
    name: string;
    description?: string;
    scheduleType: ScheduleType;
    scheduleExpression: string;
    timezone?: string;
    targetAgentName: string;
    targetType?: TargetType;
    taskMessage: MessagePart[];
    taskMetadata?: Record<string, unknown>;
    enabled?: boolean;
    maxRetries?: number;
    retryDelaySeconds?: number;
    timeoutSeconds?: number;
    notificationConfig?: NotificationConfig;
    userLevel?: boolean;
}

export interface UpdateScheduledTaskRequest {
    name?: string;
    description?: string;
    scheduleType?: ScheduleType;
    scheduleExpression?: string;
    timezone?: string;
    targetAgentName?: string;
    targetType?: TargetType;
    taskMessage?: MessagePart[];
    taskMetadata?: Record<string, unknown>;
    enabled?: boolean;
    maxRetries?: number;
    retryDelaySeconds?: number;
    timeoutSeconds?: number;
    notificationConfig?: NotificationConfig;
}

export function deriveTaskStatus(enabled: boolean, consecutiveFailureCount: number): TaskStatus {
    if (!enabled) return "paused";
    if (consecutiveFailureCount > 0) return "error";
    return "active";
}

export function fillScheduledTaskDerived(task: ScheduledTask): ScheduledTask {
    if (task.status) return task;
    return {
        ...task,
        status: deriveTaskStatus(task.enabled, task.consecutiveFailureCount ?? 0),
    };
}
