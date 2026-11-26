// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useEffect, useState } from "react";

import { api } from "@/api/client";

import { applyActivityEventFrame } from "@/providers/task/activityEventStore";

import { getApiBearerToken } from "@/utils/api";
import { jitteredBackoff } from "@/utils/backoff";

import type { TaskFE } from "@/types";

const INITIAL_RETRY_DELAY = 1000;
const MAX_RETRY_DELAY = 30000;
const MAX_RETRY_ATTEMPTS = 5;
const HEALTHY_CONNECTION_MS = 10000;

export function useTaskActivityEvents(taskId: string | null, enabled: boolean): Record<string, TaskFE> {
    const [tasks, setTasks] = useState<Record<string, TaskFE>>({});

    useEffect(() => {
        setTasks({});
        if (!enabled || !taskId) {
            return;
        }

        let eventSource: EventSource | null = null;
        let retryCount = 0;
        let retryDelay = INITIAL_RETRY_DELAY;
        let retryTimeoutId: ReturnType<typeof setTimeout> | null = null;
        let cancelled = false;

        const onActivityEvent = (event: MessageEvent) => {
            setTasks(prev => applyActivityEventFrame(prev, event.data));
        };

        const buildUrl = () => {
            const params = new URLSearchParams();
            const bearerToken = getApiBearerToken();
            if (bearerToken) {
                params.append("token", bearerToken);
            }
            const base = api.webui.getFullUrl(`/api/v1/sse/subscribe/${taskId}`);
            return params.toString() ? `${base}?${params.toString()}` : base;
        };

        const connect = () => {
            const es = new EventSource(buildUrl(), { withCredentials: true });
            eventSource = es;
            es.addEventListener("activity_event", onActivityEvent);

            let openedAt = 0;

            es.onopen = () => {
                openedAt = Date.now();
            };

            es.onerror = () => {
                es.removeEventListener("activity_event", onActivityEvent);
                es.close();
                if (cancelled) {
                    return;
                }
                const stayedHealthy = openedAt > 0 && Date.now() - openedAt >= HEALTHY_CONNECTION_MS;
                if (stayedHealthy) {
                    retryCount = 0;
                    retryDelay = INITIAL_RETRY_DELAY;
                }
                if (retryCount >= MAX_RETRY_ATTEMPTS) {
                    return;
                }
                retryCount++;
                const jitteredDelay = jitteredBackoff(retryDelay);
                retryDelay = Math.min(retryDelay * 2, MAX_RETRY_DELAY);
                retryTimeoutId = setTimeout(connect, jitteredDelay);
            };
        };

        connect();

        return () => {
            cancelled = true;
            if (retryTimeoutId) {
                clearTimeout(retryTimeoutId);
            }
            if (eventSource) {
                eventSource.removeEventListener("activity_event", onActivityEvent);
                eventSource.close();
            }
        };
    }, [taskId, enabled]);

    return tasks;
}
