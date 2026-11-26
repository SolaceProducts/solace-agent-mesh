// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createSemaphore } from "@/utils/semaphore";

const semaphore = createSemaphore(4);

export const acquireFetchSlotOrAbort = semaphore.acquireOrAbort;
export const releaseFetchSlot = semaphore.release;

export async function fetchWithSlot<T>(signal: AbortSignal, fn: () => Promise<T>): Promise<T | undefined> {
    const acquired = await acquireFetchSlotOrAbort(signal);
    if (!acquired) return undefined;
    try {
        return await fn();
    } finally {
        releaseFetchSlot();
    }
}
