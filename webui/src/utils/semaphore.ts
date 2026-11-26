// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export function createSemaphore(maxConcurrency: number) {
    let active = 0;
    const queue: Array<() => void> = [];

    function acquire(): Promise<void> {
        if (active < maxConcurrency) {
            active++;
            return Promise.resolve();
        }
        return new Promise<void>(resolve => {
            queue.push(resolve);
        });
    }

    function release(): void {
        if (queue.length > 0) {
            const next = queue.shift()!;
            next();
        } else {
            active--;
        }
    }

    function acquireOrAbort(signal: AbortSignal): Promise<boolean> {
        if (signal.aborted) return Promise.resolve(false);

        if (active < maxConcurrency) {
            active++;
            return Promise.resolve(true);
        }

        return new Promise<boolean>(resolve => {
            let settled = false;

            const onAbort = () => {
                if (settled) return;
                settled = true;
                const idx = queue.indexOf(grantSlot);
                if (idx !== -1) {
                    queue.splice(idx, 1);
                } else {
                    release();
                }
                resolve(false);
            };

            const grantSlot = () => {
                signal.removeEventListener("abort", onAbort);
                if (settled) {
                    release();
                    return;
                }
                settled = true;
                if (signal.aborted) {
                    release();
                    resolve(false);
                } else {
                    resolve(true);
                }
            };

            queue.push(grantSlot);
            signal.addEventListener("abort", onAbort, { once: true });
        });
    }

    return { acquire, release, acquireOrAbort };
}
