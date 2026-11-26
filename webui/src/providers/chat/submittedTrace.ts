// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { MutableRefObject } from "react";

export function takeSubmittedTrace(pending: MutableRefObject<{ taskId: string; traceId: string } | null>): Record<string, string> {
    const trace = pending.current;
    pending.current = null;
    return trace ? { traceId: trace.traceId } : {};
}

export function buildSubmittedTrace(taskId: string | undefined, traceId: string | undefined): { taskId: string; traceId: string } | null {
    return taskId && traceId ? { taskId, traceId } : null;
}
