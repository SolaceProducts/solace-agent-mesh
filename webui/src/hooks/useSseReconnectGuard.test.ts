// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { SseReconnectGuardCallbacks, SseReconnectGuardState } from "./useSseReconnectGuard";
import { useSseReconnectGuard } from "./useSseReconnectGuard";

const CONNECTING = 0;
const OPEN = 1;
const CLOSED = 2;

const GRACE_MS = 45_000;

function createCallbacks(): SseReconnectGuardCallbacks {
    return {
        closeCurrentEventSource: vi.fn(),
        setError: vi.fn(),
        setIsResponding: vi.fn(),
        setCurrentTaskId: vi.fn(),
        setLatestStatusText: vi.fn(),
        cleanupMessages: vi.fn(),
        requestManualRetry: vi.fn(),
        takeTaskTrace: vi.fn(() => undefined),
    };
}

interface FakeES {
    readyState: number;
}

function createState(readyState: number | null, overrides: Partial<SseReconnectGuardState> = {}): { state: SseReconnectGuardState; es: FakeES | null } {
    const es: FakeES | null = readyState === null ? null : { readyState };
    const state: SseReconnectGuardState = {
        isResponding: true,
        isFinalizing: { current: false },
        isCancelling: { current: false },
        currentTaskId: "task-1",
        getEventSource: () => es as unknown as EventSource | null,
        ...overrides,
    };
    return { state, es };
}

function render(state: SseReconnectGuardState, callbacks: SseReconnectGuardCallbacks) {
    return renderHook(({ s, c }) => useSseReconnectGuard(s, c, GRACE_MS), { initialProps: { s: state, c: callbacks } });
}

beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal("EventSource", { CONNECTING, OPEN, CLOSED });
});

afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

describe("useSseReconnectGuard — transient drop", () => {
    test("does NOT tear down while the browser is auto-reconnecting (CONNECTING)", () => {
        const callbacks = createCallbacks();
        const { state } = createState(CONNECTING);
        const { result } = render(state, callbacks);

        act(() => result.current.handleSseError());

        expect(callbacks.setError).not.toHaveBeenCalled();
        expect(callbacks.setIsResponding).not.toHaveBeenCalled();
        expect(callbacks.setCurrentTaskId).not.toHaveBeenCalled();
        expect(callbacks.closeCurrentEventSource).not.toHaveBeenCalled();
    });

    test("fails the connection if the reconnect never recovers within the grace window", () => {
        const callbacks = createCallbacks();
        const { state } = createState(CONNECTING);
        const { result } = render(state, callbacks);

        act(() => result.current.handleSseError());
        expect(callbacks.setError).not.toHaveBeenCalled();

        act(() => vi.advanceTimersByTime(GRACE_MS));

        expect(callbacks.setError).toHaveBeenCalledWith(expect.objectContaining({ title: "Connection Failed" }));
        expect(callbacks.setIsResponding).toHaveBeenCalledWith(false);
        expect(callbacks.closeCurrentEventSource).toHaveBeenCalled();
        expect(callbacks.setCurrentTaskId).toHaveBeenCalledWith(null);
        expect(callbacks.cleanupMessages).toHaveBeenCalled();
    });

    test("handleSseOpen cancels a pending grace timer so a recovered drop is silent", () => {
        const callbacks = createCallbacks();
        const { state } = createState(CONNECTING);
        const { result } = render(state, callbacks);

        act(() => result.current.handleSseError());
        act(() => result.current.handleSseOpen());
        act(() => vi.advanceTimersByTime(GRACE_MS));

        expect(callbacks.setError).not.toHaveBeenCalled();
        expect(callbacks.setCurrentTaskId).not.toHaveBeenCalled();
    });

    test("grace timer no-ops if the stream reconnected (readyState OPEN) by the time it fires", () => {
        const callbacks = createCallbacks();
        const { state, es } = createState(CONNECTING);
        const { result } = render(state, callbacks);

        act(() => result.current.handleSseError());
        if (es) es.readyState = OPEN;
        act(() => vi.advanceTimersByTime(GRACE_MS));

        expect(callbacks.setError).not.toHaveBeenCalled();
    });

    test("arms only one grace timer across repeated CONNECTING errors", () => {
        const callbacks = createCallbacks();
        const { state } = createState(CONNECTING);
        const { result } = render(state, callbacks);

        act(() => result.current.handleSseError());
        act(() => result.current.handleSseError());
        act(() => result.current.handleSseError());
        act(() => vi.advanceTimersByTime(GRACE_MS));

        expect(callbacks.setError).toHaveBeenCalledTimes(1);
    });

    test("clearReconnectGraceTimer cancels a pending failure", () => {
        const callbacks = createCallbacks();
        const { state } = createState(CONNECTING);
        const { result } = render(state, callbacks);

        act(() => result.current.handleSseError());
        act(() => result.current.clearReconnectGraceTimer());
        act(() => vi.advanceTimersByTime(GRACE_MS));

        expect(callbacks.setError).not.toHaveBeenCalled();
    });
});

describe("useSseReconnectGuard — fatal error", () => {
    test("tears down immediately when the browser gave up (CLOSED)", () => {
        const callbacks = createCallbacks();
        const { state } = createState(CLOSED);
        const { result } = render(state, callbacks);

        act(() => result.current.handleSseError());

        expect(callbacks.setError).toHaveBeenCalledWith(expect.objectContaining({ title: "Connection Failed" }));
        expect(callbacks.setIsResponding).toHaveBeenCalledWith(false);
        expect(callbacks.setCurrentTaskId).toHaveBeenCalledWith(null);
    });

    test("tears down immediately when there is no EventSource", () => {
        const callbacks = createCallbacks();
        const { state } = createState(null);
        const { result } = render(state, callbacks);

        act(() => result.current.handleSseError());

        expect(callbacks.closeCurrentEventSource).toHaveBeenCalled();
        expect(callbacks.setCurrentTaskId).toHaveBeenCalledWith(null);
    });

    test("names the interrupted task's run on the error it raises", () => {
        const callbacks = createCallbacks();
        (callbacks.takeTaskTrace as ReturnType<typeof vi.fn>).mockReturnValue("0199b2c1-0000-7000-8000-000000000003");
        const { state } = createState(CLOSED);
        const { result } = render(state, callbacks);

        act(() => result.current.handleSseError());

        expect(callbacks.setError).toHaveBeenCalledWith(expect.objectContaining({ traceId: "0199b2c1-0000-7000-8000-000000000003" }));
    });

    test("leaves the trace alone when no error is raised", () => {
        const callbacks = createCallbacks();
        const { state } = createState(CLOSED, { isFinalizing: { current: true } });
        const { result } = render(state, callbacks);

        act(() => result.current.handleSseError());

        expect(callbacks.takeTaskTrace).not.toHaveBeenCalled();
    });

    test("Retry action re-subscribes to the interrupted task", () => {
        const callbacks = createCallbacks();
        const { state } = createState(CLOSED, { currentTaskId: "task-77" });
        const { result } = render(state, callbacks);

        act(() => result.current.handleSseError());

        const arg = (callbacks.setError as ReturnType<typeof vi.fn>).mock.calls[0][0];
        expect(typeof arg.onRetry).toBe("function");
        act(() => arg.onRetry());
        expect(callbacks.requestManualRetry).toHaveBeenCalledWith("task-77");
    });
});

describe("useSseReconnectGuard — guarded states", () => {
    test("does not surface an error while finalizing, but still cleans up", () => {
        const callbacks = createCallbacks();
        const { state } = createState(CLOSED, { isFinalizing: { current: true } });
        const { result } = render(state, callbacks);

        act(() => result.current.handleSseError());

        expect(callbacks.setError).not.toHaveBeenCalled();
        expect(callbacks.setIsResponding).not.toHaveBeenCalled();
        expect(callbacks.cleanupMessages).toHaveBeenCalled();
    });

    test("preserves the task while cancelling (no setCurrentTaskId null)", () => {
        const callbacks = createCallbacks();
        const { state } = createState(CLOSED, { isCancelling: { current: true } });
        const { result } = render(state, callbacks);

        act(() => result.current.handleSseError());

        expect(callbacks.setError).not.toHaveBeenCalled();
        expect(callbacks.closeCurrentEventSource).not.toHaveBeenCalled();
        expect(callbacks.setCurrentTaskId).not.toHaveBeenCalled();
    });

    test("a CONNECTING drop while not responding is treated as fatal (no grace)", () => {
        const callbacks = createCallbacks();
        const { state } = createState(CONNECTING, { isResponding: false });
        const { result } = render(state, callbacks);

        act(() => result.current.handleSseError());

        expect(callbacks.cleanupMessages).toHaveBeenCalled();
        expect(callbacks.setError).not.toHaveBeenCalled();
    });
});
