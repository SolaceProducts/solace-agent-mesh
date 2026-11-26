// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { PINNED_AGENT_GRACE_MS, usePinnedAgentResolution } from "./usePinnedAgentResolution";

describe("usePinnedAgentResolution", () => {
    let warnSpy: ReturnType<typeof vi.spyOn>;

    beforeEach(() => {
        vi.useFakeTimers();
        warnSpy = vi.spyOn(console, "warn").mockImplementation(() => {});
    });

    afterEach(() => {
        vi.useRealTimers();
        warnSpy.mockRestore();
    });

    test("flips timedOut and warns after the grace period", () => {
        const { result } = renderHook(() => usePinnedAgentResolution(true, "Foo"));

        expect(result.current.timedOut).toBe(false);

        act(() => vi.advanceTimersByTime(PINNED_AGENT_GRACE_MS));

        expect(result.current.timedOut).toBe(true);
        expect(warnSpy).toHaveBeenCalledWith("pinned agent did not register within grace period", { agentName: "Foo", graceMs: PINNED_AGENT_GRACE_MS });
    });

    test("resets timedOut to false when the wait ends after having timed out", () => {
        const { result, rerender } = renderHook(({ awaiting }) => usePinnedAgentResolution(awaiting, "Foo"), { initialProps: { awaiting: true } });

        act(() => vi.advanceTimersByTime(PINNED_AGENT_GRACE_MS));
        expect(result.current.timedOut).toBe(true);

        rerender({ awaiting: false });
        expect(result.current.timedOut).toBe(false);
    });

    test("does not flip to unavailable if the agent connects before the grace elapses (no leaked timer)", () => {
        const { result, rerender } = renderHook(({ awaiting }) => usePinnedAgentResolution(awaiting, "Foo"), { initialProps: { awaiting: true } });

        rerender({ awaiting: false });

        act(() => vi.advanceTimersByTime(PINNED_AGENT_GRACE_MS * 2));

        expect(result.current.timedOut).toBe(false);
        expect(warnSpy).not.toHaveBeenCalled();
    });

    test("does not fire after unmount", () => {
        const { unmount } = renderHook(() => usePinnedAgentResolution(true, "Foo"));

        unmount();
        act(() => vi.advanceTimersByTime(PINNED_AGENT_GRACE_MS * 2));

        expect(warnSpy).not.toHaveBeenCalled();
    });
});
