// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useRef } from "react";

import type { ProductAnalyticsErrorSource } from "@/common/product-analytics/types";

export const SSE_RECONNECT_GRACE_MS = 45_000;

export interface SseReconnectGuardState {
    isResponding: boolean;
    isFinalizing: React.RefObject<boolean>;
    isCancelling: React.RefObject<boolean>;
    currentTaskId: string | null;
    getEventSource: () => EventSource | null;
}

export interface SseReconnectGuardCallbacks {
    closeCurrentEventSource: () => void;
    setError: (error: { title: string; error: string; onRetry?: () => void; traceId?: string; errorSource?: ProductAnalyticsErrorSource }) => void;
    setIsResponding: (responding: boolean) => void;
    setCurrentTaskId: (taskId: string | null) => void;
    setLatestStatusText: (text: string | null) => void;
    cleanupMessages: () => void;
    requestManualRetry: (taskId: string) => void;
    takeTaskTrace: () => string | undefined;
}

export interface SseReconnectGuardResult {
    handleSseOpen: () => void;
    handleSseError: () => void;
    clearReconnectGraceTimer: () => void;
}

export function useSseReconnectGuard(state: SseReconnectGuardState, callbacks: SseReconnectGuardCallbacks, graceMs: number = SSE_RECONNECT_GRACE_MS): SseReconnectGuardResult {
    const stateRef = useRef(state);
    stateRef.current = state;
    const callbacksRef = useRef(callbacks);
    callbacksRef.current = callbacks;

    const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

    const clearReconnectGraceTimer = useCallback(() => {
        if (timerRef.current) {
            clearTimeout(timerRef.current);
            timerRef.current = null;
        }
    }, []);

    const handleSseOpen = useCallback(() => {
        clearReconnectGraceTimer();
    }, [clearReconnectGraceTimer]);

    const failSseConnection = useCallback(
        (interruptedTaskId: string | null) => {
            clearReconnectGraceTimer();
            const s = stateRef.current;
            const c = callbacksRef.current;
            if (s.isResponding && !s.isFinalizing.current && !s.isCancelling.current) {
                c.setError({
                    title: "Connection Failed",
                    error: "Connection lost. Please try again.",
                    onRetry: interruptedTaskId ? () => c.requestManualRetry(interruptedTaskId) : undefined,
                    traceId: c.takeTaskTrace(),
                    errorSource: "client",
                });
            }
            if (!s.isFinalizing.current) {
                c.setIsResponding(false);
                if (!s.isCancelling.current) {
                    c.closeCurrentEventSource();
                    c.setCurrentTaskId(null);
                }
                c.setLatestStatusText(null);
            }
            c.cleanupMessages();
        },
        [clearReconnectGraceTimer]
    );

    const handleSseError = useCallback(() => {
        const s = stateRef.current;
        const interruptedTaskId = s.currentTaskId;
        const es = s.getEventSource();

        if (es?.readyState === EventSource.CONNECTING && s.isResponding && !s.isFinalizing.current && !s.isCancelling.current) {
            if (!timerRef.current) {
                timerRef.current = setTimeout(() => {
                    timerRef.current = null;
                    const es2 = stateRef.current.getEventSource();
                    if (!es2 || es2.readyState === EventSource.OPEN || stateRef.current.isFinalizing.current) {
                        return;
                    }
                    failSseConnection(interruptedTaskId);
                }, graceMs);
            }
            return;
        }

        failSseConnection(interruptedTaskId);
    }, [failSseConnection, graceMs]);

    return { handleSseOpen, handleSseError, clearReconnectGraceTimer };
}
