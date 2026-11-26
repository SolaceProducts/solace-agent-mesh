// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { RefreshCw } from "lucide-react";
import { useNavigate } from "react-router-dom";

import { useAgentCards } from "@/api/agent-cards/hooks";
import { useAllArtifacts } from "@/api/artifacts/hooks";
import { isTaskRunning, useActivityTasks } from "@/api/tasks";

import { useChatContext, useTaskContext, useUserContext } from "@/providers/contexts";
import { useTaskActivityEvents } from "@/providers/task/useTaskActivityEvents";

import { ACTIVITY_MONITOR_DISPLAY_NAME, canActivityMonitorReadTask, hasTenantOrTraceReach, resolveActivityMonitor } from "@/utils/activityMonitor";

import { processTaskForVisualization } from "@/features/activities/taskVisualizerProcessor";

import { CustomCheckbox, EmptyState, Header, PageLayout } from "@/common";
import { useDebounce, useIsMobile } from "@/hooks";
import { type ArtifactInfo, type TaskFE, type VisualizedTask } from "@/types";
import { Button, ResizableHandle, ResizablePanel, ResizablePanelGroup, SearchInput, Sheet, SheetContent, SheetTitle } from "@/ui";
import { cn } from "@/utils";

import { buildTaskTracePrefill } from "./askAiPrefill";
import { PerformanceModal } from "./PerformanceModal";
import { groupTasksBySession } from "./sessionGrouping";
import SessionListPanel from "./SessionListPanel";
import { type AskAiAction, TaskDetailPanel, type TaskDetailState } from "./TaskDetailPanel";
import { eventTreeIsComplete, mergeDedupeEvents, REHYDRATE_DELAY_MS, REHYDRATE_MAX_ATTEMPTS } from "./taskEventMerge";
import { type HierarchicalTaskItem } from "./TaskListPanel";
import { viewArtifactHandler } from "./viewArtifactHandler";

const SEARCH_DEBOUNCE_MS = 300;

function withHydratedEvents(row: TaskFE, hydrated: TaskFE | undefined): TaskFE {
    if (!hydrated) return row;
    return {
        ...hydrated,
        userId: row.userId,
        sessionId: row.sessionId,
        sessionName: row.sessionName,
        status: row.status,
        endTime: row.endTime,
        eventCount: row.eventCount ?? hydrated.eventCount,
        subject: row.subject,
        trigger: row.trigger,
        parentTaskId: row.parentTaskId ?? hydrated.parentTaskId,
        inputTokens: row.inputTokens ?? hydrated.inputTokens,
        outputTokens: row.outputTokens ?? hydrated.outputTokens,
    };
}

const AGENT_LIST_UNAVAILABLE_ACTION: AskAiAction = { canStart: false, disabledReason: "Ask AI is unavailable because the agent list could not be loaded", onStart: () => {} };

const toggleMember = (set: Set<string>, member: string): Set<string> => {
    const next = new Set(set);
    if (next.has(member)) {
        next.delete(member);
    } else {
        next.add(member);
    }
    return next;
};

export const ActivitiesPage: React.FC = () => {
    const { monitoredTasks, unavailableTaskIds, failedTaskIds, loadTaskFromBackend } = useTaskContext();
    const { scopes, username, capabilitiesLoaded } = useUserContext();
    const currentUserId = username ?? null;
    const showOwnerFilter = currentUserId !== null && hasTenantOrTraceReach(scopes);
    const [mineOnly, setMineOnly] = useState(true);
    const [searchText, setSearchText] = useState("");
    const search = useDebounce(searchText.trim(), SEARCH_DEBOUNCE_MS);
    const ownerUserId = showOwnerFilter && mineOnly ? (currentUserId ?? undefined) : undefined;
    const {
        tasks: restTasks,
        isLoading: restTasksLoading,
        isPlaceholderData: restTasksPlaceholder,
        error: restTasksError,
        refetch: refetchRestTasks,
    } = useActivityTasks({ userId: ownerUserId, search: search || undefined, enabled: capabilitiesLoaded, pollWhileOpen: true });
    const [refreshing, setRefreshing] = useState(false);

    const tasksById = useMemo(() => {
        const byId: Record<string, TaskFE> = {};
        for (const task of restTasks) {
            byId[task.taskId] = withHydratedEvents(task, monitoredTasks[task.taskId]);
        }
        return byId;
    }, [restTasks, monitoredTasks]);
    const taskOrder = useMemo(() => restTasks.map(task => task.taskId), [restTasks]);

    const hydrationAttemptedRef = useRef<Set<string>>(new Set());
    const rehydrateAttemptsRef = useRef<Map<string, number>>(new Map());
    const chatContext = useChatContext();

    const [selectedTaskId, setSelectedTaskId] = useState<string | null>(null);
    const [selectedVisualizedTask, setSelectedVisualizedTask] = useState<VisualizedTask | null>(null);
    const [expandedSessionKeys, setExpandedSessionKeys] = useState<Set<string>>(new Set());
    const [expandedParentTaskIds, setExpandedParentTaskIds] = useState<Set<string>>(new Set());
    const [isPerformanceModalOpen, setIsPerformanceModalOpen] = useState(false);
    const [performanceModalTask, setPerformanceModalTask] = useState<VisualizedTask | null>(null);
    const isMobile = useIsMobile();
    const navigate = useNavigate();

    const { data: allArtifacts } = useAllArtifacts();

    const chatContextRef = useRef(chatContext);
    chatContextRef.current = chatContext;

    const artifactLookup = useCallback((filename: string): ArtifactInfo | undefined => (allArtifacts as unknown as ArtifactInfo[]).find(a => a.filename === filename), [allArtifacts]);

    const handleViewArtifact = useCallback((artifact: ArtifactInfo, version?: number) => viewArtifactHandler(artifact, version, chatContextRef.current, navigate), [navigate]);

    const { data: agentCards, isError: agentCardsUnavailable } = useAgentCards();
    const activityMonitor = resolveActivityMonitor(agentCards);
    const askAiFor = useCallback(
        (task: TaskFE): AskAiAction | undefined =>
            activityMonitor
                ? {
                      canStart: canActivityMonitorReadTask(task.userId, username, scopes),
                      onStart: () => navigate("/chat", { state: { promptText: buildTaskTracePrefill(task), groupId: `activity-trace-${task.taskId}`, groupName: ACTIVITY_MONITOR_DISPLAY_NAME, agentName: activityMonitor.name } }),
                  }
                : agentCardsUnavailable
                  ? AGENT_LIST_UNAVAILABLE_ACTION
                  : undefined,
        [activityMonitor, agentCardsUnavailable, username, scopes, navigate]
    );

    const toggleSessionExpansion = (sessionKey: string) => setExpandedSessionKeys(prev => toggleMember(prev, sessionKey));
    const toggleTaskExpansion = (taskId: string) => setExpandedParentTaskIds(prev => toggleMember(prev, taskId));
    const closeTaskDetails = useCallback(() => setSelectedTaskId(null), []);

    const handleOpenPerformanceModal = () => {
        if (selectedVisualizedTask) {
            setPerformanceModalTask(selectedVisualizedTask);
            setIsPerformanceModalOpen(true);
        }
    };

    const hierarchicalTasks = useMemo(() => {
        const getParentId = (task: TaskFE): string | null | undefined => {
            if (task.parentTaskId !== undefined) {
                return task.parentTaskId;
            }
            if (task.events && task.events.length > 0) {
                const firstEvent = task.events[0];
                if (firstEvent.full_payload?.params?.message?.metadata?.parentTaskId) {
                    return firstEvent.full_payload.params.message.metadata.parentTaskId;
                }
            }
            return null;
        };

        const buildTaskTree = (currentParentId: string | null, level: number): HierarchicalTaskItem[] => {
            const children: HierarchicalTaskItem[] = [];
            taskOrder.forEach((taskId: string) => {
                const task = tasksById[taskId];
                if (task) {
                    const taskParentId = getParentId(task);
                    if (taskParentId === currentParentId) {
                        const grandChildren = buildTaskTree(task.taskId, level + 1);
                        children.push({
                            ...task,
                            nestingLevel: level,
                            children: grandChildren,
                        });
                    }
                }
            });
            return children;
        };

        return buildTaskTree(null, 0);
    }, [tasksById, taskOrder]);

    const sessions = useMemo(() => groupTasksBySession(hierarchicalTasks), [hierarchicalTasks]);
    const sessionKeyList = sessions.map(session => session.key).join("\n");
    const sessionKeys = useMemo(() => (sessionKeyList === "" ? [] : sessionKeyList.split("\n")), [sessionKeyList]);

    useEffect(() => {
        if (search === "") return;
        setExpandedSessionKeys(new Set(sessionKeys));
    }, [search, sessionKeys]);

    const listFilters = `${ownerUserId ?? ""}\n${search}`;
    const selectedSeenUnderRef = useRef<{ taskId: string; filters: string } | null>(null);
    const listedSelectedRow = useMemo(() => restTasks.find(task => task.taskId === selectedTaskId), [restTasks, selectedTaskId]);
    const [lastListedRow, setLastListedRow] = useState<TaskFE>();
    if (listedSelectedRow && listedSelectedRow !== lastListedRow) setLastListedRow(listedSelectedRow);
    useEffect(() => {
        if (!selectedTaskId) return;
        if (tasksById[selectedTaskId]) {
            if (!restTasksPlaceholder) selectedSeenUnderRef.current = { taskId: selectedTaskId, filters: listFilters };
            return;
        }
        const seen = selectedSeenUnderRef.current;
        const filteredOut = !restTasksPlaceholder && (seen?.taskId !== selectedTaskId || seen.filters !== listFilters);
        if (filteredOut || !monitoredTasks[selectedTaskId]) {
            setSelectedTaskId(null);
        }
    }, [selectedTaskId, tasksById, monitoredTasks, listFilters, restTasksPlaceholder]);

    const selectedRow = useMemo<TaskFE | undefined>(() => {
        if (!selectedTaskId) return undefined;
        if (tasksById[selectedTaskId]) return tasksById[selectedTaskId];
        if (lastListedRow?.taskId !== selectedTaskId) return undefined;
        return withHydratedEvents(lastListedRow, monitoredTasks[selectedTaskId]);
    }, [selectedTaskId, tasksById, lastListedRow, monitoredTasks]);

    const isSelectedRunning = isTaskRunning(selectedRow);
    const liveActivityTasks = useTaskActivityEvents(selectedTaskId, isSelectedRunning);

    const selectedTaskDetails = useMemo<TaskFE | null>(() => {
        if (!selectedTaskId) return null;
        const backfilled = selectedRow ?? monitoredTasks[selectedTaskId] ?? null;
        if (!isSelectedRunning) return backfilled;
        const live = liveActivityTasks[selectedTaskId];
        if (!live?.events?.length) return backfilled;
        const base = backfilled ?? live;
        return { ...base, events: mergeDedupeEvents(backfilled?.events ?? [], live.events), lastUpdated: live.lastUpdated };
    }, [selectedTaskId, selectedRow, monitoredTasks, isSelectedRunning, liveActivityTasks]);

    const processingTasksById = useMemo(() => {
        if (!isSelectedRunning) return tasksById;
        const merged: Record<string, TaskFE> = { ...tasksById, ...liveActivityTasks };
        if (selectedTaskId && selectedTaskDetails) merged[selectedTaskId] = selectedTaskDetails;
        return merged;
    }, [isSelectedRunning, tasksById, liveActivityTasks, selectedTaskId, selectedTaskDetails]);

    useEffect(() => {
        if (selectedTaskDetails) {
            const vizTask = processTaskForVisualization(selectedTaskDetails.events || [], processingTasksById, selectedTaskDetails, chatContext.agentNameDisplayNameMap);
            setSelectedVisualizedTask(vizTask);

            if (selectedTaskDetails.needsHistoricalHydration && !hydrationAttemptedRef.current.has(selectedTaskDetails.taskId)) {
                const { taskId } = selectedTaskDetails;
                hydrationAttemptedRef.current.add(taskId);
                void loadTaskFromBackend(taskId).then(loaded => {
                    if (!loaded) hydrationAttemptedRef.current.delete(taskId);
                });
            }
        } else {
            setSelectedVisualizedTask(null);
        }
    }, [selectedTaskDetails, processingTasksById, loadTaskFromBackend, chatContext.agentNameDisplayNameMap]);

    const detailState = useMemo<TaskDetailState>(() => {
        if (!selectedTaskDetails) return "loading";
        if ((selectedVisualizedTask?.steps.length ?? 0) > 0) return "ready";
        if (unavailableTaskIds.has(selectedTaskDetails.taskId)) return "unavailable";
        if (failedTaskIds.has(selectedTaskDetails.taskId)) return "error";
        if (selectedTaskDetails.needsHistoricalHydration) return "loading";
        return "empty";
    }, [selectedTaskDetails, selectedVisualizedTask, unavailableTaskIds, failedTaskIds]);

    useEffect(() => {
        if (!selectedTaskId) return;
        const backfill = selectedRow;
        if (!backfill || backfill.needsHistoricalHydration) return;
        if (eventTreeIsComplete(backfill.events)) return;
        const attemptsKey = `${selectedTaskId}:${backfill.endTime ?? "running"}`;
        const attempts = rehydrateAttemptsRef.current.get(attemptsKey) ?? 0;
        if (attempts >= REHYDRATE_MAX_ATTEMPTS) return;
        const timer = setTimeout(() => {
            rehydrateAttemptsRef.current.set(attemptsKey, attempts + 1);
            void loadTaskFromBackend(selectedTaskId);
        }, REHYDRATE_DELAY_MS);
        return () => clearTimeout(timer);
    }, [selectedTaskId, selectedRow, loadTaskFromBackend]);

    const emptyStatusMessage = useMemo(() => {
        if (restTasksError) {
            return <EmptyState variant="error" title="Something went wrong" subtitle={restTasksError.message} />;
        }
        if (restTasksLoading || !capabilitiesLoaded) {
            return <p>Loading tasks...</p>;
        }
        if (search !== "") {
            return <EmptyState variant="notFound" title="No matching sessions" subtitle="Try another session, task or user ID, or clear the search" />;
        }
        if (ownerUserId !== undefined) {
            return <EmptyState variant="notFound" title="No sessions of yours yet" subtitle={`Clear "Show my sessions only" to see everyone's`} />;
        }
        return <EmptyState variant="notFound" title="No sessions yet" subtitle="Sessions appear here once a task runs. Start a chat to create one." buttons={[{ text: "Go To Chat", variant: "default", onClick: () => navigate("/chat") }]} />;
    }, [restTasksError, restTasksLoading, capabilitiesLoaded, search, ownerUserId, navigate]);

    const renderEmptyState = () => (
        <div className="flex h-full items-center justify-center p-4" data-testid="task-list-empty">
            <div className="text-(--secondary-text-wMain)">{emptyStatusMessage}</div>
        </div>
    );

    const renderOwnerFilter = () => (
        <div className="flex items-center gap-2 text-sm whitespace-nowrap" data-testid="task-owner-filter">
            <CustomCheckbox id="activities-mine-only" checked={mineOnly} onChange={e => setMineOnly(e.target.checked)} />
            <label htmlFor="activities-mine-only" className="cursor-pointer">
                Show my sessions only
            </label>
        </div>
    );

    const renderTaskListToolbar = (showDetailPanel: boolean) => (
        <div className={cn("flex shrink-0 flex-wrap items-center gap-2 pb-2", showDetailPanel ? "px-4 pt-4" : "pt-2")}>
            <SearchInput value={searchText} onChange={setSearchText} placeholder="Search by session, task or user ID" testid="task-search-input" className="min-w-48 flex-1" />
            {showOwnerFilter && renderOwnerFilter()}
        </div>
    );

    const refreshTasks = () => {
        setRefreshing(true);
        void refetchRestTasks().finally(() => setRefreshing(false));
    };
    const refreshButton = (
        <Button key="refresh" variant="ghost" className="px-3 sm:px-4" onClick={refreshTasks} disabled={refreshing} title="Refresh tasks" data-testid="refresh-tasks">
            <RefreshCw className={refreshing ? "animate-spin" : ""} />
            <span className="hidden sm:inline">Refresh</span>
        </Button>
    );

    const renderTaskDetails = () =>
        selectedTaskId &&
        selectedTaskDetails && (
            <TaskDetailPanel
                task={selectedTaskDetails}
                persistedTask={selectedRow ?? null}
                visualizedTask={selectedVisualizedTask}
                detailState={detailState}
                askAi={askAiFor(selectedTaskDetails)}
                onClose={closeTaskDetails}
                onOpenPerformance={handleOpenPerformanceModal}
                artifactLookup={artifactLookup}
                onViewArtifact={handleViewArtifact}
            />
        );

    const renderSessionsContent = () => {
        const taskDetails = renderTaskDetails();
        const showDetailPanel = !isMobile && !!taskDetails;

        return (
            <div className="min-h-0 flex-1">
                <ResizablePanelGroup orientation="horizontal" className="h-full">
                    <ResizablePanel id="activities-session-list" defaultSize={`${showDetailPanel ? 35 : 100}%`} minSize="20%" maxSize="100%">
                        <div data-testid="task-list-panel" className={cn("flex h-full flex-col", !showDetailPanel && "p-4 md:px-8 md:py-6")}>
                            {renderTaskListToolbar(showDetailPanel)}
                            <div className={cn("scrollbar-themed min-h-0 flex-1 overflow-y-auto", showDetailPanel && "border-t")}>
                                {sessions.length === 0 ? (
                                    renderEmptyState()
                                ) : (
                                    <SessionListPanel
                                        sessions={sessions}
                                        expandedSessionKeys={expandedSessionKeys}
                                        onToggleSession={toggleSessionExpansion}
                                        selectedTaskId={selectedTaskId}
                                        onSelectTask={setSelectedTaskId}
                                        expandedTaskIds={expandedParentTaskIds}
                                        onToggleExpand={toggleTaskExpansion}
                                        framed={!showDetailPanel}
                                    />
                                )}
                            </div>
                        </div>
                    </ResizablePanel>
                    {showDetailPanel && (
                        <>
                            <ResizableHandle />
                            <ResizablePanel id="activities-task-details" defaultSize="65%" minSize="35%" maxSize="80%">
                                {taskDetails}
                            </ResizablePanel>
                        </>
                    )}
                </ResizablePanelGroup>
                {isMobile && (
                    <Sheet open={!!taskDetails} onOpenChange={open => !open && closeTaskDetails()}>
                        <SheetContent side="right" className="w-full p-0 sm:max-w-full" showClose={false}>
                            <SheetTitle className="sr-only">Task details</SheetTitle>
                            {taskDetails}
                        </SheetContent>
                    </Sheet>
                )}
            </div>
        );
    };

    return (
        <PageLayout>
            <Header title="Activities" buttons={[refreshButton]} />
            {renderSessionsContent()}
            <PerformanceModal
                isOpen={isPerformanceModalOpen}
                onOpenChange={setIsPerformanceModalOpen}
                report={performanceModalTask?.performanceReport || null}
                isTaskComplete={performanceModalTask?.steps.some((step: { type: string }) => step.type === "TASK_COMPLETED") || false}
            />
        </PageLayout>
    );
};
