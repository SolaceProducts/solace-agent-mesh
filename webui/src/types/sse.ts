// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

/* eslint-disable @typescript-eslint/no-explicit-any */

export type SSEConnectionState = "connecting" | "connected" | "reconnecting" | "disconnected" | "error";

export interface SSETask {
    taskId: string;
    sseUrl: string;
    metadata?: Record<string, unknown>;
    registeredAt: number;
}

export interface SSESubscriptionOptions {
    endpoint: string | null;
    taskId?: string;
    eventType?: string;
    onMessage?: (event: MessageEvent) => void;
    onError?: (event: Event) => void;
    onTaskAlreadyCompleted?: () => void;
}

export interface SSESubscriptionReturn {
    connectionState: SSEConnectionState;
    isConnected: boolean;
}

export interface A2AEventSSEPayload {
    event_type: "a2a_message" | string;
    timestamp: string;
    solace_topic: string;
    direction: "request" | "response" | "status_update" | "artifact_update" | "discovery" | string;
    source_entity: string;
    target_entity: string;
    message_id?: string | null;
    task_id?: string | null;
    payload_summary: {
        method?: string;
        params_preview?: string;
    };
    full_payload: Record<string, any>;
}
