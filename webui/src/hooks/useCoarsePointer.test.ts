// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, test } from "vitest";

import { isTouchPrimaryDevice, useIsTouchPrimaryDevice } from "./useCoarsePointer";
import { MOBILE_BREAKPOINT } from "./useMobile";

const originalMatchMedia = window.matchMedia;
const originalInnerWidth = window.innerWidth;

const listenersByQuery = new Map<string, Set<() => void>>();
const state = { coarse: false, fine: false };

function mockPointer({ coarse, fine, width = 1024 }: { coarse: boolean; fine: boolean; width?: number }) {
    state.coarse = coarse;
    state.fine = fine;
    listenersByQuery.clear();
    Object.defineProperty(window, "innerWidth", { writable: true, configurable: true, value: width });
    window.matchMedia = ((query: string) => {
        if (!listenersByQuery.has(query)) listenersByQuery.set(query, new Set());
        const listeners = listenersByQuery.get(query) as Set<() => void>;
        return {
            get matches() {
                if (query === "(pointer: coarse)") return state.coarse;
                if (query === "(any-pointer: fine)") return state.fine;
                if (query === `(max-width: ${MOBILE_BREAKPOINT - 1}px)`) return window.innerWidth < MOBILE_BREAKPOINT;
                return false;
            },
            media: query,
            onchange: null,
            addListener: () => {},
            removeListener: () => {},
            addEventListener: (_type: string, listener: () => void) => listeners.add(listener),
            removeEventListener: (_type: string, listener: () => void) => listeners.delete(listener),
            dispatchEvent: () => false,
        };
    }) as unknown as typeof window.matchMedia;
}

function setPointerState(update: Partial<{ coarse: boolean; fine: boolean; width: number }>) {
    if (update.coarse !== undefined) state.coarse = update.coarse;
    if (update.fine !== undefined) state.fine = update.fine;
    if (update.width !== undefined) Object.defineProperty(window, "innerWidth", { writable: true, configurable: true, value: update.width });
}

function fireChange(query: string) {
    listenersByQuery.get(query)?.forEach(listener => listener());
}

afterEach(() => {
    window.matchMedia = originalMatchMedia;
    Object.defineProperty(window, "innerWidth", { writable: true, configurable: true, value: originalInnerWidth });
});

describe("useIsTouchPrimaryDevice", () => {
    test("returns true on a touch-only device (phone/tablet)", () => {
        mockPointer({ coarse: true, fine: false });
        const { result } = renderHook(() => useIsTouchPrimaryDevice());
        expect(result.current).toBe(true);
    });

    test("returns false on a fine-pointer (mouse) device", () => {
        mockPointer({ coarse: false, fine: false });
        const { result } = renderHook(() => useIsTouchPrimaryDevice());
        expect(result.current).toBe(false);
    });

    test("returns false on a hybrid device with both a touchscreen and a trackpad/mouse", () => {
        mockPointer({ coarse: true, fine: true, width: 1024 });
        const { result } = renderHook(() => useIsTouchPrimaryDevice());
        expect(result.current).toBe(false);
    });

    test("returns true on a narrow-viewport touch device with a paired mouse", () => {
        mockPointer({ coarse: true, fine: true, width: MOBILE_BREAKPOINT - 1 });
        const { result } = renderHook(() => useIsTouchPrimaryDevice());
        expect(result.current).toBe(true);
    });

    test("recomputes live when the viewport crosses the mobile breakpoint", () => {
        mockPointer({ coarse: true, fine: true, width: 1024 });
        const { result } = renderHook(() => useIsTouchPrimaryDevice());
        expect(result.current).toBe(false);

        setPointerState({ width: MOBILE_BREAKPOINT - 1 });
        act(() => fireChange(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`));

        expect(result.current).toBe(true);
    });

    test("recomputes live when the primary pointer flips from fine to coarse", () => {
        mockPointer({ coarse: false, fine: true, width: 1024 });
        const { result } = renderHook(() => useIsTouchPrimaryDevice());
        expect(result.current).toBe(false);

        setPointerState({ coarse: true });
        act(() => fireChange("(pointer: coarse)"));

        expect(result.current).toBe(false);
    });

    test("recomputes live when a fine pointer connects on a touch-primary device", () => {
        mockPointer({ coarse: true, fine: false, width: 1024 });
        const { result } = renderHook(() => useIsTouchPrimaryDevice());
        expect(result.current).toBe(true);

        setPointerState({ fine: true });
        act(() => fireChange("(any-pointer: fine)"));

        expect(result.current).toBe(false);
    });
});

describe("isTouchPrimaryDevice", () => {
    test("returns false on a hybrid device with both a touchscreen and a trackpad/mouse", () => {
        mockPointer({ coarse: true, fine: true, width: 1024 });
        expect(isTouchPrimaryDevice()).toBe(false);
    });

    test("returns true on a touch-only device (phone/tablet)", () => {
        mockPointer({ coarse: true, fine: false });
        expect(isTouchPrimaryDevice()).toBe(true);
    });

    test("returns true on a narrow-viewport touch device with a paired mouse", () => {
        mockPointer({ coarse: true, fine: true, width: MOBILE_BREAKPOINT - 1 });
        expect(isTouchPrimaryDevice()).toBe(true);
    });
});

describe("isTouchPrimaryDevice — defensive guards", () => {
    test("returns false, rather than throwing, when matchMedia does not exist on the global", () => {
        // @ts-expect-error simulating a runtime with no matchMedia support at all
        window.matchMedia = undefined;
        expect(isTouchPrimaryDevice()).toBe(false);
    });

    test("returns false when matchMedia returns a partial polyfill missing `matches`", () => {
        window.matchMedia = ((query: string) => ({
            media: query,
            addEventListener: () => {},
            removeEventListener: () => {},
        })) as unknown as typeof window.matchMedia;
        expect(isTouchPrimaryDevice()).toBe(false);
    });

    test("useIsTouchPrimaryDevice does not throw when matchMedia does not exist on the global", () => {
        // @ts-expect-error simulating a runtime with no matchMedia support at all
        window.matchMedia = undefined;
        let result: ReturnType<typeof renderHook>["result"] | undefined;
        expect(() => {
            result = renderHook(() => useIsTouchPrimaryDevice()).result;
        }).not.toThrow();
        expect(result?.current).toBe(false);
    });
});
