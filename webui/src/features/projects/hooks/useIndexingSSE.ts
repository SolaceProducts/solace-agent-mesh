// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useCallback, useEffect, useRef, useState } from "react";

import { useSSEContext } from "@/providers/contexts";

import { useSSESubscription } from "@/providers";
import type { SSEConnectionState } from "@/types";

export function useStartIndexing(metadataKey = "resourceId") {
    const { registerTask } = useSSEContext();

    return useCallback(
        (sseLocation: string, resourceId: string, operation: string) => {
            const taskId = sseLocation.split("/").pop();
            if (taskId) {
                registerTask({
                    taskId,
                    sseUrl: sseLocation,
                    metadata: { [metadataKey]: resourceId, operation },
                });
            }
        },
        [registerTask, metadataKey]
    );
}

export interface IndexingSSEEvent {
    type: string;
    [key: string]: unknown;
}

export interface UseIndexingSSEOptions {
    resourceId: string;
    metadataKey?: string;
    onComplete?: (failedFiles: string[], errors: string[]) => void;
    onEvent?: (event: IndexingSSEEvent) => void;
}

export interface UseIndexingSSEReturn {
    connectionState: SSEConnectionState;
    isConnected: boolean;
    isIndexing: boolean;
    latestEvent: IndexingSSEEvent | null;
    startIndexing: (sseLocation: string, resourceId: string, operation: string) => void;
}

export function useIndexingSSE(options: UseIndexingSSEOptions): UseIndexingSSEReturn {
    const { resourceId, metadataKey = "resourceId", onComplete, onEvent } = options;

    const { unregisterTask, getTasksByMetadata } = useSSEContext();
    const startIndexing = useStartIndexing(metadataKey);

    const [latestEvent, setLatestEvent] = useState<IndexingSSEEvent | null>(null);
    const failedFilesRef = useRef<string[]>([]);
    const errorsRef = useRef<string[]>([]);

    const onCompleteRef = useRef(onComplete);
    const onEventRef = useRef(onEvent);
    useEffect(() => {
        onCompleteRef.current = onComplete;
        onEventRef.current = onEvent;
    });

    const existingTask = getTasksByMetadata(metadataKey, resourceId)[0] ?? null;
    const taskId = existingTask?.taskId ?? null;
    const sseUrl = existingTask?.sseUrl ?? null;

    const handleTaskComplete = useCallback(
        (failedFiles: string[], errors: string[]) => {
            setLatestEvent(null);
            if (taskId) {
                unregisterTask(taskId);
            }
            onCompleteRef.current?.(failedFiles, errors);
            failedFilesRef.current = [];
            errorsRef.current = [];
        },
        [taskId, unregisterTask]
    );

    const { connectionState, isConnected } = useSSESubscription({
        endpoint: sseUrl,
        eventType: "index_message",
        onMessage: event => {
            try {
                const data = JSON.parse(event.data) as IndexingSSEEvent;

                setLatestEvent(data);
                onEventRef.current?.(data);

                if (data.type === "task_completed") {
                    handleTaskComplete(failedFilesRef.current, errorsRef.current);
                } else if (data.type === "task_error") {
                    const errorMsg = typeof data.error === "string" ? data.error : "File processing failed";
                    handleTaskComplete(failedFilesRef.current, [...errorsRef.current, errorMsg]);
                } else if (data.type === "conversion_failed") {
                    const filename = typeof data.file === "string" ? data.file : "Unknown";
                    failedFilesRef.current = [...failedFilesRef.current, filename];
                } else if (data.type === "indexing_failed") {
                    const errorMsg = typeof data.error === "string" ? data.error : "Indexing failed";
                    errorsRef.current = [...errorsRef.current, errorMsg];
                }
            } catch (e) {
                console.error("[useIndexingSSE] Failed to parse event:", e);
            }
        },
        onError: event => {
            console.error("[useIndexingSSE] Connection error:", event);
        },
    });

    return {
        connectionState,
        isConnected,
        isIndexing: taskId !== null,
        latestEvent,
        startIndexing,
    };
}
