// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { sanitizeRagData } from "@/utils/sourceUrlHelpers";

import type { RAGSearchResult } from "@/types";

interface TaskWithMetadata {
    taskId?: string;
    id?: string;
    workflowTaskId?: string;
    taskMetadata?: { ragData?: unknown[]; [key: string]: unknown } | string | null;
}

export function parseTaskMetadata(raw: unknown): Record<string, unknown> | null {
    if (!raw) return null;
    if (typeof raw === "object") return raw as Record<string, unknown>;
    if (typeof raw === "string") {
        try {
            return JSON.parse(raw);
        } catch {
            return null;
        }
    }
    return null;
}

export function extractRagDataFromTasks(tasks: TaskWithMetadata[]): RAGSearchResult[] {
    const allRagData: RAGSearchResult[] = [];

    for (const task of tasks) {
        const taskId = task.workflowTaskId || task.taskId || task.id || "";
        const metadata = parseTaskMetadata(task.taskMetadata);

        if (metadata && Array.isArray(metadata.ragData)) {
            for (const ragEntry of metadata.ragData) {
                allRagData.push({
                    ...(ragEntry as RAGSearchResult),
                    taskId,
                });
            }
        }
    }

    return sanitizeRagData(allRagData);
}
