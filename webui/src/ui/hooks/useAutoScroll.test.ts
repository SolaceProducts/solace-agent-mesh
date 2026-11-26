// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createElement } from "react";
import { act, render, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { useAutoScroll } from "./useAutoScroll";

function createMockScrollElement(): HTMLDivElement {
    const el = document.createElement("div");
    Object.defineProperty(el, "scrollHeight", { value: 100, writable: true, configurable: true });
    Object.defineProperty(el, "clientHeight", { value: 100, writable: true, configurable: true });
    Object.defineProperty(el, "scrollTop", { value: 0, writable: true, configurable: true });
    el.scrollTo = vi.fn();
    return el;
}

function assignScrollRef(result: { current: ReturnType<typeof useAutoScroll> }) {
    const el = createMockScrollElement();
    (result.current.scrollRef as React.MutableRefObject<HTMLDivElement>).current = el;
    return el;
}

let rafCallbacks: Array<() => void> = [];
const originalRaf = globalThis.requestAnimationFrame;

beforeEach(() => {
    rafCallbacks = [];
    globalThis.requestAnimationFrame = vi.fn((cb: FrameRequestCallback) => {
        rafCallbacks.push(() => cb(performance.now()));
        return rafCallbacks.length;
    }) as unknown as typeof requestAnimationFrame;
});

afterEach(() => {
    globalThis.requestAnimationFrame = originalRaf;
});

function flushRaf() {
    let safety = 10;
    while (rafCallbacks.length > 0 && safety-- > 0) {
        const batch = [...rafCallbacks];
        rafCallbacks = [];
        batch.forEach(cb => cb());
    }
}

describe("useAutoScroll", () => {
    describe("initial state", () => {
        test("starts at bottom with auto-scroll enabled", () => {
            const { result } = renderHook(() => useAutoScroll());

            expect(result.current.isAtBottom).toBe(true);
            expect(result.current.autoScrollEnabled).toBe(true);
            expect(result.current.userHasScrolled).toBe(false);
        });
    });

    describe("pauseAutoScroll", () => {
        test("disables auto-scroll and marks user as scrolled", () => {
            const { result } = renderHook(() => useAutoScroll());

            act(() => {
                result.current.pauseAutoScroll();
            });

            expect(result.current.isAtBottom).toBe(false);
            expect(result.current.autoScrollEnabled).toBe(false);
            expect(result.current.userHasScrolled).toBe(true);
        });

        test("clears isProgrammaticScroll after double-rAF so auto-scroll can resume", () => {
            const { result } = renderHook(() => useAutoScroll());

            act(() => {
                assignScrollRef(result);
            });

            act(() => {
                result.current.pauseAutoScroll();
            });

            expect(result.current.autoScrollEnabled).toBe(false);

            act(() => flushRaf());

            act(() => {
                result.current.scrollToBottom();
            });

            expect(result.current.isAtBottom).toBe(true);
            expect(result.current.autoScrollEnabled).toBe(true);
        });

        test("handles rapid double-call without premature flag reset", () => {
            const { result } = renderHook(() => useAutoScroll());

            act(() => {
                result.current.pauseAutoScroll();
                result.current.pauseAutoScroll();
            });

            expect(result.current.autoScrollEnabled).toBe(false);

            act(() => {
                const batch = [...rafCallbacks];
                rafCallbacks = [];
                batch.forEach(cb => cb());
            });

            expect(result.current.autoScrollEnabled).toBe(false);
        });
    });

    describe("scrollToBottom", () => {
        test("re-enables auto-scroll and resets isAtBottom", () => {
            const { result } = renderHook(() => useAutoScroll());

            act(() => {
                assignScrollRef(result);
            });

            act(() => {
                result.current.pauseAutoScroll();
            });
            expect(result.current.autoScrollEnabled).toBe(false);
            expect(result.current.isAtBottom).toBe(false);

            act(() => flushRaf());

            act(() => {
                result.current.scrollToBottom();
            });
            expect(result.current.autoScrollEnabled).toBe(true);
            expect(result.current.isAtBottom).toBe(true);
        });
    });

    describe("isProgrammaticScroll gate on content effects", () => {
        test("content height change during pauseAutoScroll does not trigger scrollToBottom", () => {
            const { result, rerender } = renderHook(({ content }) => useAutoScroll({ content }), { initialProps: { content: "initial" as React.ReactNode } });

            act(() => {
                result.current.pauseAutoScroll();
            });

            expect(result.current.autoScrollEnabled).toBe(false);
            expect(result.current.isAtBottom).toBe(false);

            rerender({ content: "updated content" as React.ReactNode });

            act(() => flushRaf());

            expect(result.current.autoScrollEnabled).toBe(false);
            expect(result.current.isAtBottom).toBe(false);
        });
    });

    describe("content that grows while a scroll to the bottom is in flight", () => {
        const SETTLE_AFTER_SMOOTH_MS = 500;
        const SETTLE_AFTER_INSTANT_MS = 50;

        function mountWithScrolledElement(scrollHeight: number) {
            const hook = renderHook(() => useAutoScroll());
            let el!: HTMLDivElement;
            act(() => {
                el = assignScrollRef(hook.result);
            });
            const size = el as unknown as { scrollHeight: number; clientHeight: number };
            size.scrollHeight = scrollHeight;
            size.clientHeight = 100;
            return { ...hook, el, size };
        }

        beforeEach(() => {
            vi.useFakeTimers();
        });

        afterEach(() => {
            vi.useRealTimers();
        });

        test("follows growth that landed while the scroll guard was up", () => {
            const { result, el, size } = mountWithScrolledElement(400);

            act(() => result.current.scrollToBottom());
            expect(el.scrollTo).toHaveBeenCalledWith(expect.objectContaining({ top: 300 }));

            size.scrollHeight = 600;
            act(() => {
                vi.advanceTimersByTime(SETTLE_AFTER_SMOOTH_MS);
            });

            expect(el.scrollTop).toBe(500);
        });

        test("keeps following when the content grows again during the catch-up scroll", () => {
            const { result, el, size } = mountWithScrolledElement(400);

            act(() => result.current.scrollToBottom());
            size.scrollHeight = 600;
            act(() => {
                vi.advanceTimersByTime(SETTLE_AFTER_SMOOTH_MS);
            });
            size.scrollHeight = 900;
            act(() => {
                vi.advanceTimersByTime(SETTLE_AFTER_INSTANT_MS);
            });

            expect(el.scrollTop).toBe(800);
        });

        test("does not scroll again when the growth is within the at-bottom tolerance", () => {
            const { result, el, size } = mountWithScrolledElement(400);

            act(() => result.current.scrollToBottom());
            size.scrollHeight = 410;
            act(() => {
                vi.advanceTimersByTime(SETTLE_AFTER_SMOOTH_MS);
            });

            expect(el.scrollTop).toBe(0);
        });

        test("scrolls for growth just past the at-bottom tolerance and not for growth exactly at it", () => {
            const atTolerance = mountWithScrolledElement(400);
            act(() => atTolerance.result.current.scrollToBottom());
            atTolerance.size.scrollHeight = 420;
            act(() => {
                vi.advanceTimersByTime(SETTLE_AFTER_SMOOTH_MS);
            });
            expect(atTolerance.el.scrollTop).toBe(0);

            const pastTolerance = mountWithScrolledElement(400);
            act(() => pastTolerance.result.current.scrollToBottom());
            pastTolerance.size.scrollHeight = 421;
            act(() => {
                vi.advanceTimersByTime(SETTLE_AFTER_SMOOTH_MS);
            });
            expect(pastTolerance.el.scrollTop).toBe(321);
        });

        test("measures the tolerance with the offset it was given", () => {
            const hook = renderHook(() => useAutoScroll({ offset: 50 }));
            let el!: HTMLDivElement;
            act(() => {
                el = assignScrollRef(hook.result);
            });
            const size = el as unknown as { scrollHeight: number; clientHeight: number };
            size.scrollHeight = 400;
            size.clientHeight = 100;

            act(() => hook.result.current.scrollToBottom());
            size.scrollHeight = 440;
            act(() => {
                vi.advanceTimersByTime(SETTLE_AFTER_SMOOTH_MS);
            });

            expect(el.scrollTop).toBe(0);
        });

        test("leaves the list alone when it unmounts with the catch-up pending", () => {
            const { result, el, size, unmount } = mountWithScrolledElement(400);

            act(() => result.current.scrollToBottom());
            size.scrollHeight = 600;
            unmount();
            act(() => {
                vi.advanceTimersByTime(SETTLE_AFTER_SMOOTH_MS);
            });

            expect(el.scrollTop).toBe(0);
        });

        test("once the guard has dropped, an upward scroll is the reader's and switches auto-scroll off", () => {
            let hook!: ReturnType<typeof useAutoScroll>;
            function Harness() {
                hook = useAutoScroll();
                return createElement("div", { ref: hook.scrollRef, "data-testid": "scroller" });
            }
            const view = render(createElement(Harness));
            const el = view.getByTestId("scroller") as HTMLDivElement;
            Object.defineProperty(el, "scrollHeight", { value: 400, configurable: true });
            Object.defineProperty(el, "clientHeight", { value: 100, configurable: true });
            Object.defineProperty(el, "scrollTop", { value: 0, writable: true, configurable: true });
            el.scrollTo = vi.fn();

            act(() => hook.scrollToBottom());
            el.scrollTop = 300;
            act(() => {
                vi.advanceTimersByTime(SETTLE_AFTER_SMOOTH_MS);
            });
            el.scrollTop = 100;
            act(() => {
                el.dispatchEvent(new Event("scroll"));
            });

            expect(hook.autoScrollEnabled).toBe(false);
        });

        test("leaves a reader who scrolled up on purpose where they are", () => {
            const { result, el, size } = mountWithScrolledElement(400);

            act(() => result.current.scrollToBottom());
            act(() => result.current.disableAutoScroll());
            size.scrollHeight = 600;
            act(() => {
                vi.advanceTimersByTime(SETTLE_AFTER_SMOOTH_MS);
            });

            expect(el.scrollTop).toBe(0);
            expect(result.current.autoScrollEnabled).toBe(false);
        });

        test("does not follow while a pause is holding auto-scroll off", () => {
            const { result, el, size } = mountWithScrolledElement(400);

            act(() => result.current.scrollToBottom());
            act(() => {
                void result.current.pauseAutoScroll();
            });
            size.scrollHeight = 600;
            act(() => {
                vi.advanceTimersByTime(SETTLE_AFTER_SMOOTH_MS);
            });

            expect(el.scrollTop).toBe(0);
        });
    });

    describe("disableAutoScroll", () => {
        test("disables auto-scroll when not at bottom", () => {
            const { result } = renderHook(() => useAutoScroll());

            act(() => {
                result.current.disableAutoScroll();
            });

            expect(result.current.autoScrollEnabled).toBe(false);
        });
    });
});
