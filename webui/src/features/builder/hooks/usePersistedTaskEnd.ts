// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";

import { api } from "@/api/client";

import type { MessageFE } from "@/types";

const ABNORMAL_TASK_STATUSES: ReadonlySet<string | undefined> = new Set(["canceled", "failed", "timeout", "error"]);

const RETRY_DELAYS_MS: readonly number[] = [1500, 4000, 8000];

const NOT_FOUND = 404;

interface TaskStatusResponse {
    task?: { status?: string };
    isRunning?: boolean;
}

interface TaskEndRecord {
    taskId: string;
    abnormal: boolean;
}

type TaskEnd = "abnormal" | "normal" | "unsettled";

function lastTaskIdOf(messages: readonly MessageFE[] | undefined): string | undefined {
    return messages?.findLast(m => !!m.taskId)?.taskId;
}

function classifyTaskEnd(response: TaskStatusResponse | null | undefined): TaskEnd {
    if (response?.isRunning) return "unsettled";
    const status = response?.task?.status;
    if (ABNORMAL_TASK_STATUSES.has(status)) return "abnormal";
    return status ? "normal" : "unsettled";
}

async function readTaskEnd(taskId: string): Promise<TaskEnd> {
    try {
        return classifyTaskEnd(await api.webui.get<TaskStatusResponse>(`/api/v1/tasks/${encodeURIComponent(taskId)}/status`));
    } catch (error) {
        const status = (error as { status?: number }).status;
        if (status !== NOT_FOUND) console.warn("[builder] could not read how the task ended; treating it as ended normally unless a retry says otherwise", taskId, status ?? error);
        return "unsettled";
    }
}

export function usePersistedTaskEnd({ messages, enabled }: Readonly<{ messages: readonly MessageFE[] | undefined; enabled: boolean }>): boolean {
    const taskId = lastTaskIdOf(messages);
    const [record, setRecord] = useState<TaskEndRecord | null>(null);

    useEffect(() => {
        if (!enabled || !taskId) return;
        let superseded = false;
        let retryTimer: ReturnType<typeof setTimeout> | undefined;

        const attempt = async (retriesDone: number) => {
            const end = await readTaskEnd(taskId);
            if (superseded) return;
            if (end === "unsettled" && retriesDone < RETRY_DELAYS_MS.length) {
                retryTimer = setTimeout(() => void attempt(retriesDone + 1), RETRY_DELAYS_MS[retriesDone]);
                return;
            }
            setRecord({ taskId, abnormal: end === "abnormal" });
        };
        void attempt(0);

        return () => {
            superseded = true;
            clearTimeout(retryTimer);
        };
    }, [enabled, taskId]);

    return enabled && record !== null && record.taskId === taskId && record.abnormal;
}
