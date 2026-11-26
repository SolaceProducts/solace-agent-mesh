// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useState } from "react";

import { restorableBuilderSession } from "../builderSessionRestore";

const PENDING_AUTO_CREATE_KEY = "sam_builder_pending_autocreate";

function readStoredSessionId(): string | null {
    try {
        return sessionStorage.getItem(PENDING_AUTO_CREATE_KEY);
    } catch {
        return null;
    }
}

let storedSessionId: string | null = readStoredSessionId();

function clearStored(): void {
    try {
        sessionStorage.removeItem(PENDING_AUTO_CREATE_KEY);
        storedSessionId = null;
    } catch (err) {
        console.warn("[builder] could not clear the remembered plan approval", err);
    }
}

let approvedBeforeThisPageLoad: string | null = storedSessionId !== null && storedSessionId === restorableBuilderSession() ? storedSessionId : null;
let firstSessionSeen = false;

if (storedSessionId !== null && approvedBeforeThisPageLoad === null) clearStored();

function store(sessionId: string): void {
    try {
        sessionStorage.setItem(PENDING_AUTO_CREATE_KEY, sessionId);
        storedSessionId = sessionId;
    } catch (err) {
        console.warn("[builder] could not remember the plan approval; a reload will not resume the build", err);
    }
}

function forget(sessionId: string): void {
    if (!sessionId) return;
    if (approvedBeforeThisPageLoad === sessionId) approvedBeforeThisPageLoad = null;
    if (storedSessionId === sessionId) clearStored();
}

function resumedFor(sessionId: string): boolean {
    if (!sessionId) return false;
    if (!firstSessionSeen) {
        firstSessionSeen = true;
        if (approvedBeforeThisPageLoad !== null && approvedBeforeThisPageLoad !== sessionId) forget(approvedBeforeThisPageLoad);
    }
    return approvedBeforeThisPageLoad === sessionId;
}

interface PendingState {
    sessionId: string;
    pending: boolean;
}

export function usePendingAutoCreate(sessionId: string): readonly [boolean, (pending: boolean) => void] {
    const [state, setState] = useState<PendingState>(() => ({ sessionId, pending: resumedFor(sessionId) }));

    let current = state;
    if (state.sessionId !== sessionId) {
        current = { sessionId, pending: resumedFor(sessionId) };
        setState(current);
    }

    useEffect(() => () => forget(sessionId), [sessionId]);

    const setPending = useCallback(
        (pending: boolean) => {
            if (pending) {
                if (sessionId) store(sessionId);
            } else {
                forget(sessionId);
            }
            setState(prev => (prev.sessionId === sessionId ? { sessionId, pending } : prev));
        },
        [sessionId]
    );

    return [current.pending, setPending];
}
