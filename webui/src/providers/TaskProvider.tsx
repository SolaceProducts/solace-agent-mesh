// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { type ReactNode, useCallback, useEffect, useMemo, useRef, useState } from "react";

import { api, type ApiError } from "@/api/client";
import { type PersistedTaskRow, recentTasksPath, rowToTaskFE, stripGatewayTimestamp } from "@/api/tasks/types";

import { TaskContext, type TaskContextValue } from "@/providers/contexts/TaskContext";

import type { A2AEventSSEPayload, TaskFE } from "@/types";

interface TaskProviderProps {
    children: ReactNode;
}

const isRefusal = (error: unknown): boolean => {
    const status = (error as ApiError | null)?.status;
    return status === 403 || status === 404;
};

const without = (set: ReadonlySet<string>, id: string): ReadonlySet<string> => {
    if (!set.has(id)) return set;
    const next = new Set(set);
    next.delete(id);
    return next;
};

export const TaskProvider: React.FC<TaskProviderProps> = ({ children }) => {
    const [monitoredTasks, setMonitoredTasks] = useState<Record<string, TaskFE>>({});
    const [monitoredTaskOrder, setMonitoredTaskOrder] = useState<string[]>([]);
    const [highlightedStepId, setHighlightedStepIdState] = useState<string | null>(null);
    const [unavailableTaskIds, setUnavailableTaskIds] = useState<ReadonlySet<string>>(() => new Set());
    const [failedTaskIds, setFailedTaskIds] = useState<ReadonlySet<string>>(() => new Set());
    const inFlightLoadsRef = useRef<Map<string, Promise<TaskFE | null>>>(new Map());

    const setHighlightedStepId = useCallback((stepId: string | null) => {
        setHighlightedStepIdState(stepId);
    }, []);

    const loadRecentTasks = useCallback(async (pageSize = 50): Promise<void> => {
        try {
            const resp = await api.webui.get<{ data?: PersistedTaskRow[] }>(recentTasksPath(pageSize));
            const rows = Array.isArray(resp?.data) ? resp.data : [];
            if (rows.length === 0) return;

            const seeded: Record<string, TaskFE> = {};
            const orderFromBackend: string[] = [];
            for (const row of rows) {
                if (!row.id) continue;
                seeded[row.id] = rowToTaskFE(row);
                orderFromBackend.push(row.id);
            }

            setMonitoredTasks(prev => {
                const merged = { ...prev };
                for (const [tid, row] of Object.entries(seeded)) {
                    merged[tid] ??= row;
                }
                return merged;
            });
            setMonitoredTaskOrder(prev => {
                const seen = new Set(prev);
                const appended = orderFromBackend.filter(id => !seen.has(id));
                return [...prev, ...appended];
            });
        } catch (err) {
            console.error("TaskProvider: loadRecentTasks failed:", err);
        }
    }, []);

    const loadTaskFromBackend = useCallback(async (taskId: string): Promise<TaskFE | null> => {
        if (taskId.startsWith("manual-compaction-")) {
            return null;
        }

        const inFlight = inFlightLoadsRef.current.get(taskId);
        if (inFlight) return inFlight;

        setUnavailableTaskIds(prev => without(prev, taskId));
        setFailedTaskIds(prev => without(prev, taskId));
        const load = (async (): Promise<TaskFE | null> => {
            try {
                const resp = await api.webui.get<{
                    data?: Array<{ taskId: string; events: A2AEventSSEPayload[]; initialRequestText?: string; sessionId?: string }>;
                }>(`/api/v1/tasks/${taskId}/events`);
                const taskEntries = Array.isArray(resp?.data) ? resp.data : [];
                const loadedTasks: Record<string, TaskFE> = {};

                for (const entry of taskEntries) {
                    const tid = entry.taskId;
                    if (!tid) continue;
                    const events = entry.events ?? [];
                    const rawRequestText = entry.initialRequestText || "Task loaded from history";
                    const taskFE: TaskFE = {
                        taskId: tid,
                        initialRequestText: stripGatewayTimestamp(rawRequestText),
                        events: events,
                        firstSeen: new Date(events[0]?.timestamp || Date.now()),
                        lastUpdated: new Date(events[events.length - 1]?.timestamp || Date.now()),
                        sessionId: entry.sessionId,
                    };
                    loadedTasks[tid] = taskFE;
                }

                setMonitoredTasks(prevTasks => {
                    const updated = { ...prevTasks };
                    for (const [tid, loadedTask] of Object.entries(loadedTasks)) {
                        const existing = prevTasks[tid];
                        updated[tid] = existing
                            ? { ...existing, ...loadedTask, initialRequestText: loadedTask.initialRequestText || existing.initialRequestText, sessionId: loadedTask.sessionId ?? existing.sessionId, needsHistoricalHydration: false }
                            : { ...loadedTask, needsHistoricalHydration: false };
                    }
                    return updated;
                });

                setMonitoredTaskOrder(prevOrder => {
                    if (prevOrder.includes(taskId)) {
                        return prevOrder;
                    }
                    return [taskId, ...prevOrder];
                });

                return loadedTasks[taskId] || null;
            } catch (error) {
                console.error(`TaskProvider: Error loading task ${taskId} from backend:`, error);
                if (isRefusal(error)) {
                    setUnavailableTaskIds(prev => new Set(prev).add(taskId));
                } else {
                    setFailedTaskIds(prev => new Set(prev).add(taskId));
                }
                return null;
            } finally {
                inFlightLoadsRef.current.delete(taskId);
            }
        })();

        inFlightLoadsRef.current.set(taskId, load);
        return load;
    }, []);

    const registerTaskEarly = useCallback((taskId: string, initialRequestText: string) => {
        setMonitoredTasks(prevTasks => {
            if (prevTasks[taskId]) {
                return prevTasks;
            }

            const now = new Date();
            const newTask: TaskFE = {
                taskId,
                initialRequestText,
                events: [],
                firstSeen: now,
                lastUpdated: now,
            };

            setMonitoredTaskOrder(prevOrder => [taskId, ...prevOrder.filter(id => id !== taskId)]);
            return { ...prevTasks, [taskId]: newTask };
        });
    }, []);

    useEffect(() => {
        void loadRecentTasks();
    }, [loadRecentTasks]);

    const contextValue = useMemo<TaskContextValue>(
        () => ({
            monitoredTasks,
            unavailableTaskIds,
            failedTaskIds,
            monitoredTaskOrder,
            highlightedStepId,
            setHighlightedStepId,
            loadTaskFromBackend,
            loadRecentTasks,
            registerTaskEarly,
        }),
        [monitoredTasks, unavailableTaskIds, failedTaskIds, monitoredTaskOrder, highlightedStepId, setHighlightedStepId, loadTaskFromBackend, loadRecentTasks, registerTaskEarly]
    );

    return <TaskContext.Provider value={contextValue}>{children}</TaskContext.Provider>;
};
