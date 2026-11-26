// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { QueryClient } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, test, vi } from "vitest";

const addNotification = vi.fn();
const invalidateQueries = vi.fn();

const handlers = new Map<string, (event: MessageEvent) => void>();

async function loadHook() {
    vi.resetModules();
    handlers.clear();

    vi.doMock("@/providers/SSEProvider", () => ({
        useSSESubscription: ({ eventType, onMessage }: { eventType: string; onMessage: (event: MessageEvent) => void }) => {
            handlers.set(eventType, onMessage);
            return { isConnected: true };
        },
    }));

    vi.doMock("@/providers/contexts", () => ({
        useChatContext: () => ({ addNotification }),
    }));

    vi.doMock("@tanstack/react-query", async importOriginal => {
        const actual = (await importOriginal()) as Record<string, unknown>;
        return { ...actual, useQueryClient: () => ({ invalidateQueries }) as unknown as QueryClient };
    });

    const { useNotificationSSE } = await import("./useNotificationSSE");
    renderHook(() => useNotificationSSE());
}

const fire = (eventType: string, data: string) => {
    const handler = handlers.get(eventType);
    if (!handler) throw new Error(`no handler registered for ${eventType}`);
    handler({ data } as MessageEvent);
};

const invalidatedKeys = () => invalidateQueries.mock.calls.map(([arg]) => JSON.stringify(arg?.queryKey));

beforeEach(() => {
    vi.clearAllMocks();
});

describe("useNotificationSSE chat_shared", () => {
    test("subscribes to the chat_shared event", async () => {
        await loadHook();
        expect(handlers.has("chat_shared")).toBe(true);
    });

    test("refreshes the shared-with-me caches and toasts on a new share", async () => {
        await loadHook();

        fire("chat_shared", JSON.stringify({ shareId: "sh-1", title: "Q3 planning", ownerEmail: "dana@example.com", reason: "shared" }));

        expect(invalidatedKeys()).toContain(JSON.stringify(["shares", "shared-with-me"]));
        expect(addNotification).toHaveBeenCalledWith('dana@example.com shared "Q3 planning" with you', "info");
    });

    test("refreshes but does not toast when access merely changed", async () => {
        await loadHook();

        fire("chat_shared", JSON.stringify({ shareId: "sh-1", title: "Q3 planning", ownerEmail: "dana@example.com", reason: "access_changed" }));

        expect(invalidatedKeys()).toContain(JSON.stringify(["shares", "shared-with-me"]));
        expect(addNotification).not.toHaveBeenCalled();
    });

    test("still refreshes the caches when the payload is malformed", async () => {
        await loadHook();

        expect(() => fire("chat_shared", "{not json")).not.toThrow();

        expect(invalidatedKeys()).toContain(JSON.stringify(["shares", "shared-with-me"]));
        expect(addNotification).not.toHaveBeenCalled();
    });

    test("falls back to generic wording when the payload omits details", async () => {
        await loadHook();

        fire("chat_shared", JSON.stringify({ reason: "shared" }));

        expect(addNotification).toHaveBeenCalledWith('Someone shared "a chat" with you', "info");
    });

    test("leaves the existing scheduled-task subscriptions in place", async () => {
        await loadHook();
        for (const eventType of ["session_created", "execution_queued", "execution_started", "task_auto_paused"]) {
            expect(handlers.has(eventType)).toBe(true);
        }
    });
});

describe("useNotificationSSE session_created", () => {
    test("refreshes the scheduled tasks and the chat list when a hidden quiet run announces no session", async () => {
        await loadHook();
        const refreshed = vi.fn();
        window.addEventListener("new-chat-session", refreshed);

        fire("session_created", JSON.stringify({ execution_id: "exec-1", task_id: "task-1", task_name: "Check the queue", session_id: "", status: "quiet" }));

        window.removeEventListener("new-chat-session", refreshed);
        expect(invalidatedKeys()).toContain(JSON.stringify(["scheduled-tasks"]));
        expect(refreshed).toHaveBeenCalledTimes(1);
        expect(addNotification).not.toHaveBeenCalled();
    });
});
