// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { type ReactNode, useCallback, useEffect, useRef, useState } from "react";

import { api } from "@/api/client";

import { SSEContext, type SSEContextValue } from "@/providers/contexts/SSEContext";

import { getApiBearerToken } from "@/utils/api";
import { jitteredBackoff } from "@/utils/backoff";
import { uuid } from "@/utils/uuid";

import { useSessionStorage } from "@/hooks";
import type { SSEConnectionState, SSESubscriptionOptions, SSESubscriptionReturn, SSETask } from "@/types";

const TASK_STORAGE_KEY = "sam_sse_tasks";
const INITIAL_RETRY_DELAY = 1000;
const MAX_RETRY_DELAY = 30000;
const MAX_RETRY_ATTEMPTS = 20;
const STALE_TASK_THRESHOLD_MS = 3 * 60 * 1000;

interface ConnectionEntry {
    eventSource: EventSource | null;
    state: SSEConnectionState;
    subscribers: Map<
        string,
        {
            onMessage?: (e: MessageEvent) => void;
            onError?: (e: Event) => void;
            onStateChange: (state: SSEConnectionState) => void;
            eventType: string;
        }
    >;
    registeredEventTypes: Set<string>;
    retryCount: number;
    retryDelay: number;
    retryTimeoutId: ReturnType<typeof setTimeout> | null;
}

interface SSEProviderProps {
    children: ReactNode;
}

export const SSEProvider = ({ children }: SSEProviderProps) => {
    const connectionsRef = useRef<Map<string, ConnectionEntry>>(new Map());

    const [tasks, setTasks] = useSessionStorage<SSETask[]>(TASK_STORAGE_KEY, []);

    const getValidTasks = useCallback(() => {
        const now = Date.now();
        return tasks.filter(task => now - task.registeredAt < STALE_TASK_THRESHOLD_MS);
    }, [tasks]);

    const updateConnectionState = useCallback((endpoint: string, state: SSEConnectionState) => {
        const entry = connectionsRef.current.get(endpoint);
        if (entry) {
            entry.state = state;
            entry.subscribers.forEach(sub => sub.onStateChange(state));
        }
    }, []);

    const buildUrlWithAuth = useCallback((endpoint: string): string => {
        const baseUrl = api.webui.getFullUrl(endpoint);
        const accessToken = getApiBearerToken();

        if (accessToken) {
            const separator = baseUrl.includes("?") ? "&" : "?";
            return `${baseUrl}${separator}token=${accessToken}`;
        }

        return baseUrl;
    }, []);

    const addEventTypeListener = useCallback((entry: ConnectionEntry, eventSource: EventSource, eventType: string) => {
        const handler = (event: MessageEvent) => {
            entry.subscribers.forEach(sub => {
                if (sub.eventType === eventType) {
                    sub.onMessage?.(event);
                }
            });
        };

        if (eventType === "message") {
            eventSource.onmessage = handler;
        } else {
            eventSource.addEventListener(eventType, handler as EventListener);
        }
    }, []);

    const connect = useCallback(
        (endpoint: string, entry: ConnectionEntry) => {
            const open = () => {
                const eventSource = new EventSource(buildUrlWithAuth(endpoint), { withCredentials: true });
                entry.eventSource = eventSource;

                eventSource.onopen = () => {
                    entry.retryCount = 0;
                    entry.retryDelay = INITIAL_RETRY_DELAY;
                    updateConnectionState(endpoint, "connected");
                };

                eventSource.onerror = (errorEvent: Event) => {
                    console.error(`[SSEProvider] Connection error: ${endpoint}`, errorEvent);

                    eventSource.close();

                    entry.subscribers.forEach(sub => {
                        try {
                            sub.onError?.(errorEvent);
                        } catch (callbackError) {
                            console.error(`[SSEProvider] Subscriber onError threw: ${endpoint}`, callbackError);
                        }
                    });

                    scheduleRetry();
                };

                entry.registeredEventTypes.forEach(eventType => {
                    addEventTypeListener(entry, eventSource, eventType);
                });
            };

            const scheduleRetry = () => {
                if (entry.retryTimeoutId !== null) {
                    return;
                }

                if (entry.retryCount >= MAX_RETRY_ATTEMPTS) {
                    console.error(`[SSEProvider] Max reconnection attempts reached: ${endpoint}`);
                    updateConnectionState(endpoint, "error");
                    return;
                }

                updateConnectionState(endpoint, "reconnecting");

                const jitteredDelay = jitteredBackoff(entry.retryDelay);
                entry.retryCount++;
                entry.retryDelay = Math.min(entry.retryDelay * 2, MAX_RETRY_DELAY);

                entry.retryTimeoutId = setTimeout(() => {
                    entry.retryTimeoutId = null;
                    if (entry.subscribers.size > 0) {
                        open();
                    }
                }, jitteredDelay);
            };

            open();
        },
        [addEventTypeListener, buildUrlWithAuth, updateConnectionState]
    );

    const createConnection = useCallback(
        (endpoint: string): ConnectionEntry => {
            const entry: ConnectionEntry = {
                eventSource: null,
                state: "connecting",
                subscribers: new Map(),
                registeredEventTypes: new Set(),
                retryCount: 0,
                retryDelay: INITIAL_RETRY_DELAY,
                retryTimeoutId: null,
            };

            connect(endpoint, entry);

            return entry;
        },
        [connect]
    );

    const reviveConnection = useCallback(
        (endpoint: string, entry: ConnectionEntry) => {
            entry.retryCount = 0;
            entry.retryDelay = INITIAL_RETRY_DELAY;
            updateConnectionState(endpoint, "connecting");
            connect(endpoint, entry);
        },
        [connect, updateConnectionState]
    );

    const subscribe = useCallback(
        (endpoint: string, handlers: { onMessage?: (e: MessageEvent) => void; onError?: (e: Event) => void }, onStateChange: (state: SSEConnectionState) => void, eventType: string = "message"): (() => void) => {
            const subscriberId = uuid();

            let entry = connectionsRef.current.get(endpoint);

            if (!entry) {
                entry = createConnection(endpoint);
                connectionsRef.current.set(endpoint, entry);
            } else if (entry.state === "error") {
                reviveConnection(endpoint, entry);
            }

            if (!entry.registeredEventTypes.has(eventType)) {
                entry.registeredEventTypes.add(eventType);

                if (entry.eventSource) {
                    addEventTypeListener(entry, entry.eventSource, eventType);
                }
            }

            entry.subscribers.set(subscriberId, {
                onMessage: handlers.onMessage,
                onError: handlers.onError,
                onStateChange,
                eventType,
            });

            onStateChange(entry.state);

            return () => {
                const currentEntry = connectionsRef.current.get(endpoint);
                if (!currentEntry) return;

                currentEntry.subscribers.delete(subscriberId);

                if (currentEntry.subscribers.size === 0) {
                    if (currentEntry.retryTimeoutId) {
                        clearTimeout(currentEntry.retryTimeoutId);
                    }

                    currentEntry.eventSource?.close();
                    connectionsRef.current.delete(endpoint);
                }
            };
        },
        [addEventTypeListener, createConnection, reviveConnection]
    );

    const checkTaskStatus = useCallback(async (taskId: string): Promise<{ isRunning: boolean } | null> => {
        try {
            const response = await api.webui.get(`/api/v1/tasks/${taskId}/status`);
            return { isRunning: response.isRunning };
        } catch (error) {
            console.warn(`[SSEProvider] Failed to check task status for ${taskId}:`, error);
            return null;
        }
    }, []);

    const registerTask = useCallback(
        (task: Omit<SSETask, "registeredAt">) => {
            setTasks(prev => {
                if (prev.some(t => t.taskId === task.taskId)) {
                    return prev;
                }
                return [...prev, { ...task, registeredAt: Date.now() }];
            });
        },
        [setTasks]
    );

    const unregisterTask = useCallback(
        (taskId: string) => {
            setTasks(prev => prev.filter(t => t.taskId !== taskId));
        },
        [setTasks]
    );

    const getTask = useCallback(
        (taskId: string): SSETask | null => {
            return getValidTasks().find(t => t.taskId === taskId) ?? null;
        },
        [getValidTasks]
    );

    const getTasks = useCallback((): SSETask[] => {
        return getValidTasks();
    }, [getValidTasks]);

    const getTasksByMetadata = useCallback(
        (key: string, value: unknown): SSETask[] => {
            return getValidTasks().filter(t => t.metadata?.[key] === value);
        },
        [getValidTasks]
    );

    useEffect(() => {
        const reviveExhausted = () => {
            connectionsRef.current.forEach((entry, endpoint) => {
                if (entry.state === "error" && entry.subscribers.size > 0) {
                    reviveConnection(endpoint, entry);
                }
            });
        };

        const reviveIfVisible = () => {
            if (document.visibilityState === "visible") {
                reviveExhausted();
            }
        };

        globalThis.addEventListener("focus", reviveExhausted);
        globalThis.addEventListener("online", reviveExhausted);
        document.addEventListener("visibilitychange", reviveIfVisible);

        return () => {
            globalThis.removeEventListener("focus", reviveExhausted);
            globalThis.removeEventListener("online", reviveExhausted);
            document.removeEventListener("visibilitychange", reviveIfVisible);
        };
    }, [reviveConnection]);

    useEffect(() => {
        const connections = connectionsRef.current;
        return () => {
            connections.forEach(entry => {
                if (entry.retryTimeoutId) {
                    clearTimeout(entry.retryTimeoutId);
                }
                entry.eventSource?.close();
            });
            connections.clear();
        };
    }, []);

    const contextValue: SSEContextValue = {
        registerTask,
        unregisterTask,
        getTask,
        getTasks,
        getTasksByMetadata,
        subscribe,
        checkTaskStatus,
    };

    return <SSEContext.Provider value={contextValue}>{children}</SSEContext.Provider>;
};

export function useSSESubscription(options: SSESubscriptionOptions): SSESubscriptionReturn {
    const context = React.useContext(SSEContext);
    if (!context) {
        throw new Error("useSSESubscription must be used within SSEProvider");
    }

    const { subscribe, checkTaskStatus } = context;
    const { endpoint, taskId, eventType = "message", onMessage, onError, onTaskAlreadyCompleted } = options;
    const [connectionState, setConnectionState] = useState<SSEConnectionState>("disconnected");

    const onMessageRef = useRef(onMessage);
    const onErrorRef = useRef(onError);
    const onTaskAlreadyCompletedRef = useRef(onTaskAlreadyCompleted);
    const hasCheckedStatus = useRef(false);

    useEffect(() => {
        onMessageRef.current = onMessage;
        onErrorRef.current = onError;
        onTaskAlreadyCompletedRef.current = onTaskAlreadyCompleted;
    });

    const prevTaskIdRef = useRef(taskId);
    useEffect(() => {
        if (prevTaskIdRef.current !== taskId) {
            hasCheckedStatus.current = false;
            prevTaskIdRef.current = taskId;
        }
    }, [taskId]);

    useEffect(() => {
        if (!endpoint) {
            setConnectionState("disconnected");
            return;
        }

        let unsubscribe: (() => void) | undefined;
        let cancelled = false;

        const connect = async () => {
            if (taskId && !hasCheckedStatus.current) {
                hasCheckedStatus.current = true;
                const status = await checkTaskStatus(taskId);

                if (cancelled) return;

                if (status && !status.isRunning) {
                    setConnectionState("disconnected");
                    onTaskAlreadyCompletedRef.current?.();
                    return;
                }
            }

            unsubscribe = subscribe(
                endpoint,
                {
                    onMessage: (e: MessageEvent) => onMessageRef.current?.(e),
                    onError: (e: Event) => onErrorRef.current?.(e),
                },
                setConnectionState,
                eventType
            );
        };

        connect();

        return () => {
            cancelled = true;
            unsubscribe?.();
        };
    }, [endpoint, taskId, eventType, subscribe, checkTaskStatus]);

    return {
        connectionState,
        isConnected: connectionState === "connected",
    };
}
