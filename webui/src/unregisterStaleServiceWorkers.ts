// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { BEFORE_RECOVERY_RELOAD_EVENT } from "@/utils/staleChunkRecovery";

const RELOAD_GUARD_KEY = "sw-cleanup-reloaded";

export async function unregisterStaleServiceWorkers(): Promise<void> {
    if (typeof navigator === "undefined" || !("serviceWorker" in navigator)) {
        return;
    }

    try {
        const registrations = await navigator.serviceWorker.getRegistrations();
        if (registrations.length === 0) {
            return;
        }

        await Promise.all(registrations.map(registration => registration.unregister()));

        if (typeof caches !== "undefined") {
            const keys = await caches.keys();
            await Promise.all(keys.map(key => caches.delete(key)));
        }

        if (!sessionStorage.getItem(RELOAD_GUARD_KEY)) {
            sessionStorage.setItem(RELOAD_GUARD_KEY, "1");
            globalThis.dispatchEvent(new Event(BEFORE_RECOVERY_RELOAD_EVENT));
            globalThis.location.reload();
        }
    } catch {}
}
