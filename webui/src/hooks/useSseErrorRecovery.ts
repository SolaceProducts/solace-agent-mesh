// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

import { refreshToken } from "@/api/client";

import type { ProductAnalyticsErrorSource } from "@/common/product-analytics/types";

export interface SseErrorRecoveryCallbacks {
    closeCurrentEventSource: () => void;
    setError: (error: { title: string; error: string; errorSource?: ProductAnalyticsErrorSource }) => void;
    setIsResponding: (responding: boolean) => void;
    setCurrentTaskId: (taskId: string | null) => void;
    cleanupMessages: () => void;
}

export interface SseErrorRecoveryState {
    isResponding: boolean;
    isFinalizing: React.RefObject<boolean>;
    isCancelling: React.RefObject<boolean>;
    currentTaskId: string | null;
}

export interface SseErrorRecoveryResult {
    sseReconnectKey: number;
    handleSseError: () => void;
    cleanupSseFailure: (title: string, message: string) => void;
}

export function useSseErrorRecovery(state: SseErrorRecoveryState, callbacks: SseErrorRecoveryCallbacks): SseErrorRecoveryResult {
    const { isResponding, isFinalizing, isCancelling, currentTaskId } = state;
    const { closeCurrentEventSource, setError, setIsResponding, setCurrentTaskId, cleanupMessages } = callbacks;

    const sseRefreshAttempted = useRef(false);
    const sseRefreshTaskId = useRef<string | null>(null);

    const [sseReconnectKey, setSseReconnectKey] = useState(0);

    useEffect(() => {
        if (currentTaskId && currentTaskId !== sseRefreshTaskId.current) {
            sseRefreshAttempted.current = false;
            sseRefreshTaskId.current = currentTaskId;
        }
    }, [currentTaskId]);

    const cleanupSseFailure = useCallback(
        (title: string, message: string) => {
            setError({ title, error: message, errorSource: "client" });
            setIsResponding(false);
            setCurrentTaskId(null);
            cleanupMessages();
        },
        [setError, setIsResponding, setCurrentTaskId, cleanupMessages]
    );

    const handleSseError = useCallback(() => {
        if (!sseRefreshAttempted.current && currentTaskId && isResponding && !isFinalizing.current && !isCancelling.current) {
            sseRefreshAttempted.current = true;
            console.log("[useSseErrorRecovery] SSE error — attempting token refresh before reconnect");
            closeCurrentEventSource();

            void refreshToken()
                .then(newToken => {
                    const taskId = sseRefreshTaskId.current;
                    if (newToken && taskId) {
                        console.log("[useSseErrorRecovery] Token refreshed, reconnecting SSE for task", taskId);
                        setSseReconnectKey(k => k + 1);
                    } else {
                        console.warn("[useSseErrorRecovery] Token refresh failed during SSE error");
                        cleanupSseFailure("Connection Failed", "Session expired. Please log in again.");
                    }
                })
                .catch(err => {
                    console.error("[useSseErrorRecovery] Unexpected error during SSE token refresh:", err);
                    cleanupSseFailure("Connection Failed", "Connection lost. Please try again.");
                });
            return;
        }

        if (isResponding && !isFinalizing.current && !isCancelling.current) {
            setError({ title: "Connection Failed", error: "Connection lost. Please try again.", errorSource: "client" });
        }
        if (!isFinalizing.current) {
            setIsResponding(false);
            if (!isCancelling.current) {
                closeCurrentEventSource();
                setCurrentTaskId(null);
            }
        }
        cleanupMessages();
    }, [closeCurrentEventSource, isResponding, setError, currentTaskId, cleanupSseFailure, isFinalizing, isCancelling, setIsResponding, setCurrentTaskId, cleanupMessages]);

    return {
        sseReconnectKey,
        handleSseError,
        cleanupSseFailure,
    };
}
