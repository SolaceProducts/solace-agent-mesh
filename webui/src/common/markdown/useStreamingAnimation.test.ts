// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { STREAMING_EMIT_INTERVAL_MS } from "./streaming";
import { useStreamingAnimation } from "./useStreamingAnimation";
import type { StreamingState } from "./useStreamingSpeed";

let now = 0;
let rafQueue: FrameRequestCallback[] = [];

beforeEach(() => {
    now = 1_000_000;
    rafQueue = [];
    vi.spyOn(Date, "now").mockImplementation(() => now);
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => rafQueue.push(cb));
    vi.stubGlobal("cancelAnimationFrame", () => {});
});

afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
});

function frame(dtMs: number) {
    act(() => {
        now += dtMs;
        const callbacks = rafQueue;
        rafQueue = [];
        for (const cb of callbacks) {
            cb(now);
        }
    });
}

function makeState(over: Partial<StreamingState> = {}): { current: StreamingState } {
    return { current: { cursor: 0, speed: 0.05, lastArrivalTime: 0, avgInterval: 500, lastLen: 0, ...over } };
}

describe("useStreamingAnimation", () => {
    test("coalesces re-renders to STREAMING_EMIT_INTERVAL_MS while content streams", () => {
        const state = makeState({ speed: 0.1 });
        const contentRef = { current: "abcdefghijklmnopqrstuvwxyz" };
        const { result } = renderHook(() => useStreamingAnimation(state, contentRef));

        expect(result.current).toBe("");

        frame(20);
        const firstEmit = result.current;
        expect(firstEmit).toBe("ab");

        frame(16);
        frame(16);
        expect(result.current).toBe(firstEmit);

        frame(30);
        expect(result.current.length).toBeGreaterThan(firstEmit.length);
        expect("abcdefghijklmnopqrstuvwxyz".startsWith(result.current)).toBe(true);
    });

    test("always flushes the final fully-revealed text even when the cursor reaches the end during a throttled frame", () => {
        const content = "hello world";
        const state = makeState({ speed: 0.05 });
        const contentRef = { current: content };
        const { result } = renderHook(() => useStreamingAnimation(state, contentRef));

        state.current.cursor = 4;
        frame(20);
        expect(result.current).toBe("hello");
        expect(result.current).not.toBe(content);

        state.current.cursor = 10.6;
        state.current.speed = 1;
        frame(20);
        expect(result.current).toBe("hello");

        frame(20);
        expect(result.current).toBe(content);
    });

    test("snaps the display down when the target content shrinks (reset)", () => {
        const state = makeState({ speed: 1, cursor: 0 });
        const contentRef = { current: "the quick brown fox" };
        const { result } = renderHook(() => useStreamingAnimation(state, contentRef));

        frame(50);
        expect(result.current).toBe("the quick brown fox");

        contentRef.current = "hi";
        frame(50);
        expect(result.current).toBe("hi");
    });
});

test("STREAMING_EMIT_INTERVAL_MS stays in a sane coalescing range", () => {
    expect(STREAMING_EMIT_INTERVAL_MS).toBeGreaterThan(16);
    expect(STREAMING_EMIT_INTERVAL_MS).toBeLessThanOrEqual(100);
});
