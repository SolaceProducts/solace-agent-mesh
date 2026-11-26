// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useState } from "react";
import { AlertCircle, BellOff, CalendarDays, CalendarSync, CircleCheckBig, Clock, History, Loader2, MinusCircle, MoreHorizontal, Pause, Pencil, Play, Trash2, XCircle, Zap } from "lucide-react";

import { formatDuration, formatRelativeTime } from "@/utils/format";
import { toEpochMs } from "@/utils/sessionUnseen";

import type { LastExecutionSummary, ScheduledTask } from "@/types/scheduled-tasks";

import { GridCard } from "@/common";
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/ui";

import { getStatusBadge } from "./StatusBadge";
import { formatSchedule } from "./utils";

const lastRunLabel = (last: LastExecutionSummary): string => {
    switch (last.status) {
        case "completed":
            return "Succeeded";
        case "quiet":
            return "Nothing to report";
        case "failed":
            return "Failed";
        case "timeout":
            return "Timed out";
        case "skipped":
            return "Skipped";
        case "cancelled":
            return "Cancelled";
        case "running":
            return "Running";
        case "pending":
            return "Pending";
        default:
            return "Last run";
    }
};

export const lastRunIconConfig = (last: LastExecutionSummary): { Icon: React.ComponentType<{ className?: string }>; colorClass: string } => {
    switch (last.status) {
        case "completed":
            return { Icon: CircleCheckBig, colorClass: "text-(--success-wMain)" };
        case "quiet":
            return { Icon: BellOff, colorClass: "text-(--secondary-text-wMain)" };
        case "failed":
            return { Icon: XCircle, colorClass: "text-(--error-wMain)" };
        case "timeout":
            return { Icon: AlertCircle, colorClass: "text-(--warning-wMain)" };
        case "skipped":
        case "cancelled":
            return { Icon: MinusCircle, colorClass: "text-(--secondary-text-wMain)" };
        case "running":
        case "pending":
            return { Icon: Loader2, colorClass: "text-(--brand-wMain)" };
        default:
            return { Icon: Clock, colorClass: "text-(--secondary-text-wMain)" };
    }
};

interface TaskCardProps {
    task: ScheduledTask;
    isSelected?: boolean;
    onTaskClick: () => void;
    onEdit: (task: ScheduledTask) => void;
    onDelete: (taskId: string) => void;
    onToggleEnabled: (task: ScheduledTask) => void;
    onViewExecutions: (task: ScheduledTask) => void;
    onRunNow?: (task: ScheduledTask) => void;
    isRunNowPending?: boolean;
}

export const TaskCard: React.FC<TaskCardProps> = ({ task, isSelected = false, onTaskClick, onEdit, onDelete, onToggleEnabled, onViewExecutions, onRunNow, isRunNowPending = false }) => {
    const [dropdownOpen, setDropdownOpen] = useState(false);

    const handleEdit = (e: React.MouseEvent) => {
        e.stopPropagation();
        setDropdownOpen(false);
        onEdit(task);
    };

    const handleDelete = (e: React.MouseEvent) => {
        e.stopPropagation();
        setDropdownOpen(false);
        onDelete(task.id);
    };

    const handleToggleEnabled = (e: React.MouseEvent) => {
        e.stopPropagation();
        setDropdownOpen(false);
        onToggleEnabled(task);
    };

    const handleViewExecutions = (e: React.MouseEvent) => {
        e.stopPropagation();
        setDropdownOpen(false);
        onViewExecutions(task);
    };

    const handleRunNow = (e: Event | React.MouseEvent) => {
        (e as React.MouseEvent).stopPropagation?.();
        setDropdownOpen(false);
        onRunNow?.(task);
    };

    const canRunNow = !!onRunNow && task.scheduleType !== "one_time" && task.source !== "config";

    const isRunning = !!task.lastExecution && (task.lastExecution.status === "running" || task.lastExecution.status === "pending");

    const formatNextRun = (task: ScheduledTask): string => {
        if (task.status === "paused") return "Paused";

        if (task.lastExecution && (task.lastExecution.status === "running" || task.lastExecution.status === "pending")) {
            return "Running now";
        }

        if (!task.nextRunTime) {
            if (task.scheduleType === "one_time" && task.lastRunTime) return "Completed";
            return "Not scheduled";
        }

        const now = Date.now();
        const diff = new Date(task.nextRunTime).getTime() - now;

        if (diff >= 0) {
            if (diff < 60000) return "In < 1 minute";
            if (diff < 3600000) return `In ${Math.floor(diff / 60000)} minutes`;
            if (diff < 86400000) return `In ${Math.floor(diff / 3600000)} hours`;
            return `In ${Math.floor(diff / 86400000)} days`;
        }

        return "Due now";
    };

    return (
        <GridCard onClick={onTaskClick} isSelected={isSelected}>
            <div className="flex h-full w-full flex-col">
                <div className="flex items-center justify-between px-4">
                    <div className="flex min-w-0 flex-1 items-center gap-2">
                        <CalendarDays className="h-6 w-6 flex-shrink-0 text-(--brand-wMain)" />
                        <div className="min-w-0">
                            <h2 className="truncate text-lg font-semibold" title={task.name}>
                                {task.name}
                            </h2>
                        </div>
                    </div>
                    <div className="flex items-center gap-1">
                        <DropdownMenu open={dropdownOpen} onOpenChange={setDropdownOpen}>
                            <DropdownMenuTrigger asChild>
                                <Button variant="ghost" size="icon" onClick={e => e.stopPropagation()} tooltip="Actions" className="cursor-pointer">
                                    <MoreHorizontal size={16} />
                                </Button>
                            </DropdownMenuTrigger>
                            <DropdownMenuContent align="end" onClick={e => e.stopPropagation()}>
                                {canRunNow && (
                                    <DropdownMenuItem onSelect={handleRunNow} disabled={isRunNowPending}>
                                        <Zap size={14} className="mr-2" />
                                        {isRunNowPending ? "Running…" : "Run Now"}
                                    </DropdownMenuItem>
                                )}
                                <DropdownMenuItem onClick={handleEdit}>
                                    <Pencil size={14} className="mr-2" />
                                    Edit Task
                                </DropdownMenuItem>
                                <DropdownMenuItem onClick={handleViewExecutions}>
                                    <History size={14} className="mr-2" />
                                    View Execution History
                                </DropdownMenuItem>
                                <DropdownMenuItem onClick={handleToggleEnabled}>
                                    {task.enabled ? (
                                        <>
                                            <Pause size={14} className="mr-2" />
                                            Pause Task
                                        </>
                                    ) : (
                                        <>
                                            <Play size={14} className="mr-2" />
                                            Resume Task
                                        </>
                                    )}
                                </DropdownMenuItem>
                                <DropdownMenuItem onClick={handleDelete}>
                                    <Trash2 size={14} className="mr-2" />
                                    Delete Task
                                </DropdownMenuItem>
                            </DropdownMenuContent>
                        </DropdownMenu>
                    </div>
                </div>
                <div className="flex flex-grow flex-col overflow-hidden px-4">
                    {task.source === "config" && (
                        <div className="mb-2 flex items-center gap-2">
                            <span className="inline-block rounded-full bg-(--info-w10) px-2 py-0.5 text-xs text-(--info-w100)">Config</span>
                        </div>
                    )}
                    {task.description && (
                        <div className="mb-2 flex-shrink-0 truncate text-sm leading-6" title={task.description}>
                            {task.description}
                        </div>
                    )}
                    <div className="mt-2 space-y-1.5">
                        <div className="flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)">
                            <Clock className="h-3 w-3 flex-shrink-0" />
                            <span className="truncate">
                                <span className="font-medium">Occurs:</span> {formatSchedule(task)}
                            </span>
                        </div>
                        <div className="flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)">
                            <CalendarSync className="h-3 w-3 flex-shrink-0" />
                            <span className="truncate">
                                <span className="font-medium">Next run:</span> {formatNextRun(task)}
                            </span>
                        </div>
                        {task.lastCompletedExecution &&
                            (() => {
                                const last = task.lastCompletedExecution;
                                const { Icon, colorClass } = lastRunIconConfig(last);
                                const whenTs = toEpochMs(last.completedAt ?? last.startedAt ?? last.scheduledFor);
                                const whenIso = new Date(whenTs).toISOString();
                                const relative = formatRelativeTime(whenIso);
                                const absolute = new Date(whenTs).toLocaleString();
                                const duration = last.durationMs ? ` · ${formatDuration(last.durationMs)}` : "";
                                const label = lastRunLabel(last);
                                return (
                                    <div className="flex items-center gap-1.5 text-xs text-(--secondary-text-wMain)" title={last.errorMessage ?? `${label} · ${absolute}${duration}`}>
                                        <Icon className={`h-3 w-3 flex-shrink-0 ${colorClass}`} />
                                        <span className="truncate">
                                            {label} {relative}
                                        </span>
                                    </div>
                                );
                            })()}
                    </div>
                    <div className="mt-auto pt-2">
                        {isRunning ? (
                            <span className="inline-flex items-center gap-1.5 text-xs font-medium text-(--primary-text-wMain)">
                                Running
                                <Loader2 className="h-3 w-3 animate-spin text-(--brand-wMain)" />
                            </span>
                        ) : task.status ? (
                            getStatusBadge(task.status)
                        ) : null}
                    </div>
                </div>
            </div>
        </GridCard>
    );
};
