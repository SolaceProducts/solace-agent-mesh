// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useDisableScheduledTask, useEnableScheduledTask, useRunScheduledTaskNow } from "@/api/scheduled-tasks";

import { useChatContext } from "@/providers/contexts";

import { reportAndNotifyError } from "@/common/product-analytics/reportAndNotifyError";

import type { ScheduledTask } from "@/types/scheduled-tasks";

export function useScheduledTaskActions() {
    const { addNotification } = useChatContext();
    const enableMutation = useEnableScheduledTask();
    const disableMutation = useDisableScheduledTask();
    const runNowMutation = useRunScheduledTaskNow();

    const toggleEnabled = async (task: ScheduledTask) => {
        const pausing = task.enabled;
        try {
            await (pausing ? disableMutation.mutateAsync(task.id) : enableMutation.mutateAsync(task.id));
        } catch (err: unknown) {
            const message = (err instanceof Error && err.message) || "Failed to update task";
            reportAndNotifyError(addNotification, `${pausing ? "Pause" : "Resume"} failed: ${message}`, "api");
        }
    };

    const runNow = async (task: ScheduledTask) => {
        try {
            await runNowMutation.mutateAsync(task.id);
            addNotification(`"${task.name}" started — check execution history for results.`, "success");
        } catch (err: unknown) {
            const message = (err instanceof Error && err.message) || "Failed to run task";
            reportAndNotifyError(addNotification, `Run Now failed: ${message}`, "api");
        }
    };

    return {
        toggleEnabled,
        runNow,
        isRunNowPending: runNowMutation.isPending,
        runNowPendingTaskId: runNowMutation.isPending ? ((runNowMutation.variables as string | undefined) ?? null) : null,
    };
}
