// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import * as apiClientModule from "@/api/client";

import type { SseErrorRecoveryCallbacks, SseErrorRecoveryState } from "./useSseErrorRecovery";
import { useSseErrorRecovery } from "./useSseErrorRecovery";

function createMockCallbacks(): SseErrorRecoveryCallbacks {
    return {
        closeCurrentEventSource: vi.fn(),
        setError: vi.fn(),
        setIsResponding: vi.fn(),
        setCurrentTaskId: vi.fn(),
        cleanupMessages: vi.fn(),
    };
}

function createMockState(overrides: Partial<SseErrorRecoveryState> = {}): SseErrorRecoveryState {
    return {
        isResponding: true,
        isFinalizing: { current: false },
        isCancelling: { current: false },
        currentTaskId: "task-123",
        ...overrides,
    };
}

let refreshTokenSpy: ReturnType<typeof vi.spyOn>;

describe("useSseErrorRecovery", () => {
    beforeEach(() => {
        refreshTokenSpy = vi.spyOn(apiClientModule, "refreshToken");
    });

    afterEach(() => {
        refreshTokenSpy.mockRestore();
        vi.restoreAllMocks();
    });

    describe("initial state", () => {
        test("returns sseReconnectKey starting at 0", () => {
            const callbacks = createMockCallbacks();
            const state = createMockState();

            const { result } = renderHook(() => useSseErrorRecovery(state, callbacks));

            expect(result.current.sseReconnectKey).toBe(0);
        });

        test("returns handleSseError function", () => {
            const callbacks = createMockCallbacks();
            const state = createMockState();

            const { result } = renderHook(() => useSseErrorRecovery(state, callbacks));

            expect(typeof result.current.handleSseError).toBe("function");
        });

        test("returns cleanupSseFailure function", () => {
            const callbacks = createMockCallbacks();
            const state = createMockState();

            const { result } = renderHook(() => useSseErrorRecovery(state, callbacks));

            expect(typeof result.current.cleanupSseFailure).toBe("function");
        });
    });

    describe("cleanupSseFailure", () => {
        test("calls setError, setIsResponding, setCurrentTaskId, and cleanupMessages", () => {
            const callbacks = createMockCallbacks();
            const state = createMockState();

            const { result } = renderHook(() => useSseErrorRecovery(state, callbacks));

            act(() => {
                result.current.cleanupSseFailure("Test Title", "Test message");
            });

            expect(callbacks.setError).toHaveBeenCalledWith({ title: "Test Title", error: "Test message", errorSource: "client" });
            expect(callbacks.setIsResponding).toHaveBeenCalledWith(false);
            expect(callbacks.setCurrentTaskId).toHaveBeenCalledWith(null);
            expect(callbacks.cleanupMessages).toHaveBeenCalled();
        });
    });

    describe("handleSseError — token refresh path", () => {
        test("attempts token refresh on first SSE error when responding", async () => {
            const callbacks = createMockCallbacks();
            const state = createMockState({ isResponding: true, currentTaskId: "task-123" });
            refreshTokenSpy.mockResolvedValue("new-token");

            const { result } = renderHook(() => useSseErrorRecovery(state, callbacks));

            act(() => {
                result.current.handleSseError();
            });

            expect(callbacks.closeCurrentEventSource).toHaveBeenCalled();
            expect(refreshTokenSpy).toHaveBeenCalled();

            await act(async () => {
                await new Promise(resolve => setTimeout(resolve, 0));
            });

            expect(result.current.sseReconnectKey).toBe(1);
        });

        test("calls cleanupSseFailure when refresh returns null", async () => {
            const callbacks = createMockCallbacks();
            const state = createMockState({ isResponding: true, currentTaskId: "task-456" });
            refreshTokenSpy.mockResolvedValue(null);

            const { result } = renderHook(() => useSseErrorRecovery(state, callbacks));

            act(() => {
                result.current.handleSseError();
            });

            await act(async () => {
                await new Promise(resolve => setTimeout(resolve, 0));
            });

            expect(callbacks.setError).toHaveBeenCalledWith({
                title: "Connection Failed",
                error: "Session expired. Please log in again.",
                errorSource: "client",
            });
            expect(callbacks.setIsResponding).toHaveBeenCalledWith(false);
            expect(callbacks.setCurrentTaskId).toHaveBeenCalledWith(null);
        });

        test("calls cleanupSseFailure when refresh throws", async () => {
            const callbacks = createMockCallbacks();
            const state = createMockState({ isResponding: true, currentTaskId: "task-789" });
            refreshTokenSpy.mockRejectedValue(new Error("Network error"));

            const { result } = renderHook(() => useSseErrorRecovery(state, callbacks));

            act(() => {
                result.current.handleSseError();
            });

            await act(async () => {
                await new Promise(resolve => setTimeout(resolve, 0));
            });

            expect(callbacks.setError).toHaveBeenCalledWith({
                title: "Connection Failed",
                error: "Connection lost. Please try again.",
                errorSource: "client",
            });
            expect(callbacks.setIsResponding).toHaveBeenCalledWith(false);
            expect(callbacks.setCurrentTaskId).toHaveBeenCalledWith(null);
        });

        test("does not attempt refresh on second SSE error (retry-once guard)", async () => {
            const callbacks = createMockCallbacks();
            const state = createMockState({ isResponding: true, currentTaskId: "task-123" });
            refreshTokenSpy.mockResolvedValue("new-token");

            const { result } = renderHook(() => useSseErrorRecovery(state, callbacks));

            act(() => {
                result.current.handleSseError();
            });

            await act(async () => {
                await new Promise(resolve => setTimeout(resolve, 0));
            });

            expect(result.current.sseReconnectKey).toBe(1);

            refreshTokenSpy.mockClear();
            (callbacks.closeCurrentEventSource as ReturnType<typeof vi.fn>).mockClear();

            act(() => {
                result.current.handleSseError();
            });

            expect(refreshTokenSpy).not.toHaveBeenCalled();
            expect(callbacks.setError).toHaveBeenCalledWith({
                title: "Connection Failed",
                error: "Connection lost. Please try again.",
                errorSource: "client",
            });
        });
    });

    describe("handleSseError — non-refresh path", () => {
        test("does not attempt refresh when not responding", () => {
            const callbacks = createMockCallbacks();
            const state = createMockState({ isResponding: false, currentTaskId: "task-123" });

            const { result } = renderHook(() => useSseErrorRecovery(state, callbacks));

            act(() => {
                result.current.handleSseError();
            });

            expect(refreshTokenSpy).not.toHaveBeenCalled();
            expect(callbacks.cleanupMessages).toHaveBeenCalled();
        });

        test("does not attempt refresh when isFinalizing", () => {
            const callbacks = createMockCallbacks();
            const state = createMockState({
                isResponding: true,
                currentTaskId: "task-123",
                isFinalizing: { current: true },
            });

            const { result } = renderHook(() => useSseErrorRecovery(state, callbacks));

            act(() => {
                result.current.handleSseError();
            });

            expect(refreshTokenSpy).not.toHaveBeenCalled();
            expect(callbacks.setError).not.toHaveBeenCalled();
            expect(callbacks.cleanupMessages).toHaveBeenCalled();
        });

        test("does not attempt refresh when isCancelling", () => {
            const callbacks = createMockCallbacks();
            const state = createMockState({
                isResponding: true,
                currentTaskId: "task-123",
                isCancelling: { current: true },
            });

            const { result } = renderHook(() => useSseErrorRecovery(state, callbacks));

            act(() => {
                result.current.handleSseError();
            });

            expect(refreshTokenSpy).not.toHaveBeenCalled();
            expect(callbacks.setError).not.toHaveBeenCalled();
        });

        test("does not attempt refresh when no currentTaskId", () => {
            const callbacks = createMockCallbacks();
            const state = createMockState({ isResponding: true, currentTaskId: null });

            const { result } = renderHook(() => useSseErrorRecovery(state, callbacks));

            act(() => {
                result.current.handleSseError();
            });

            expect(refreshTokenSpy).not.toHaveBeenCalled();
        });
    });

    describe("task ID change resets refresh guard", () => {
        test("resets sseRefreshAttempted when task ID changes", async () => {
            const callbacks = createMockCallbacks();
            const state = createMockState({ isResponding: true, currentTaskId: "task-A" });
            refreshTokenSpy.mockResolvedValue("new-token");

            const { result, rerender } = renderHook(({ taskId }) => useSseErrorRecovery({ ...state, currentTaskId: taskId }, callbacks), { initialProps: { taskId: "task-A" } });

            act(() => {
                result.current.handleSseError();
            });

            await act(async () => {
                await new Promise(resolve => setTimeout(resolve, 0));
            });

            expect(result.current.sseReconnectKey).toBe(1);

            refreshTokenSpy.mockClear();

            rerender({ taskId: "task-B" });

            act(() => {
                result.current.handleSseError();
            });

            expect(refreshTokenSpy).toHaveBeenCalled();
        });
    });
});
