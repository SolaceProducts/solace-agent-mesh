// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

import type { MessageFE } from "@/types";

interface TurnEndInputs {
    messages: readonly MessageFE[] | undefined;
    isResponding: boolean;
    isCancelling: boolean;
    currentTaskId: string | null;
    sessionId: string;
    endedAbnormallyOnRecord?: boolean;
}

interface CancelRequest {
    taskId: string;
    sessionId: string;
}

interface StopMemory {
    wasResponding: boolean;
    sessionId: string;
    cancel: CancelRequest | null;
    stoppedTaskId: string | null;
}

function advanceStopMemory(memory: StopMemory, { isResponding, isCancelling, currentTaskId, sessionId }: Omit<TurnEndInputs, "messages" | "endedAbnormallyOnRecord">): StopMemory {
    let { cancel, stoppedTaskId } = memory;
    if (sessionId !== memory.sessionId) stoppedTaskId = null;
    if (isCancelling && currentTaskId) cancel ??= { taskId: currentTaskId, sessionId };
    else if (isResponding && !isCancelling) cancel = null;

    if (memory.wasResponding && !isResponding) {
        if (cancel?.sessionId === sessionId) stoppedTaskId = cancel.taskId;
        cancel = null;
    } else if (currentTaskId && stoppedTaskId !== null && stoppedTaskId !== currentTaskId) {
        stoppedTaskId = null;
    }

    const unchanged = memory.wasResponding === isResponding && memory.sessionId === sessionId && memory.cancel === cancel && memory.stoppedTaskId === stoppedTaskId;
    return unchanged ? memory : { wasResponding: isResponding, sessionId, cancel, stoppedTaskId };
}

export function useTurnEndedAbnormally({ messages, endedAbnormallyOnRecord = false, ...inputs }: TurnEndInputs): boolean {
    const [memory, setMemory] = useState<StopMemory>({ wasResponding: inputs.isResponding, sessionId: inputs.sessionId, cancel: null, stoppedTaskId: null });
    const next = advanceStopMemory(memory, inputs);
    if (next !== memory) setMemory(next);

    const failed = !!messages?.findLast(m => !m.isUser)?.isError;
    return next.stoppedTaskId !== null || failed || endedAbnormallyOnRecord;
}
