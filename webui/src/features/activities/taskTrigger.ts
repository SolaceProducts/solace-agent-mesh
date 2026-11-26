// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { TaskFE, TaskTrigger } from "@/types";

function triggerSummary(trigger: TaskTrigger | undefined): string | undefined {
    if (!trigger) return undefined;
    const parts = [trigger.channel ?? trigger.kind, trigger.entrypointName, trigger.event].filter(Boolean);
    return parts.length > 0 ? parts.join(" · ") : undefined;
}

export function taskOriginLine(task: Pick<TaskFE, "subject" | "trigger" | "parentTaskId">): string | undefined {
    const parts = [task.subject?.name, task.parentTaskId ? undefined : triggerSummary(task.trigger)].filter(Boolean);
    return parts.length > 0 ? parts.join(" · ") : undefined;
}
