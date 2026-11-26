// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, describe, expect, test, vi } from "vitest";

import { unregisterStaleServiceWorkers } from "./unregisterStaleServiceWorkers";

describe("unregisterStaleServiceWorkers", () => {
    afterEach(() => {
        vi.unstubAllGlobals();
        sessionStorage.clear();
    });

    test("no-op when the serviceWorker API is unavailable", async () => {
        vi.stubGlobal("navigator", {});
        await expect(unregisterStaleServiceWorkers()).resolves.toBeUndefined();
    });

    test("does nothing when no service workers control the origin", async () => {
        const getRegistrations = vi.fn().mockResolvedValue([]);
        const reload = vi.fn();
        vi.stubGlobal("navigator", { serviceWorker: { getRegistrations } });
        vi.stubGlobal("location", { reload });

        await unregisterStaleServiceWorkers();

        expect(getRegistrations).toHaveBeenCalledOnce();
        expect(reload).not.toHaveBeenCalled();
    });

    test("unregisters stale workers, clears caches, and reloads once", async () => {
        const unregister = vi.fn().mockResolvedValue(true);
        const cacheDelete = vi.fn().mockResolvedValue(true);
        const reload = vi.fn();
        vi.stubGlobal("navigator", { serviceWorker: { getRegistrations: vi.fn().mockResolvedValue([{ unregister }]) } });
        vi.stubGlobal("caches", { keys: vi.fn().mockResolvedValue(["v1", "v2"]), delete: cacheDelete });
        vi.stubGlobal("location", { reload });

        await unregisterStaleServiceWorkers();

        expect(unregister).toHaveBeenCalledOnce();
        expect(cacheDelete).toHaveBeenCalledTimes(2);
        expect(reload).toHaveBeenCalledOnce();
        expect(sessionStorage.getItem("sw-cleanup-reloaded")).toBe("1");
    });

    test("does not reload again once the session guard is set", async () => {
        sessionStorage.setItem("sw-cleanup-reloaded", "1");
        const reload = vi.fn();
        vi.stubGlobal("navigator", { serviceWorker: { getRegistrations: vi.fn().mockResolvedValue([{ unregister: vi.fn().mockResolvedValue(true) }]) } });
        vi.stubGlobal("caches", { keys: vi.fn().mockResolvedValue([]), delete: vi.fn() });
        vi.stubGlobal("location", { reload });

        await unregisterStaleServiceWorkers();

        expect(reload).not.toHaveBeenCalled();
    });
});
