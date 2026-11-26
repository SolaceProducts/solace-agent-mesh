// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useEffect, useMemo, useState } from "react";
import { Filter, X } from "lucide-react";
import { useSearchParams } from "react-router-dom";

import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/ui/resizable";
import type { ScheduledTask } from "@/types/scheduled-tasks";

import { EmptyState } from "@/common";
import { useIsMobile } from "@/hooks";
import { Button, SearchInput, Sheet, SheetContent } from "@/ui";

import { CreateTaskCard } from "./CreateTaskCard";
import { TaskCard } from "./TaskCard";
import { TaskDetailSidePanel } from "./TaskDetailSidePanel";
import { useTargetDisplayNameResolver } from "./useTargetDisplayName";

interface TaskCardsProps {
    tasks: ScheduledTask[];
    onManualCreate: () => void;
    onAIAssisted: () => void;
    onEdit: (task: ScheduledTask) => void;
    onDelete: (taskId: string) => void;
    onToggleEnabled: (task: ScheduledTask) => void;
    onViewExecutions: (task: ScheduledTask) => void;
    onRunNow?: (task: ScheduledTask) => void;
    runNowPendingTaskId?: string | null;
}

export const TaskCards: React.FC<TaskCardsProps> = ({ tasks, onManualCreate, onAIAssisted, onEdit, onDelete, onToggleEnabled, onViewExecutions, onRunNow, runNowPendingTaskId }) => {
    const isMobile = useIsMobile();
    const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null);
    const [searchQuery, setSearchQuery] = useState<string>("");
    const [selectedStatuses, setSelectedStatuses] = useState<string[]>([]);
    const [showStatusDropdown, setShowStatusDropdown] = useState(false);

    const [searchParams, setSearchParams] = useSearchParams();
    useEffect(() => {
        const deeplinkTaskId = searchParams.get("taskId");
        if (!deeplinkTaskId) return;
        if (tasks.some(t => t.id === deeplinkTaskId)) {
            setSelectedTaskId(deeplinkTaskId);
            requestAnimationFrame(() => {
                document.querySelector(`[data-task-id="${deeplinkTaskId}"]`)?.scrollIntoView({ behavior: "smooth", block: "center" });
            });
            const next = new URLSearchParams(searchParams);
            next.delete("taskId");
            setSearchParams(next, { replace: true });
        }
    }, [searchParams, setSearchParams, tasks]);

    const selectedTask = useMemo(() => tasks.find(t => t.id === selectedTaskId) ?? null, [tasks, selectedTaskId]);

    const handleTaskClick = (task: ScheduledTask) => {
        setSelectedTaskId(prev => (prev === task.id ? null : task.id));
    };

    const handleCloseSidePanel = () => {
        setSelectedTaskId(null);
    };

    const resolveTargetDisplayName = useTargetDisplayNameResolver();

    const statuses = ["Active", "Paused", "Error"];

    const filteredTasks = useMemo(() => {
        const statusMap: Record<string, string> = { Active: "active", Paused: "paused", Error: "error" };
        const selectedStatusValues = selectedStatuses.map(s => statusMap[s]);

        const filtered = tasks.filter(task => {
            const matchesSearch =
                task.name?.toLowerCase().includes(searchQuery.toLowerCase()) ||
                task.description?.toLowerCase().includes(searchQuery.toLowerCase()) ||
                task.targetAgentName?.toLowerCase().includes(searchQuery.toLowerCase()) ||
                resolveTargetDisplayName(task.targetAgentName).toLowerCase().includes(searchQuery.toLowerCase());

            const matchesStatus = selectedStatusValues.length === 0 || selectedStatusValues.includes(task.status);

            return matchesSearch && matchesStatus;
        });

        const statusOrder: Record<string, number> = { active: 0, paused: 1, error: 2 };
        return filtered.sort((a, b) => {
            const statusDiff = (statusOrder[a.status] ?? 3) - (statusOrder[b.status] ?? 3);
            if (statusDiff !== 0) return statusDiff;
            const nameA = (a.name || "").toLowerCase();
            const nameB = (b.name || "").toLowerCase();
            return nameA.localeCompare(nameB);
        });
    }, [tasks, searchQuery, selectedStatuses, resolveTargetDisplayName]);

    const toggleStatus = (status: string) => {
        setSelectedStatuses(prev => (prev.includes(status) ? prev.filter(s => s !== status) : [...prev, status]));
    };

    const clearStatuses = () => {
        setSelectedStatuses([]);
    };

    const clearAllFilters = () => {
        setSearchQuery("");
        setSelectedStatuses([]);
    };

    const hasActiveFilters = searchQuery.length > 0 || selectedStatuses.length > 0;
    const isLibraryEmpty = tasks.length === 0;

    const createButtons = useMemo(() => {
        return [
            {
                text: "Build with AI",
                variant: "default" as const,
                onClick: onAIAssisted,
            },
            {
                text: "Create Manually",
                variant: "outline" as const,
                onClick: onManualCreate,
            },
        ];
    }, [onAIAssisted, onManualCreate]);

    return (
        <div className="absolute inset-0 h-full w-full">
            <ResizablePanelGroup orientation="horizontal" className="h-full">
                <ResizablePanel defaultSize={`${selectedTask && !isMobile ? 70 : 100}%`} minSize="50%" maxSize="100%" id="taskCardsMainPanel">
                    <div className="flex h-full flex-col px-4 py-4 sm:pt-6 sm:pr-0 sm:pb-6 sm:pl-6">
                        {!isLibraryEmpty && (
                            <div className="mb-4 flex flex-wrap items-center gap-2">
                                <SearchInput value={searchQuery} onChange={setSearchQuery} placeholder="Filter by name, description, or agent..." testid="taskSearchInput" />

                                <div className="relative">
                                    <Button onClick={() => setShowStatusDropdown(!showStatusDropdown)} variant="outline" testid="taskStatusFilter">
                                        <Filter size={16} />
                                        Status
                                        {selectedStatuses.length > 0 && <span className="rounded-full bg-(--primary-wMain) px-2 py-0.5 text-xs text-(--primary-text-w10)">{selectedStatuses.length}</span>}
                                    </Button>

                                    {showStatusDropdown && (
                                        <>
                                            <div className="fixed inset-0 z-40" onClick={() => setShowStatusDropdown(false)} />

                                            <div className="absolute top-full right-0 z-50 mt-1 max-h-[300px] min-w-[200px] overflow-y-auto rounded-md border border-(--secondary-w20) bg-(--background-w10) p-1 shadow-md sm:right-auto sm:left-0">
                                                {selectedStatuses.length > 0 && (
                                                    <div className="border-b">
                                                        <button
                                                            onClick={clearStatuses}
                                                            className="flex min-h-[24px] w-full cursor-pointer items-center gap-1 px-3 py-2 text-left text-xs text-(--secondary-text-wMain) transition-colors hover:bg-(--secondary-w10) hover:text-(--primary-text-wMain)"
                                                        >
                                                            <X size={14} />
                                                            {selectedStatuses.length === 1 ? "Clear Filter" : "Clear Filters"}
                                                        </button>
                                                    </div>
                                                )}
                                                <div className="p-1">
                                                    {statuses.map(status => (
                                                        <label key={status} className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 hover:bg-(--secondary-w10)">
                                                            <input type="checkbox" checked={selectedStatuses.includes(status)} onChange={() => toggleStatus(status)} className="rounded" />
                                                            <span className="text-sm">{status}</span>
                                                        </label>
                                                    ))}
                                                </div>
                                            </div>
                                        </>
                                    )}
                                </div>

                                {hasActiveFilters && (
                                    <Button variant="ghost" onClick={clearAllFilters} data-testid="clearAllFiltersButton">
                                        <X size={16} />
                                        Clear All
                                    </Button>
                                )}
                            </div>
                        )}

                        {(() => {
                            if (filteredTasks.length === 0 && hasActiveFilters) {
                                return (
                                    <EmptyState
                                        title="No Tasks Match Your Filter"
                                        subtitle="Try adjusting your filter terms."
                                        variant="notFound"
                                        buttons={[
                                            {
                                                text: "Clear All Filters",
                                                variant: "default",
                                                onClick: clearAllFilters,
                                            },
                                        ]}
                                    />
                                );
                            }
                            if (isLibraryEmpty) {
                                return <EmptyState title="No Scheduled Tasks Found" subtitle="Create scheduled tasks to automate agent workflows." variant="noImage" buttons={createButtons} />;
                            }
                            return (
                                <div className="flex-1 overflow-y-auto sm:pr-6">
                                    <div className="grid grid-cols-[repeat(auto-fill,minmax(min(320px,100%),1fr))] gap-6">
                                        <CreateTaskCard onManualCreate={onManualCreate} onAIAssisted={onAIAssisted} />

                                        {filteredTasks.map(task => (
                                            <div key={task.id} data-task-id={task.id}>
                                                <TaskCard
                                                    task={task}
                                                    isSelected={selectedTask?.id === task.id}
                                                    onTaskClick={() => handleTaskClick(task)}
                                                    onEdit={onEdit}
                                                    onDelete={onDelete}
                                                    onToggleEnabled={onToggleEnabled}
                                                    onViewExecutions={onViewExecutions}
                                                    onRunNow={onRunNow}
                                                    isRunNowPending={runNowPendingTaskId === task.id}
                                                />
                                            </div>
                                        ))}
                                    </div>
                                </div>
                            );
                        })()}
                    </div>
                </ResizablePanel>

                {!isMobile && selectedTask && (
                    <>
                        <ResizableHandle />
                        <ResizablePanel defaultSize="30%" minSize="20%" maxSize="50%" id="taskDetailSidePanel">
                            <TaskDetailSidePanel
                                task={selectedTask}
                                onClose={handleCloseSidePanel}
                                onEdit={onEdit}
                                onDelete={taskId => {
                                    onDelete(taskId);
                                    handleCloseSidePanel();
                                }}
                                onViewExecutions={onViewExecutions}
                                onToggleEnabled={onToggleEnabled}
                                onRunNow={onRunNow}
                                isRunNowPending={runNowPendingTaskId === selectedTask.id}
                            />
                        </ResizablePanel>
                    </>
                )}
            </ResizablePanelGroup>

            {isMobile && (
                <Sheet
                    open={!!selectedTask}
                    onOpenChange={open => {
                        if (!open) handleCloseSidePanel();
                    }}
                >
                    <SheetContent side="right" className="w-full p-0" showClose={false}>
                        <div className="h-full overflow-y-auto">
                            {selectedTask && (
                                <TaskDetailSidePanel
                                    task={selectedTask}
                                    onClose={handleCloseSidePanel}
                                    onEdit={onEdit}
                                    onDelete={taskId => {
                                        onDelete(taskId);
                                        handleCloseSidePanel();
                                    }}
                                    onViewExecutions={onViewExecutions}
                                    onToggleEnabled={onToggleEnabled}
                                    onRunNow={onRunNow}
                                    isRunNowPending={runNowPendingTaskId === selectedTask.id}
                                />
                            )}
                        </div>
                    </SheetContent>
                </Sheet>
            )}
        </div>
    );
};
