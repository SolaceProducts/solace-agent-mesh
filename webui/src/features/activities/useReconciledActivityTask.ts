// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

import { useTaskContext } from "@/providers/contexts";

import { eventTreeIsComplete, mergeDedupeEvents, REHYDRATE_DELAY_MS, REHYDRATE_MAX_ATTEMPTS } from "@/features/activities/taskEventMerge";
import { processTaskForVisualization } from "@/features/activities/taskVisualizerProcessor";

import type { TaskFE, VisualizedTask } from "@/types";

export interface ReconciledActivityTask {
    visualizedTask: VisualizedTask | null;
    isLoadingTask: boolean;
    reload: () => void;
}

export function useReconciledActivityTask(taskId: string | null, liveTasks: Record<string, TaskFE>, isTaskActive = false): ReconciledActivityTask {
    const { monitoredTasks, loadTaskFromBackend } = useTaskContext();
    const [visualizedTask, setVisualizedTask] = useState<VisualizedTask | null>(null);
    const [isLoadingTask, setIsLoadingTask] = useState(false);
    const loadAttemptedRef = useRef<Set<string>>(new Set());
    const rehydrateAttemptsRef = useRef<Map<string, number>>(new Map());

    const reload = useCallback(() => {
        if (!taskId) return;
        setIsLoadingTask(true);
        loadTaskFromBackend(taskId).finally(() => setIsLoadingTask(false));
    }, [taskId, loadTaskFromBackend]);

    useEffect(() => {
        if (!taskId) {
            setVisualizedTask(null);
            return;
        }

        const live = liveTasks[taskId];
        const backfill = monitoredTasks[taskId];
        const hasLive = (live?.events?.length ?? 0) > 0;
        const hasBackfill = (backfill?.events?.length ?? 0) > 0;
        let existingTask: TaskFE | undefined;
        if (hasLive && hasBackfill) {
            existingTask = { ...backfill, events: mergeDedupeEvents(backfill.events, live.events), lastUpdated: live.lastUpdated };
        } else if (hasBackfill) {
            existingTask = backfill;
        } else {
            existingTask = live ?? backfill;
        }

        const tasksById: Record<string, TaskFE> = { ...monitoredTasks, ...liveTasks };
        if (existingTask) {
            tasksById[taskId] = existingTask;
        }

        if (existingTask && existingTask.events.length > 0) {
            setVisualizedTask(processTaskForVisualization(existingTask.events, tasksById, existingTask));
        } else if (loadAttemptedRef.current.has(taskId)) {
            setVisualizedTask(null);
        }

        if (!loadAttemptedRef.current.has(taskId)) {
            loadAttemptedRef.current.add(taskId);
            if ((existingTask?.events.length ?? 0) === 0) {
                setIsLoadingTask(true);
            }
            loadTaskFromBackend(taskId).finally(() => setIsLoadingTask(false));
        }
    }, [taskId, monitoredTasks, liveTasks, loadTaskFromBackend]);

    useEffect(() => {
        if (!taskId || !loadAttemptedRef.current.has(taskId)) return;
        const backfill = monitoredTasks[taskId];
        if (backfill?.needsHistoricalHydration) return;
        if (eventTreeIsComplete(backfill?.events)) return;
        if (isTaskActive) return;
        const live = liveTasks[taskId];
        const hasLiveSubscription = (live?.events?.length ?? 0) > 0;
        if (hasLiveSubscription && !eventTreeIsComplete(live?.events)) return;
        const attempts = rehydrateAttemptsRef.current.get(taskId) ?? 0;
        if (attempts >= REHYDRATE_MAX_ATTEMPTS) return;
        const timer = setTimeout(() => {
            rehydrateAttemptsRef.current.set(taskId, attempts + 1);
            void loadTaskFromBackend(taskId);
        }, REHYDRATE_DELAY_MS);
        return () => clearTimeout(timer);
    }, [taskId, monitoredTasks, liveTasks, isTaskActive, loadTaskFromBackend]);

    useEffect(() => {
        if (!taskId) return;
        const loadAttempted = loadAttemptedRef.current;
        const rehydrateAttempts = rehydrateAttemptsRef.current;
        return () => {
            setTimeout(() => {
                loadAttempted.delete(taskId);
                rehydrateAttempts.delete(taskId);
            }, 1000);
        };
    }, [taskId]);

    return { visualizedTask, isLoadingTask, reload };
}
