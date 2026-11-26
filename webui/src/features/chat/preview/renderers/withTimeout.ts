// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const MAX_ZIP_ENTRY_BYTES = 64 * 1024 * 1024;

export function withTimeout<T>(p: Promise<T>, ms: number, message: string): Promise<T> {
    let timeoutId: ReturnType<typeof setTimeout> | undefined;
    let timedOut = false;
    const timeout = new Promise<never>((_, reject) => {
        timeoutId = setTimeout(() => {
            timedOut = true;
            reject(new Error(message));
        }, ms);
    });
    p.then(
        () => timedOut && console.debug("[ooxml] load settled after timeout"),
        err => timedOut && console.warn("[ooxml] load rejected after timeout", err)
    );
    return Promise.race([p, timeout]).finally(() => clearTimeout(timeoutId));
}
