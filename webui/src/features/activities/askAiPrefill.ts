// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { TaskFE } from "@/types";

export function buildTaskTracePrefill(task: Pick<TaskFE, "taskId" | "sessionId">): string {
    const sessionClause = task.sessionId ? ` in Session ID ${task.sessionId}` : "";
    return `Look up Task ID ${task.taskId}${sessionClause} and tell me what happened.`;
}
