// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { TaskFE } from "@/types";

import type { HierarchicalTaskItem } from "./TaskListPanel";

export interface SessionGroup {
    key: string;
    sessionId: string | null;
    name: string | null;
    tasks: HierarchicalTaskItem[];
    latestTask: HierarchicalTaskItem;
    turnCount: number;
    eventCount: number;
    tokenUsage: TokenUsage | null;
}

export interface TokenUsage {
    input: number;
    output: number;
}

export const taskEventCount = (task: Pick<TaskFE, "events" | "eventCount">): number => (task.events.length > 0 ? task.events.length : (task.eventCount ?? 0));

export const taskTokenUsage = (task: Pick<TaskFE, "inputTokens" | "outputTokens">): TokenUsage | null => (task.inputTokens === undefined && task.outputTokens === undefined ? null : { input: task.inputTokens ?? 0, output: task.outputTokens ?? 0 });

export const subtreeTokenUsage = (tasks: readonly HierarchicalTaskItem[]): TokenUsage | null => {
    const usages = tasks.flatMap(task => [taskTokenUsage(task), subtreeTokenUsage(task.children)]).filter((usage): usage is TokenUsage => usage !== null);
    if (usages.length === 0) return null;
    return usages.reduce((total, usage) => ({ input: total.input + usage.input, output: total.output + usage.output }), { input: 0, output: 0 });
};

const sessionKeyOf = (task: HierarchicalTaskItem): string => task.sessionId || `task:${task.taskId}`;

export const groupTasksBySession = (rootTasks: HierarchicalTaskItem[]): SessionGroup[] => {
    const tasksByKey = new Map<string, HierarchicalTaskItem[]>();
    for (const task of rootTasks) {
        const key = sessionKeyOf(task);
        const tasks = tasksByKey.get(key);
        if (tasks) {
            tasks.push(task);
        } else {
            tasksByKey.set(key, [task]);
        }
    }
    return [...tasksByKey.entries()].map(([key, tasks]) => ({
        key,
        sessionId: tasks[0].sessionId || null,
        name: tasks.find(task => task.sessionName)?.sessionName ?? null,
        tasks,
        latestTask: tasks[0],
        turnCount: tasks.length,
        eventCount: tasks.reduce((sum, task) => sum + taskEventCount(task), 0),
        tokenUsage: subtreeTokenUsage(tasks),
    }));
};
