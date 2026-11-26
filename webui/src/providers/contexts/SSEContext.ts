// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createContext, useContext } from "react";

import type { SSEConnectionState, SSETask } from "@/types";

export interface SSEContextValue {
    registerTask: (task: Omit<SSETask, "registeredAt">) => void;
    unregisterTask: (taskId: string) => void;
    getTask: (taskId: string) => SSETask | null;
    getTasks: () => SSETask[];
    getTasksByMetadata: (key: string, value: unknown) => SSETask[];
    subscribe: (endpoint: string, handlers: { onMessage?: (e: MessageEvent) => void; onError?: (e: Event) => void }, onStateChange: (state: SSEConnectionState) => void, eventType?: string) => () => void;
    checkTaskStatus: (taskId: string) => Promise<{ isRunning: boolean } | null>;
}

export const SSEContext = createContext<SSEContextValue | null>(null);

export function useSSEContext() {
    const context = useContext(SSEContext);
    if (!context) {
        throw new Error("useSSEContext must be used within SSEProvider");
    }

    return {
        registerTask: context.registerTask,
        unregisterTask: context.unregisterTask,
        getTask: context.getTask,
        getTasks: context.getTasks,
        getTasksByMetadata: context.getTasksByMetadata,
    };
}
