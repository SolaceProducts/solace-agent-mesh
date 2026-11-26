// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createContext, useContext } from "react";

import type { TaskFE } from "@/types";

interface TaskState {
    monitoredTasks: Record<string, TaskFE>;
    monitoredTaskOrder: string[];
    highlightedStepId: string | null;
    unavailableTaskIds: ReadonlySet<string>;
    failedTaskIds: ReadonlySet<string>;
}

interface TaskActions {
    setHighlightedStepId: (stepId: string | null) => void;
    loadTaskFromBackend: (taskId: string) => Promise<TaskFE | null>;
    loadRecentTasks: (pageSize?: number) => Promise<void>;
    registerTaskEarly: (taskId: string, initialRequestText: string) => void;
}

export type TaskContextValue = TaskState & TaskActions;

export const TaskContext = createContext<TaskContextValue | undefined>(undefined);

export const useTaskContext = (): TaskContextValue => {
    const context = useContext(TaskContext);
    if (context === undefined) {
        throw new Error("useTaskContext must be used within a TaskProvider");
    }
    return context;
};
