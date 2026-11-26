// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useZoomContext, ZOOM_DEFAULT, ZOOM_MAX, ZOOM_MIN, ZOOM_STEP } from "@/providers/contexts/ZoomContext";

import { ZoomProvider } from "./ZoomProvider";

const STORAGE_KEY = "sam-zoom";

function setDesktop(enabled: boolean) {
    const w = window as unknown as { runtime?: object };
    if (enabled) {
        w.runtime = {};
    } else {
        delete w.runtime;
    }
}

function wrapper({ children }: { children: React.ReactNode }) {
    return <ZoomProvider>{children}</ZoomProvider>;
}

function pressKey(key: string, opts: { meta?: boolean; ctrl?: boolean } = { meta: true }) {
    const event = new KeyboardEvent("keydown", {
        key,
        metaKey: opts.meta ?? false,
        ctrlKey: opts.ctrl ?? false,
        bubbles: true,
        cancelable: true,
    });
    act(() => {
        window.dispatchEvent(event);
    });
    return event;
}

describe("useZoomContext", () => {
    afterEach(() => {
        setDesktop(false);
        window.localStorage.clear();
        document.documentElement.style.zoom = "";
    });

    it("throws when used outside ZoomProvider", () => {
        const spy = vi.spyOn(console, "error").mockImplementation(() => {});
        expect(() => renderHook(() => useZoomContext())).toThrow(/must be used within a ZoomProvider/);
        spy.mockRestore();
    });

    it("returns context value when used inside ZoomProvider", () => {
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        expect(result.current.zoom).toBe(ZOOM_DEFAULT);
        expect(typeof result.current.setZoom).toBe("function");
        expect(typeof result.current.zoomIn).toBe("function");
        expect(typeof result.current.zoomOut).toBe("function");
        expect(typeof result.current.reset).toBe("function");
    });
});

describe("ZoomProvider — browser mode (no Wails runtime)", () => {
    beforeEach(() => {
        setDesktop(false);
        window.localStorage.clear();
        document.documentElement.style.zoom = "";
    });

    it("reports isDesktop=false", () => {
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        expect(result.current.isDesktop).toBe(false);
    });

    it("does not write to localStorage", () => {
        const setItem = vi.spyOn(window.localStorage, "setItem");
        renderHook(() => useZoomContext(), { wrapper });
        expect(setItem).not.toHaveBeenCalled();
        setItem.mockRestore();
    });

    it("does not touch documentElement.style.zoom", () => {
        renderHook(() => useZoomContext(), { wrapper });
        expect(document.documentElement.style.zoom).toBe("");
    });

    it("ignores Cmd/Ctrl +/-/0 keys (no zoom change, no preventDefault)", () => {
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        const event = pressKey("=", { meta: true });
        expect(event.defaultPrevented).toBe(false);
        expect(result.current.zoom).toBe(ZOOM_DEFAULT);
    });
});

describe("ZoomProvider — desktop mode (Wails runtime present)", () => {
    beforeEach(() => {
        setDesktop(true);
        window.localStorage.clear();
        document.documentElement.style.zoom = "";
    });

    afterEach(() => {
        setDesktop(false);
        window.localStorage.clear();
        document.documentElement.style.zoom = "";
    });

    it("reports isDesktop=true and defaults to 1.0", () => {
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        expect(result.current.isDesktop).toBe(true);
        expect(result.current.zoom).toBe(ZOOM_DEFAULT);
    });

    it("applies the zoom level to documentElement on mount", () => {
        renderHook(() => useZoomContext(), { wrapper });
        expect(document.documentElement.style.zoom).toBe(String(ZOOM_DEFAULT));
    });

    it("persists changes to localStorage", () => {
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        act(() => result.current.zoomIn());
        expect(window.localStorage.getItem(STORAGE_KEY)).toBe(String(ZOOM_DEFAULT + ZOOM_STEP));
    });

    it("reads initial zoom from localStorage", () => {
        window.localStorage.setItem(STORAGE_KEY, "1.5");
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        expect(result.current.zoom).toBe(1.5);
        expect(document.documentElement.style.zoom).toBe("1.5");
    });

    it("falls back to default for non-numeric localStorage value", () => {
        window.localStorage.setItem(STORAGE_KEY, "not-a-number");
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        expect(result.current.zoom).toBe(ZOOM_DEFAULT);
    });

    it("clamps localStorage values above ZOOM_MAX", () => {
        window.localStorage.setItem(STORAGE_KEY, "5.0");
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        expect(result.current.zoom).toBe(ZOOM_MAX);
    });

    it("clamps localStorage values below ZOOM_MIN", () => {
        window.localStorage.setItem(STORAGE_KEY, "0.1");
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        expect(result.current.zoom).toBe(ZOOM_MIN);
    });

    it("zoomIn increments by ZOOM_STEP", () => {
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        act(() => result.current.zoomIn());
        expect(result.current.zoom).toBeCloseTo(ZOOM_DEFAULT + ZOOM_STEP);
    });

    it("zoomOut decrements by ZOOM_STEP", () => {
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        act(() => result.current.zoomOut());
        expect(result.current.zoom).toBeCloseTo(ZOOM_DEFAULT - ZOOM_STEP);
    });

    it("zoomIn clamps at ZOOM_MAX", () => {
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        act(() => result.current.setZoom(ZOOM_MAX));
        act(() => result.current.zoomIn());
        expect(result.current.zoom).toBe(ZOOM_MAX);
    });

    it("zoomOut clamps at ZOOM_MIN", () => {
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        act(() => result.current.setZoom(ZOOM_MIN));
        act(() => result.current.zoomOut());
        expect(result.current.zoom).toBe(ZOOM_MIN);
    });

    it("reset returns to ZOOM_DEFAULT", () => {
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        act(() => result.current.setZoom(1.5));
        act(() => result.current.reset());
        expect(result.current.zoom).toBe(ZOOM_DEFAULT);
    });

    it("setZoom clamps out-of-range values", () => {
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        act(() => result.current.setZoom(10));
        expect(result.current.zoom).toBe(ZOOM_MAX);
        act(() => result.current.setZoom(-1));
        expect(result.current.zoom).toBe(ZOOM_MIN);
    });

    it.each([
        ["=", "zoomIn"],
        ["+", "zoomIn"],
    ] as const)("Cmd+%s triggers %s", (key, action) => {
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        const event = pressKey(key, { meta: true });
        expect(event.defaultPrevented).toBe(true);
        if (action === "zoomIn") {
            expect(result.current.zoom).toBeCloseTo(ZOOM_DEFAULT + ZOOM_STEP);
        }
    });

    it.each([["-"], ["_"]] as const)("Cmd+%s triggers zoomOut", key => {
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        const event = pressKey(key, { meta: true });
        expect(event.defaultPrevented).toBe(true);
        expect(result.current.zoom).toBeCloseTo(ZOOM_DEFAULT - ZOOM_STEP);
    });

    it("Cmd+0 resets zoom", () => {
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        act(() => result.current.setZoom(1.5));
        const event = pressKey("0", { meta: true });
        expect(event.defaultPrevented).toBe(true);
        expect(result.current.zoom).toBe(ZOOM_DEFAULT);
    });

    it("Ctrl+= also triggers zoomIn (non-Mac shortcut)", () => {
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        const event = pressKey("=", { ctrl: true });
        expect(event.defaultPrevented).toBe(true);
        expect(result.current.zoom).toBeCloseTo(ZOOM_DEFAULT + ZOOM_STEP);
    });

    it("does not act on keys without Cmd or Ctrl modifier", () => {
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        const event = pressKey("=", {});
        expect(event.defaultPrevented).toBe(false);
        expect(result.current.zoom).toBe(ZOOM_DEFAULT);
    });

    it("ignores unrelated modifier keys (e.g. Cmd+A)", () => {
        const { result } = renderHook(() => useZoomContext(), { wrapper });
        const event = pressKey("a", { meta: true });
        expect(event.defaultPrevented).toBe(false);
        expect(result.current.zoom).toBe(ZOOM_DEFAULT);
    });

    it("removes keydown listener on unmount", () => {
        const { result, unmount } = renderHook(() => useZoomContext(), { wrapper });
        unmount();
        const event = pressKey("=", { meta: true });
        expect(event.defaultPrevented).toBe(false);
        expect(result.current.zoom).toBe(ZOOM_DEFAULT);
    });
});
