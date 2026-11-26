// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo } from "react";
import { Network } from "lucide-react";

import type { SharedTaskEvents } from "@/types/share";

import { processTaskForVisualization } from "@/features/activities/taskVisualizerProcessor";

import type { TaskFE, VisualizedTask } from "@/types";
import { cn } from "@/utils";

import { SharedFlowChartPanel } from "./SharedFlowChartPanel";

interface SharedWorkflowPanelProps {
    taskEvents: Record<string, SharedTaskEvents> | null | undefined;
    selectedTaskId: string | null;
    onTaskSelect: (taskId: string) => void;
}

function convertToTaskFE(taskId: string, taskData: SharedTaskEvents): TaskFE {
    const events = taskData.events || [];

    return {
        taskId,
        events,
        initialRequestText: taskData.initialRequestText || "",
        firstSeen: new Date(events[0]?.timestamp || Date.now()),
        lastUpdated: new Date(events[events.length - 1]?.timestamp || Date.now()),
    } as TaskFE;
}

function buildMonitoredTasks(taskEvents: Record<string, SharedTaskEvents>): Record<string, TaskFE> {
    const result: Record<string, TaskFE> = {};

    for (const [taskId, taskData] of Object.entries(taskEvents)) {
        result[taskId] = convertToTaskFE(taskId, taskData);
    }

    return result;
}

function findRootTaskId(taskEvents: Record<string, SharedTaskEvents>): string | null {
    const taskIds = Object.keys(taskEvents);
    if (taskIds.length === 0) return null;

    for (const [taskId, taskData] of Object.entries(taskEvents)) {
        const hasUserRequest = (taskData.events || []).some(event => event.direction === "request" && event.source_entity === "User");
        if (hasUserRequest) {
            return taskId;
        }
    }

    return taskIds[0];
}

export function SharedWorkflowPanel({ taskEvents, selectedTaskId, onTaskSelect }: SharedWorkflowPanelProps) {
    const { rootTaskId, monitoredTasks } = useMemo(() => {
        if (!taskEvents || Object.keys(taskEvents).length === 0) {
            return { rootTaskId: null, monitoredTasks: {} };
        }

        const monitoredTasks = buildMonitoredTasks(taskEvents);
        const rootTaskId = findRootTaskId(taskEvents);

        return { rootTaskId, monitoredTasks };
    }, [taskEvents]);

    useEffect(() => {
        if (rootTaskId && !selectedTaskId) {
            onTaskSelect(rootTaskId);
        }
    }, [rootTaskId, selectedTaskId, onTaskSelect]);

    const visualizedTask = useMemo(() => {
        if (!taskEvents || Object.keys(taskEvents).length === 0) {
            return null;
        }

        const taskIdToVisualize = selectedTaskId || rootTaskId;

        if (!taskIdToVisualize || !monitoredTasks[taskIdToVisualize]) {
            return null;
        }

        const parentTask = monitoredTasks[taskIdToVisualize];
        return processTaskForVisualization(parentTask.events || [], monitoredTasks, parentTask);
    }, [taskEvents, selectedTaskId, rootTaskId, monitoredTasks]);

    if (!taskEvents || Object.keys(taskEvents).length === 0) {
        return (
            <div className="flex h-full items-center justify-center p-4">
                <div className="text-center text-(--secondary-text-wMain)">
                    <Network className="mx-auto mb-4 h-12 w-12" />
                    <div className="text-lg font-medium">Workflow</div>
                    <div className="mt-2 text-sm">No workflow data available for this session</div>
                </div>
            </div>
        );
    }

    if (!visualizedTask) {
        return (
            <div className="flex h-full items-center justify-center p-4">
                <div className="text-center text-(--secondary-text-wMain)">
                    <Network className="mx-auto mb-4 h-12 w-12" />
                    <div className="text-lg font-medium">Workflow</div>
                    <div className="mt-2 text-sm">Unable to process workflow data</div>
                </div>
            </div>
        );
    }

    return (
        <div className="flex h-full flex-col">
            <SharedFlowChartDetails task={visualizedTask} />

            <div className="min-h-0 flex-1">
                <SharedFlowChartPanel processedSteps={visualizedTask.steps || []} />
            </div>
        </div>
    );
}

function SharedFlowChartDetails({ task }: { task: VisualizedTask }) {
    const getStatusBadge = (status: string) => {
        const statusColors: Record<string, string> = {
            completed: "bg-(--success-w10) text-(--success-wMain)",
            failed: "bg-(--error-w10) text-(--error-wMain)",
            canceled: "bg-(--secondary-w10) text-(--secondary-text-wMain)",
            working: "bg-(--info-w10) text-(--info-wMain)",
        };

        const colorClass = statusColors[status] || statusColors.working;

        return <span className={cn("rounded-full px-2 py-0.5 text-xs font-medium", colorClass)}>{status.charAt(0).toUpperCase() + status.slice(1)}</span>;
    };

    return (
        <div className="grid grid-cols-[auto_1fr] grid-rows-[32px_32px] items-center gap-x-4 border-b p-4">
            <div className="text-sm text-(--secondary-text-wMain)">User</div>
            <div className="truncate text-sm" title={task.initialRequestText}>
                {task.initialRequestText || "No request text"}
            </div>

            <div className="text-sm text-(--secondary-text-wMain)">Status</div>
            <div>{getStatusBadge(task.status)}</div>
        </div>
    );
}
