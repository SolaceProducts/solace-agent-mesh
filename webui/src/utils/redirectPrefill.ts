// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { SESSION_STORAGE_KEYS } from "./sessionStorageKeys";

export function stashPendingPrefill(text: string, targetSessionId: string | null) {
    sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT, text);
    if (targetSessionId) {
        sessionStorage.setItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT_SESSION, targetSessionId);
    } else {
        sessionStorage.removeItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT_SESSION);
    }
}

export function hasPendingPrefill(): boolean {
    try {
        return sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT) !== null;
    } catch {
        return false;
    }
}

export function clearPendingPrefill() {
    sessionStorage.removeItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT);
    sessionStorage.removeItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT_SESSION);
}

export function consumePendingPrefill(sessionId: string | null | undefined): string | null {
    const pending = sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT);
    if (!pending) return null;
    const targetSessionId = sessionStorage.getItem(SESSION_STORAGE_KEYS.REDIRECT_PENDING_INPUT_SESSION);
    if (targetSessionId && targetSessionId !== sessionId) return null;
    clearPendingPrefill();
    return pending;
}
