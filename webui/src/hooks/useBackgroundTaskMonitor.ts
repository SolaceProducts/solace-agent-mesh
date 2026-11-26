// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

import { api } from "@/api/client";

import type { ActiveBackgroundTasksResponse, BackgroundTaskNotification, BackgroundTaskState, BackgroundTaskStatusResponse } from "@/types/background-tasks";

const STORAGE_KEY = "sam_background_tasks";
const REGISTRATION_GRACE_MS = 3000;
const POLL_INTERVAL_MS = 10_000;
const SSE_STALE_MS = 60_000;

type TerminationKind = "completed" | "failed" | "timeout";

function logFetchFailure(error: unknown): null {
    if (error instanceof DOMException && error.name === "AbortError") {
        return null;
    }
    console.error(`[BackgroundTaskMonitor] Failed to fetch task status:`, error);
    return null;
}

function classifyTaskTermination(status: BackgroundTaskStatusResponse): { type: TerminationKind; message: string } {
    const taskStatus = status.task.status;
    if (taskStatus === "failed" || taskStatus === "error") {
        return { type: "failed", message: status.errorMessage || "Background task failed" };
    }
    if (taskStatus === "timeout") {
        return { type: "timeout", message: status.errorMessage || "Background task timed out" };
    }
    return { type: "completed", message: "Background task completed" };
}

const TRANSIENT_LLM_ERROR_RE = /overloaded|status 50\d|MidStreamFallback|rate ?limit|temporarily unavailable/i;

function shouldSuppressFailureToast(notificationType: TerminationKind, status: BackgroundTaskStatusResponse, task: BackgroundTaskState, tracked: BackgroundTaskState[]): boolean {
    if (notificationType !== "failed") return false;
    const isTransientLlmError = !!status.errorMessage && TRANSIENT_LLM_ERROR_RE.test(status.errorMessage);
    const hasSiblingTaskRunning = !!task.sessionId && tracked.some(t => t.taskId !== task.taskId && t.sessionId === task.sessionId);
    return isTransientLlmError && hasSiblingTaskRunning;
}

function sseCoversTask(task: BackgroundTaskState, activeSessionId: string, now: number): boolean {
    return !!task.sessionId && task.sessionId === activeSessionId && now - task.lastEventTimestamp < SSE_STALE_MS;
}

export function selectTerminationCandidates(tracked: BackgroundTaskState[], activeTaskIds: Set<string>, activeSessionId: string, now: number): BackgroundTaskState[] {
    const terminals: BackgroundTaskState[] = [];
    for (const task of tracked) {
        if (sseCoversTask(task, activeSessionId, now)) continue;
        if (activeTaskIds.has(task.taskId)) continue;
        if (now - task.registeredAt >= REGISTRATION_GRACE_MS) {
            terminals.push(task);
        }
    }
    return terminals;
}

interface UseBackgroundTaskMonitorProps {
    userId: string | null;
    currentSessionId: string;
    onTaskCompleted?: (taskId: string, sessionId: string) => void;
    onTaskFailed?: (taskId: string, error: string, sessionId: string) => void;
}

export function useBackgroundTaskMonitor({ userId, currentSessionId, onTaskCompleted, onTaskFailed }: UseBackgroundTaskMonitorProps) {
    const [backgroundTasks, setBackgroundTasks] = useState<BackgroundTaskState[]>([]);
    const [notifications, setNotifications] = useState<BackgroundTaskNotification[]>([]);

    const backgroundTasksRef = useRef<BackgroundTaskState[]>(backgroundTasks);
    const currentSessionIdRef = useRef(currentSessionId);

    useEffect(() => {
        currentSessionIdRef.current = currentSessionId;
    }, [currentSessionId]);

    useEffect(() => {
        backgroundTasksRef.current = backgroundTasks;
    }, [backgroundTasks]);

    useEffect(() => {
        const stored = localStorage.getItem(STORAGE_KEY);
        if (stored) {
            try {
                const parsed = JSON.parse(stored) as BackgroundTaskState[];
                setBackgroundTasks(parsed);
            } catch (error) {
                console.error("[BackgroundTaskMonitor] Failed to parse stored tasks:", error);
                localStorage.removeItem(STORAGE_KEY);
            }
        }
    }, []);

    useEffect(() => {
        if (backgroundTasks.length > 0) {
            localStorage.setItem(STORAGE_KEY, JSON.stringify(backgroundTasks));
        } else {
            localStorage.removeItem(STORAGE_KEY);
        }
    }, [backgroundTasks]);

    const registerBackgroundTask = useCallback((taskId: string, sessionId: string, agentName?: string) => {
        const now = Date.now();
        const newTask: BackgroundTaskState = {
            taskId,
            sessionId,
            lastEventTimestamp: now,
            isBackground: true,
            startTime: now,
            registeredAt: now,
            agentName,
        };

        setBackgroundTasks(prev => {
            if (prev.some(t => t.taskId === taskId)) {
                return prev;
            }
            return [...prev, newTask];
        });
    }, []);

    const unregisterBackgroundTask = useCallback((taskId: string) => {
        setBackgroundTasks(prev => {
            const filtered = prev.filter(t => t.taskId !== taskId);
            return filtered;
        });
    }, []);

    const updateTaskTimestamp = useCallback((taskId: string, timestamp: number) => {
        setBackgroundTasks(prev => prev.map(task => (task.taskId === taskId ? { ...task, lastEventTimestamp: timestamp } : task)));
    }, []);

    const checkTaskStatus = useCallback(
        async (taskId: string, signal?: AbortSignal): Promise<BackgroundTaskStatusResponse | null> => {
            try {
                const response = await api.webui.get(`/api/v1/tasks/${taskId}/status`, { fullResponse: true, signal });
                if (!response.ok) {
                    if (response.status === 404) {
                        unregisterBackgroundTask(taskId);
                    }
                    return null;
                }
                return await response.json();
            } catch (error: unknown) {
                return logFetchFailure(error);
            }
        },
        [unregisterBackgroundTask]
    );

    const fetchActiveBackgroundTasks = useCallback(
        async (signal?: AbortSignal): Promise<BackgroundTaskState[] | null> => {
            if (!userId) {
                return null;
            }

            try {
                const data: ActiveBackgroundTasksResponse = await api.webui.get(`/api/v1/tasks/background/active`, { signal });

                const now = Date.now();
                return data.data.map(task => {
                    const startTimeMs = new Date(task.startTime).getTime();
                    return {
                        taskId: task.id,
                        sessionId: task.sessionId ?? "",
                        lastEventTimestamp: startTimeMs,
                        isBackground: true,
                        startTime: startTimeMs,
                        registeredAt: now,
                    };
                });
            } catch (error) {
                return logFetchFailure(error);
            }
        },
        [userId]
    );

    const handleTerminalTask = useCallback(
        async (task: BackgroundTaskState, signal: AbortSignal) => {
            const status = await checkTaskStatus(task.taskId, signal);
            if (signal.aborted || !status || status.isRunning) {
                return;
            }

            if (task.sessionId === currentSessionIdRef.current && Date.now() - task.lastEventTimestamp < SSE_STALE_MS) {
                return;
            }

            const { type: notificationType, message } = classifyTaskTermination(status);

            const suppressToast = shouldSuppressFailureToast(notificationType, status, task, backgroundTasksRef.current);

            if (!suppressToast) {
                const notification: BackgroundTaskNotification = {
                    taskId: task.taskId,
                    type: notificationType,
                    message,
                    timestamp: Date.now(),
                };

                setNotifications(prev => [...prev, notification]);

                if (notificationType === "completed" && onTaskCompleted) {
                    onTaskCompleted(task.taskId, task.sessionId);
                } else if (notificationType !== "completed" && onTaskFailed) {
                    onTaskFailed(task.taskId, message, task.sessionId);
                }
            } else {
                console.debug(`[BackgroundTaskMonitor] Suppressing transient LLM failure toast for task ${task.taskId} — sibling task is still running for session ${task.sessionId}`);
            }

            unregisterBackgroundTask(task.taskId);
        },
        [checkTaskStatus, onTaskCompleted, onTaskFailed, unregisterBackgroundTask]
    );

    const runPollTick = useCallback(
        async (signal: AbortSignal) => {
            const tasks = backgroundTasksRef.current;
            if (tasks.length === 0) {
                return;
            }

            const now = Date.now();
            const someTaskNeedsPolling = tasks.some(t => !sseCoversTask(t, currentSessionIdRef.current, now));
            if (!someTaskNeedsPolling) {
                return;
            }

            const serverTasks = await fetchActiveBackgroundTasks(signal);
            if (signal.aborted || serverTasks === null) {
                return;
            }
            const activeTaskIds = new Set(serverTasks.map(t => t.taskId));
            const taskTerminationCandidates = selectTerminationCandidates(tasks, activeTaskIds, currentSessionIdRef.current, Date.now());

            for (const task of taskTerminationCandidates) {
                if (signal.aborted) return;
                await handleTerminalTask(task, signal);
            }
        },
        [fetchActiveBackgroundTasks, handleTerminalTask]
    );

    const tickRef = useRef<(signal: AbortSignal) => Promise<void>>(runPollTick);
    useEffect(() => {
        tickRef.current = runPollTick;
    }, [runPollTick]);

    useEffect(() => {
        if (!userId) {
            return;
        }

        const controller = new AbortController();

        const checkForRunningTasks = async () => {
            const serverTasks = await fetchActiveBackgroundTasks(controller.signal);
            if (controller.signal.aborted || serverTasks === null) {
                return;
            }

            setBackgroundTasks(prev => {
                const merged = [...prev];

                for (const serverTask of serverTasks) {
                    if (!merged.some(t => t.taskId === serverTask.taskId)) {
                        merged.push(serverTask);
                    }
                }

                return merged;
            });
        };

        checkForRunningTasks();

        return () => {
            controller.abort();
        };
    }, [userId, fetchActiveBackgroundTasks]);

    const hasBackgroundTasks = backgroundTasks.length > 0;

    useEffect(() => {
        if (!hasBackgroundTasks) {
            return;
        }

        const isPollingRef = { current: false };
        let currentController: AbortController | null = null;

        const tick = async () => {
            if (isPollingRef.current) {
                return;
            }
            isPollingRef.current = true;
            const controller = new AbortController();
            currentController = controller;
            try {
                await tickRef.current(controller.signal);
            } finally {
                isPollingRef.current = false;
                if (currentController === controller) {
                    currentController = null;
                }
            }
        };

        const intervalId = setInterval(tick, POLL_INTERVAL_MS);

        return () => {
            clearInterval(intervalId);
            if (currentController) {
                currentController.abort();
            }
        };
    }, [hasBackgroundTasks]);

    const dismissNotification = useCallback((taskId: string) => {
        setNotifications(prev => prev.filter(n => n.taskId !== taskId));
    }, []);

    const getSessionBackgroundTasks = useCallback(
        (sessionId: string) => {
            return backgroundTasks.filter(t => t.sessionId === sessionId);
        },
        [backgroundTasks]
    );

    const isTaskRunningInBackground = useCallback(
        (taskId: string) => {
            return backgroundTasks.some(t => t.taskId === taskId);
        },
        [backgroundTasks]
    );

    return {
        backgroundTasks,
        notifications,
        registerBackgroundTask,
        unregisterBackgroundTask,
        updateTaskTimestamp,
        checkTaskStatus,
        dismissNotification,
        getSessionBackgroundTasks,
        isTaskRunningInBackground,
    };
}
