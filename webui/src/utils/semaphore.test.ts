// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, it } from "vitest";

import { createSemaphore } from "./semaphore";

describe("createSemaphore", () => {
    it("acquire resolves immediately when slots are available", async () => {
        const { acquire } = createSemaphore(2);
        let resolved = 0;
        await Promise.all([acquire().then(() => resolved++), acquire().then(() => resolved++)]);
        expect(resolved).toBe(2);
    });

    it("acquire queues when all slots are taken", async () => {
        const { acquire, release } = createSemaphore(1);
        await acquire();

        let resolved = false;
        const pending = acquire().then(() => {
            resolved = true;
        });

        await Promise.resolve();
        expect(resolved).toBe(false);

        release();
        await pending;
        expect(resolved).toBe(true);
    });

    it("release transfers a slot to the next queued waiter", async () => {
        const { acquire, release } = createSemaphore(1);
        await acquire();

        const order: number[] = [];
        const p1 = acquire().then(() => {
            order.push(1);
        });
        const p2 = acquire().then(() => {
            order.push(2);
        });

        release();
        await p1;
        release();
        await p2;

        expect(order).toEqual([1, 2]);
    });

    it("acquireOrAbort returns false when the signal is already aborted", async () => {
        const { acquireOrAbort } = createSemaphore(2);
        const controller = new AbortController();
        controller.abort();

        const result = await acquireOrAbort(controller.signal);
        expect(result).toBe(false);
    });

    it("acquireOrAbort returns true when slots are available and signal is not aborted", async () => {
        const { acquireOrAbort } = createSemaphore(2);
        const controller = new AbortController();

        const result = await acquireOrAbort(controller.signal);
        expect(result).toBe(true);
    });

    it("queued waiter is cleaned up when signal is aborted before slot is granted", async () => {
        const { acquire, release, acquireOrAbort } = createSemaphore(1);
        await acquire();

        const controller = new AbortController();
        const pending = acquireOrAbort(controller.signal);

        controller.abort();
        const result = await pending;
        expect(result).toBe(false);

        release();

        const controller2 = new AbortController();
        const result2 = await acquireOrAbort(controller2.signal);
        expect(result2).toBe(true);
    });

    it("does not leak a slot when abort races with slot grant", async () => {
        const { acquire, release, acquireOrAbort } = createSemaphore(1);
        await acquire();

        const controller = new AbortController();
        const pending = acquireOrAbort(controller.signal);

        controller.abort();
        release();

        const result = await pending;
        expect(result).toBe(false);

        const controller2 = new AbortController();
        const result2 = await acquireOrAbort(controller2.signal);
        expect(result2).toBe(true);
    });
});
