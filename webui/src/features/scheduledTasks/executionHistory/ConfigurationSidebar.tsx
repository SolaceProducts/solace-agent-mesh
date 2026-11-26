// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { MoreHorizontal, Pause, Pencil, Play, Trash2, X, Zap } from "lucide-react";
import { useNavigate } from "react-router-dom";

import type { ScheduledTask, TaskExecution } from "@/types/scheduled-tasks";

import { getStatusBadge } from "@/features/scheduledTasks/StatusBadge";
import { useTargetDisplayName } from "@/features/scheduledTasks/useTargetDisplayName";
import { formatSchedule } from "@/features/scheduledTasks/utils";

import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/ui";

const ConfigField: React.FC<{ label: string; children: React.ReactNode; multiline?: boolean }> = ({ label, children, multiline }) => (
    <div className="space-y-1">
        <div className="text-xs text-(--secondary-text-wMain)">{label}</div>
        <div className={multiline ? "text-sm break-words whitespace-pre-wrap" : "truncate text-sm"}>{children}</div>
    </div>
);

interface ConfigurationSidebarProps {
    task: ScheduledTask;
    execution: TaskExecution | null;
    isReadOnly: boolean;
    onEdit: () => void;
    onRunNow: () => void;
    onToggleEnabled: () => void;
    onDelete: () => void;
    isRunNowPending: boolean;
    onClose?: () => void;
}

export const ConfigurationSidebar: React.FC<ConfigurationSidebarProps> = ({ task, execution, isReadOnly, onEdit, onRunNow, onToggleEnabled, onDelete, isRunNowPending, onClose }) => {
    const navigate = useNavigate();
    const canRunNow = task.scheduleType !== "one_time" && task.source !== "config";

    const snapshot = execution?.taskSnapshot ?? null;
    const name = snapshot?.name ?? task.name;
    const description = snapshot?.description ?? task.description;
    const scheduleType = snapshot?.scheduleType ?? task.scheduleType;
    const scheduleExpression = snapshot?.scheduleExpression ?? task.scheduleExpression;
    const timezone = snapshot?.timezone ?? task.timezone;
    const targetAgentName = snapshot?.targetAgentName ?? task.targetAgentName;
    const targetType = snapshot?.targetType ?? task.targetType;
    const taskMessage = snapshot?.taskMessage ?? task.taskMessage;
    const agentDisplay = useTargetDisplayName(targetAgentName);
    const taskMessageText = taskMessage?.[0]?.text || "";

    return (
        <aside className="flex h-full w-full flex-col overflow-y-auto bg-(--background-w10) md:w-[320px] md:border-r">
            <div className="flex items-center justify-between px-6 py-4 md:px-8">
                <h2 className="text-base font-semibold">Configuration</h2>
                <div className="flex items-center gap-1">
                    {!isReadOnly && (
                        <>
                            <Button variant="ghost" size="sm" onClick={onEdit} className="h-8 px-2">
                                <Pencil className="mr-1 h-3.5 w-3.5" />
                                Edit
                            </Button>
                            <DropdownMenu>
                                <DropdownMenuTrigger asChild>
                                    <Button variant="ghost" size="sm" className="h-8 w-8 p-0" tooltip="Actions">
                                        <MoreHorizontal className="h-4 w-4" />
                                    </Button>
                                </DropdownMenuTrigger>
                                <DropdownMenuContent align="end">
                                    {canRunNow && (
                                        <DropdownMenuItem onSelect={onRunNow} disabled={isRunNowPending}>
                                            <Zap size={14} className="mr-2" />
                                            {isRunNowPending ? "Running…" : "Run Now"}
                                        </DropdownMenuItem>
                                    )}
                                    <DropdownMenuItem onClick={onToggleEnabled}>
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
                                    <DropdownMenuItem onClick={onDelete}>
                                        <Trash2 size={14} className="mr-2" />
                                        Delete
                                    </DropdownMenuItem>
                                </DropdownMenuContent>
                            </DropdownMenu>
                        </>
                    )}
                    {onClose && (
                        <Button variant="ghost" size="icon" className="h-8 w-8" onClick={onClose} aria-label="Close configuration">
                            <X className="h-4 w-4" />
                        </Button>
                    )}
                </div>
            </div>

            <div className="space-y-5 px-6 py-5 md:px-8">
                {!isReadOnly && <ConfigField label="Status">{getStatusBadge(task.status)}</ConfigField>}
                <ConfigField label="Name">{name}</ConfigField>
                <ConfigField label="Description" multiline>
                    {description || <span className="text-(--secondary-text-wMain) italic">No description</span>}
                </ConfigField>
                <ConfigField label="Schedule">{formatSchedule({ scheduleType, scheduleExpression })}</ConfigField>
                <ConfigField label="Timezone">{timezone}</ConfigField>
                <ConfigField label={targetType === "workflow" ? "Workflow" : "Agent"}>
                    <Button variant="link" className="h-auto p-0" onClick={() => navigate(`/agent-mesh?selected=${encodeURIComponent(targetAgentName)}`)}>
                        {agentDisplay}
                    </Button>
                </ConfigField>
                <ConfigField label="Output">Chat</ConfigField>
                <ConfigField label="Instructions" multiline>
                    {taskMessageText || <span className="text-(--secondary-text-wMain) italic">No instructions</span>}
                </ConfigField>
            </div>
        </aside>
    );
};
