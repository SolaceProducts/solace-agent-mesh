// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";

import { useDeleteExecution, useExecution, useScheduledTask, useTaskExecutions } from "@/api/scheduled-tasks";

import { useChatContext } from "@/providers/contexts";

import { reportAndNotifyError } from "@/common/product-analytics/reportAndNotifyError";

import type { ScheduledTask, TaskExecution } from "@/types/scheduled-tasks";

import {
    buildExecutionBreadcrumbs,
    buildExecutionHeaderButtons,
    ConfigurationSidebar,
    DeleteExecutionDialog,
    endOfDayIso,
    ExecutionDetailPanel,
    ExecutionHistoryTable,
    findExecutionForChat,
    formatExecutionLabel,
    LatestExecutionPanel,
    PAGE_SIZE,
    resolveSelectedExecution,
    startOfDayIso,
} from "@/features/scheduledTasks/executionHistory";
import { useScheduledTaskActions } from "@/features/scheduledTasks/useScheduledTaskActions";

import { Header } from "@/common";
import { useIsMobile } from "@/hooks";
import { Sheet, SheetContent, SheetTitle, VisuallyHidden } from "@/ui";

interface TaskExecutionHistoryPageProps {
    task: ScheduledTask;
    onBack: () => void;
    onEdit: (task: ScheduledTask) => void;
    onDelete: (id: string, name: string) => void;
}

export const TaskExecutionHistoryPage: React.FC<TaskExecutionHistoryPageProps> = ({ task, onBack, onEdit, onDelete }) => {
    const navigate = useNavigate();
    const isMobile = useIsMobile();

    const { data: liveTask } = useScheduledTask(task.id);
    const currentTask = liveTask ?? task;
    const { handleSwitchSession, addNotification } = useChatContext();
    const [showConfig, setShowConfig] = useState(false);
    const [page, setPage] = useState(1);
    const [filterFrom, setFilterFrom] = useState("");
    const [filterTo, setFilterTo] = useState("");

    const filterFromIso = startOfDayIso(filterFrom);
    const filterToIso = endOfDayIso(filterTo);

    const handleFilterFromChange = (value: string) => {
        setFilterFrom(value);
        setPage(1);
    };
    const handleFilterToChange = (value: string) => {
        setFilterTo(value);
        setPage(1);
    };

    const { data, isLoading } = useTaskExecutions(task.id, page, PAGE_SIZE, filterFromIso, filterToIso, { poll: true });
    const executions = data?.data ?? [];
    const totalCount = data?.meta?.pagination?.count ?? executions.length;

    const { data: latestData } = useTaskExecutions(task.id, 1, 1, null, null, { poll: true });
    const latestExecutionFromQuery: TaskExecution | null = latestData?.data?.[0] ?? null;

    const { toggleEnabled, runNow, isRunNowPending } = useScheduledTaskActions();
    const deleteExecutionMutation = useDeleteExecution(task.id);
    const [executionToDelete, setExecutionToDelete] = useState<TaskExecution | null>(null);

    const [searchParams, setSearchParams] = useSearchParams();
    const selectedExecutionId = searchParams.get("execution");
    const { data: selectedExecutionFromQuery } = useExecution(selectedExecutionId, { poll: true });

    const handleSelectExecution = (executionId: string) => {
        setSearchParams(prev => {
            const next = new URLSearchParams(prev);
            next.set("execution", executionId);
            return next;
        });
    };
    const handleClearSelection = () => {
        setSearchParams(prev => {
            const next = new URLSearchParams(prev);
            next.delete("execution");
            return next;
        });
    };
    const [detailTab, setDetailTab] = useState<"output" | "artifacts">("output");

    const latestExecution = latestExecutionFromQuery;
    const selectedExecution = resolveSelectedExecution(selectedExecutionId, selectedExecutionFromQuery, executions, latestExecutionFromQuery);
    const displayedExecution = selectedExecution ?? latestExecution;

    const handleGoToChat = async (executionId: string) => {
        const exec = findExecutionForChat(executionId, selectedExecutionFromQuery, executions, latestExecutionFromQuery);
        if (!exec?.sessionId) {
            addNotification("This execution does not have a chat session yet.", "warning");
            return;
        }
        await handleSwitchSession(exec.sessionId);
        navigate("/chat");
    };

    const breadcrumbs = buildExecutionBreadcrumbs(currentTask.name, selectedExecution, onBack, handleClearSelection);

    const headerTitle = selectedExecution ? formatExecutionLabel(selectedExecution) : currentTask.name;

    const headerButtons = buildExecutionHeaderButtons({
        isMobile,
        selectedExecution,
        onShowConfig: () => setShowConfig(true),
        onGoToChat: handleGoToChat,
        onDeleteExecution: setExecutionToDelete,
    });

    const handleConfirmDelete = async (execution: TaskExecution) => {
        try {
            await deleteExecutionMutation.mutateAsync(execution.id);
        } catch (error) {
            const message = (error instanceof Error && error.message) || "Failed to delete the execution";
            reportAndNotifyError(addNotification, `Delete failed: ${message}`, "api");
            setExecutionToDelete(null);
            return;
        }
        setExecutionToDelete(null);
        if (selectedExecutionId === execution.id) {
            handleClearSelection();
        }
    };

    const renderConfigurationSidebar = (onClose?: () => void) => (
        <ConfigurationSidebar
            task={currentTask}
            execution={displayedExecution}
            isReadOnly={!!selectedExecution}
            onEdit={() => onEdit(currentTask)}
            onRunNow={() => runNow(currentTask)}
            onToggleEnabled={() => toggleEnabled(currentTask)}
            onDelete={() => onDelete(currentTask.id, currentTask.name)}
            isRunNowPending={isRunNowPending}
            onClose={onClose}
        />
    );

    return (
        <div className="flex h-full flex-col">
            <Header title={headerTitle} breadcrumbs={breadcrumbs} buttons={headerButtons} />

            <div className="flex min-h-0 flex-1">
                {!isMobile && renderConfigurationSidebar()}
                {isMobile && (
                    <Sheet open={showConfig} onOpenChange={setShowConfig}>
                        <SheetContent side="left" className="w-full max-w-sm p-0" showClose={false}>
                            <VisuallyHidden>
                                <SheetTitle>Configuration</SheetTitle>
                            </VisuallyHidden>
                            <div className="h-full overflow-y-auto">{renderConfigurationSidebar(() => setShowConfig(false))}</div>
                        </SheetContent>
                    </Sheet>
                )}

                <main className="flex-1 space-y-8 overflow-y-auto px-4 py-6 md:px-8">
                    {selectedExecution ? (
                        <ExecutionDetailPanel execution={selectedExecution} activeTab={detailTab} onTabChange={setDetailTab} />
                    ) : (
                        <>
                            <LatestExecutionPanel execution={latestExecution} onGoToChat={handleGoToChat} onShowFullOutput={handleSelectExecution} />
                            <div id="execution-history-anchor" />
                            <ExecutionHistoryTable
                                executions={executions}
                                totalCount={totalCount}
                                page={page}
                                onPageChange={setPage}
                                onRowClick={handleSelectExecution}
                                isLoading={isLoading}
                                onDeleteExecution={setExecutionToDelete}
                                filterFrom={filterFrom}
                                filterTo={filterTo}
                                onFilterFromChange={handleFilterFromChange}
                                onFilterToChange={handleFilterToChange}
                            />
                        </>
                    )}
                </main>
            </div>

            <DeleteExecutionDialog execution={executionToDelete} taskName={currentTask.name} isLoading={deleteExecutionMutation.isPending} onCancel={() => setExecutionToDelete(null)} onConfirm={handleConfirmDelete} />
        </div>
    );
};
