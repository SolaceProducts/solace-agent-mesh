// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useTaskActivityEvents } from "./useTaskActivityEvents";

class MockEventSource {
    static instances: MockEventSource[] = [];
    url: string;
    closed = false;
    onopen: (() => void) | null = null;
    onerror: (() => void) | null = null;
    private listeners: Record<string, ((e: MessageEvent) => void)[]> = {};

    constructor(url: string) {
        this.url = url;
        MockEventSource.instances.push(this);
    }
    addEventListener(type: string, cb: (e: MessageEvent) => void) {
        (this.listeners[type] ??= []).push(cb);
    }
    removeEventListener(type: string, cb: (e: MessageEvent) => void) {
        this.listeners[type] = (this.listeners[type] ?? []).filter(l => l !== cb);
    }
    close() {
        this.closed = true;
    }
    emit(type: string, data: string) {
        (this.listeners[type] ?? []).forEach(cb => cb({ data } as MessageEvent));
    }
    static latest() {
        return MockEventSource.instances[MockEventSource.instances.length - 1];
    }
}

const frame = (taskId: string) =>
    JSON.stringify({
        event_type: "a2a_message",
        timestamp: "2026-01-01T00:00:00.000Z",
        solace_topic: "",
        direction: "status_update",
        source_entity: "A",
        target_entity: "",
        full_payload: {},
        task_id: taskId,
    });

beforeEach(() => {
    MockEventSource.instances = [];
    vi.stubGlobal("EventSource", MockEventSource);
});
afterEach(() => {
    vi.unstubAllGlobals();
});

describe("useTaskActivityEvents", () => {
    it("opens a per-task subscription and accumulates activity_event frames when enabled", () => {
        const { result } = renderHook(() => useTaskActivityEvents("t1", true));
        expect(MockEventSource.instances).toHaveLength(1);
        expect(MockEventSource.latest().url).toContain("/api/v1/sse/subscribe/t1");

        act(() => MockEventSource.latest().emit("activity_event", frame("t1")));
        expect(result.current.t1?.events).toHaveLength(1);
    });

    it("opens nothing when disabled", () => {
        const { result } = renderHook(() => useTaskActivityEvents("t1", false));
        expect(MockEventSource.instances).toHaveLength(0);
        expect(result.current).toEqual({});
    });

    it("opens nothing when taskId is null", () => {
        renderHook(() => useTaskActivityEvents(null, true));
        expect(MockEventSource.instances).toHaveLength(0);
    });

    it("closes the stream on unmount", () => {
        const { unmount } = renderHook(() => useTaskActivityEvents("t1", true));
        const es = MockEventSource.latest();
        unmount();
        expect(es.closed).toBe(true);
    });

    it("reconnects with backoff after an error", () => {
        vi.useFakeTimers();
        try {
            renderHook(() => useTaskActivityEvents("t1", true));
            const es = MockEventSource.latest();
            act(() => es.onerror?.());
            expect(es.closed).toBe(true);
            act(() => vi.advanceTimersByTime(60000));
            expect(MockEventSource.instances).toHaveLength(2);
            expect(MockEventSource.latest()).not.toBe(es);
            expect(MockEventSource.latest().url).toContain("/api/v1/sse/subscribe/t1");
        } finally {
            vi.useRealTimers();
        }
    });

    it("stops reconnecting after the retry budget is exhausted", () => {
        vi.useFakeTimers();
        try {
            renderHook(() => useTaskActivityEvents("t1", true));
            for (let i = 0; i < 5; i++) {
                act(() => MockEventSource.latest().onerror?.());
                act(() => vi.advanceTimersByTime(60000));
            }
            expect(MockEventSource.instances).toHaveLength(6);
            act(() => MockEventSource.latest().onerror?.());
            act(() => vi.advanceTimersByTime(60000));
            expect(MockEventSource.instances).toHaveLength(6);
        } finally {
            vi.useRealTimers();
        }
    });

    it("resets the retry budget after a connection stays healthy", () => {
        vi.useFakeTimers();
        try {
            renderHook(() => useTaskActivityEvents("t1", true));
            for (let i = 0; i < 5; i++) {
                act(() => MockEventSource.latest().onerror?.());
                act(() => vi.advanceTimersByTime(60000));
            }
            expect(MockEventSource.instances).toHaveLength(6);

            const healthy = MockEventSource.latest();
            act(() => healthy.onopen?.());
            act(() => vi.advanceTimersByTime(11000));
            act(() => healthy.onerror?.());
            act(() => vi.advanceTimersByTime(60000));
            expect(MockEventSource.instances).toHaveLength(7);
        } finally {
            vi.useRealTimers();
        }
    });

    it("clears a pending reconnect timer on unmount", () => {
        vi.useFakeTimers();
        try {
            const { unmount } = renderHook(() => useTaskActivityEvents("t1", true));
            act(() => MockEventSource.latest().onerror?.());
            unmount();
            act(() => vi.advanceTimersByTime(60000));
            expect(MockEventSource.instances).toHaveLength(1);
        } finally {
            vi.useRealTimers();
        }
    });
});
