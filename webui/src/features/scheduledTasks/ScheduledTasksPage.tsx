// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useMemo, useRef, useState } from "react";
import { AlertCircle, RefreshCw } from "lucide-react";
import { useSearchParams } from "react-router-dom";

import { useDeleteScheduledTask, useScheduledTasks } from "@/api/scheduled-tasks";

import { useChatContext } from "@/providers/contexts";

import { ConfirmationDialog } from "@/common/dialogs/ConfirmationDialog";
import { Header } from "@/common/layout/Header";
import { PageLayout } from "@/common/layout/PageLayout";
import { reportAndNotifyError } from "@/common/product-analytics/reportAndNotifyError";
import { EmptyState } from "@/common/status/EmptyState";

import { clearPendingPrefill, consumePendingPrefill } from "@/utils/redirectPrefill";
import { SESSION_STORAGE_KEYS } from "@/utils/sessionStorageKeys";

import type { ScheduledTask } from "@/types/scheduled-tasks";

import { GenerateTaskDialog } from "@/features/scheduledTasks/GenerateTaskDialog";
import { TaskCards } from "@/features/scheduledTasks/TaskCards";
import { TaskTemplateBuilder } from "@/features/scheduledTasks/TaskTemplateBuilder";
import { useScheduledTaskActions } from "@/features/scheduledTasks/useScheduledTaskActions";

import { Button, LifecycleBadge } from "@/ui";

import { scheduledTasksGate } from "./ScheduledTasksGate";
import { TaskExecutionHistoryPage } from "./TaskExecutionHistoryPage";
import { useSchedulableTargets } from "./useSchedulableTargets";

const pageTitle = (
    <>
        Scheduled Tasks <LifecycleBadge>EXPERIMENTAL</LifecycleBadge>
    </>
);

function redirectAwaitsData(mode: string, taskId: string | null, tasksLoading: boolean, targetsLoading: boolean): boolean {
    if (mode === "create") {
        return targetsLoading;
    }
    if (mode === "edit" || mode === "delete") {
        return tasksLoading || !taskId;
    }
    return false;
}

export function ScheduledTasksPage() {
    const { data: tasksResponse, isLoading, error, refetch: loadTasks } = useScheduledTasks();
    const { isLoading: targetsLoading, hasNoInvokeAccess, hasCardsError, refetch: refetchTargets, isRefetching: isRefetchingTargets } = useSchedulableTargets();
    const { toggleEnabled, runNow, runNowPendingTaskId } = useScheduledTaskActions();
    const deleteTaskMutation = useDeleteScheduledTask();
    const { addNotification } = useChatContext();

    const tasks = useMemo(() => tasksResponse?.data ?? [], [tasksResponse?.data]);

    const [editingTask, setEditingTask] = useState<ScheduledTask | null>(null);
    const [viewingTaskHistory, setViewingTaskHistory] = useState<ScheduledTask | null>(null);
    const [showBuilder, setShowBuilder] = useState(false);
    const [showGenerateDialog, setShowGenerateDialog] = useState(false);
    const [initialMessage, setInitialMessage] = useState<string | null>(null);
    const [builderInitialMode, setBuilderInitialMode] = useState<"manual" | "ai-assisted">("ai-assisted");
    const [deleteConfirm, setDeleteConfirm] = useState<{ taskId: string; taskName: string; source: "list" | "history" } | null>(null);

    const [searchParams, setSearchParams] = useSearchParams();
    const redirectAppliedRef = useRef(false);
    useEffect(() => {
        if (redirectAppliedRef.current) return;
        const mode = searchParams.get("mode");
        if (!mode) return;
        const taskId = searchParams.get("taskId");
        if (redirectAwaitsData(mode, taskId, isLoading, targetsLoading)) return;
        redirectAppliedRef.current = true;

        const pending = consumePendingPrefill(null) ?? sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT);
        clearPendingPrefill();
        sessionStorage.removeItem(SESSION_STORAGE_KEYS.REDIRECT_CONTEXT);
        sessionStorage.removeItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_ARTIFACTS);

        const notifyTaskNotFound = () => {
            reportAndNotifyError(addNotification, `Couldn't find scheduled task ${taskId}.`, "client");
        };

        switch (mode) {
            case "create":
                if (hasNoInvokeAccess) break;
                if (pending) setInitialMessage(pending);
                setBuilderInitialMode("ai-assisted");
                setShowBuilder(true);
                break;
            case "edit": {
                const task = tasks.find(t => t.id === taskId);
                if (task) {
                    setEditingTask(task);
                    setInitialMessage(pending);
                    setBuilderInitialMode("ai-assisted");
                    setShowBuilder(true);
                } else {
                    notifyTaskNotFound();
                }
                break;
            }
            case "delete": {
                const task = tasks.find(t => t.id === taskId);
                if (task) {
                    setDeleteConfirm({ taskId: task.id, taskName: task.name, source: "list" });
                } else {
                    notifyTaskNotFound();
                }
                break;
            }
            case "view":
                break;
        }

        const nextParams = new URLSearchParams(searchParams);
        nextParams.delete("mode");
        nextParams.delete("taskId");
        setSearchParams(nextParams, { replace: true });
    }, [searchParams, setSearchParams, isLoading, targetsLoading, hasNoInvokeAccess, tasks, addNotification]);

    const handleEditTask = (task: ScheduledTask) => {
        setEditingTask(task);
        setBuilderInitialMode("manual");
        setShowBuilder(true);
    };

    const handleDelete = (taskId: string) => {
        const task = tasks.find(t => t.id === taskId);
        setDeleteConfirm({ taskId, taskName: task?.name || "this task", source: "list" });
    };

    const handleViewExecutions = (task: ScheduledTask) => {
        setViewingTaskHistory(task);
    };

    const handleDeleteFromHistory = (taskId: string, taskName: string) => {
        setDeleteConfirm({ taskId, taskName, source: "history" });
    };

    const handleConfirmDelete = async () => {
        if (!deleteConfirm) return;
        try {
            await deleteTaskMutation.mutateAsync(deleteConfirm.taskId);
            if (deleteConfirm.source === "history") {
                setViewingTaskHistory(null);
            }
        } finally {
            setDeleteConfirm(null);
        }
    };

    const handleGenerateTask = (taskDescription: string) => {
        setInitialMessage(taskDescription);
        setShowGenerateDialog(false);
        setBuilderInitialMode("ai-assisted");
        setShowBuilder(true);
    };

    if (showBuilder) {
        return (
            <>
                <TaskTemplateBuilder
                    onBack={() => {
                        setShowBuilder(false);
                        setInitialMessage(null);
                        setEditingTask(null);
                    }}
                    onSuccess={async () => {
                        const wasEditingTask = editingTask;
                        setShowBuilder(false);
                        setInitialMessage(null);
                        setEditingTask(null);
                        const { data: refreshed } = await loadTasks();

                        if (wasEditingTask && viewingTaskHistory && viewingTaskHistory.id === wasEditingTask.id) {
                            const updatedTask = refreshed?.data.find(t => t.id === wasEditingTask.id);
                            if (updatedTask) {
                                setViewingTaskHistory(updatedTask);
                            }
                        }
                    }}
                    initialMessage={initialMessage}
                    initialMode={builderInitialMode}
                    editingTask={editingTask}
                    isEditing={!!editingTask}
                />
            </>
        );
    }

    if (viewingTaskHistory) {
        return (
            <>
                <TaskExecutionHistoryPage task={viewingTaskHistory} onBack={() => setViewingTaskHistory(null)} onEdit={handleEditTask} onDelete={handleDeleteFromHistory} />
                <ConfirmationDialog
                    open={!!deleteConfirm}
                    title={deleteConfirm ? `Delete ${deleteConfirm.taskName}` : ""}
                    content={
                        deleteConfirm ? (
                            <p className="text-sm">
                                Deleting <strong>{deleteConfirm.taskName}</strong> will remove it from Scheduled Tasks and will no longer be active.
                            </p>
                        ) : null
                    }
                    onOpenChange={open => {
                        if (!open) setDeleteConfirm(null);
                    }}
                    onConfirm={handleConfirmDelete}
                    actionLabels={{ confirm: "Delete", cancel: "Cancel" }}
                />
            </>
        );
    }

    const hasNoTasksToManage = !isLoading && !error && tasks.length === 0;
    const gate = hasNoTasksToManage ? scheduledTasksGate({ hasNoInvokeAccess, hasCardsError, onRetry: refetchTargets, isRetrying: isRefetchingTargets }) : null;
    if (gate) {
        return (
            <PageLayout>
                <Header title={pageTitle} />
                {gate}
            </PageLayout>
        );
    }

    return (
        <PageLayout>
            <Header
                title={pageTitle}
                buttons={[
                    <Button data-testid="refreshTasks" disabled={isLoading} variant="ghost" tooltip="Refresh Tasks" onClick={() => loadTasks()}>
                        <RefreshCw className="size-4" />
                        Refresh
                    </Button>,
                ]}
            />

            {error && (
                <div className="mx-6 mt-4 flex items-center gap-2 rounded-md bg-(--error-w10) p-4 text-(--error-wMain)">
                    <AlertCircle className="size-4" />
                    <span>{error.message}</span>
                </div>
            )}

            {(isLoading || targetsLoading) && tasks.length === 0 ? (
                <EmptyState title="Loading tasks..." variant="loading" />
            ) : (
                <div className="relative flex-1 p-4">
                    <TaskCards
                        tasks={tasks}
                        onManualCreate={() => {
                            setBuilderInitialMode("manual");
                            setShowBuilder(true);
                        }}
                        onAIAssisted={() => setShowGenerateDialog(true)}
                        onEdit={handleEditTask}
                        onDelete={handleDelete}
                        onToggleEnabled={toggleEnabled}
                        onViewExecutions={handleViewExecutions}
                        onRunNow={runNow}
                        runNowPendingTaskId={runNowPendingTaskId}
                    />
                </div>
            )}

            <GenerateTaskDialog isOpen={showGenerateDialog} onClose={() => setShowGenerateDialog(false)} onGenerate={handleGenerateTask} />

            <ConfirmationDialog
                open={!!deleteConfirm}
                title="Delete Scheduled Task"
                description={`Are you sure you want to delete "${deleteConfirm?.taskName}"?`}
                onOpenChange={open => {
                    if (!open) setDeleteConfirm(null);
                }}
                onConfirm={handleConfirmDelete}
                actionLabels={{ confirm: "Delete", cancel: "Cancel" }}
            />
        </PageLayout>
    );
}
