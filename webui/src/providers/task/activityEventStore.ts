// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { stripGatewayTimestamp } from "@/api/tasks/types";

import type { A2AEventSSEPayload, TaskFE } from "@/types";

const maxActivityTasks = 50;

export function extractEventContextId(event: A2AEventSSEPayload): string | undefined {
    const payload = event.full_payload as { params?: { message?: { contextId?: string } }; result?: { contextId?: string } } | undefined;
    return payload?.params?.message?.contextId ?? payload?.result?.contextId;
}

function extractRequestText(event: A2AEventSSEPayload): string {
    if (event.direction !== "request" || !event.full_payload?.method?.startsWith?.("message/")) {
        return "";
    }
    const params = event.full_payload.params as { message?: { parts?: { kind: string; text: string }[] } } | undefined;
    const parts = params?.message?.parts ?? [];
    const text = parts
        .filter(p => p.kind === "text" && p.text)
        .map(p => p.text)
        .join("\n");
    return text ? stripGatewayTimestamp(text) : "";
}

function capTasks(tasks: Record<string, TaskFE>): Record<string, TaskFE> {
    const ids = Object.keys(tasks);
    if (ids.length <= maxActivityTasks) {
        return tasks;
    }
    const keep = ids.toSorted((a, b) => tasks[b].lastUpdated.getTime() - tasks[a].lastUpdated.getTime()).slice(0, maxActivityTasks);
    const next: Record<string, TaskFE> = {};
    for (const id of keep) {
        next[id] = tasks[id];
    }
    return next;
}

export function upsertActivityEvent(tasks: Record<string, TaskFE>, event: A2AEventSSEPayload): Record<string, TaskFE> {
    const taskId = event.task_id;
    if (!taskId) {
        return tasks;
    }
    const eventTimestamp = new Date(event.timestamp);
    const existing = tasks[taskId];
    if (existing) {
        const events = [...existing.events, event].sort((a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime());
        return {
            ...tasks,
            [taskId]: {
                ...existing,
                events,
                lastUpdated: eventTimestamp,
                sessionId: existing.sessionId ?? extractEventContextId(event),
                initialRequestText: existing.initialRequestText || extractRequestText(event),
            },
        };
    }
    return capTasks({
        ...tasks,
        [taskId]: {
            taskId,
            initialRequestText: extractRequestText(event),
            events: [event],
            firstSeen: eventTimestamp,
            lastUpdated: eventTimestamp,
            sessionId: extractEventContextId(event),
        },
    });
}

export function applyActivityEventFrame(tasks: Record<string, TaskFE>, data: string): Record<string, TaskFE> {
    let event: A2AEventSSEPayload;
    try {
        event = JSON.parse(data) as A2AEventSSEPayload;
    } catch (e) {
        console.debug("[activityEventStore] dropping malformed activity_event frame", e);
        return tasks;
    }
    return upsertActivityEvent(tasks, event);
}
