// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import type { ChatPanelMode } from "../useBuilderPageState";

import { SPLIT_MIN_WIDTH } from "./builderSplitLayout";
import { useBuilderSplitLayout } from "./useBuilderSplitLayout";

let observerCallback: ResizeObserverCallback | null = null;
let initialWidth = 0;
const disconnect = vi.fn();
const observe = vi.fn();

class ResizeObserverStub {
    constructor(callback: ResizeObserverCallback) {
        observerCallback = callback;
    }
    observe = observe;
    unobserve() {}
    disconnect = disconnect;
}

function resizeRowTo(width: number) {
    act(() => {
        observerCallback?.([{ contentRect: { width } } as ResizeObserverEntry], {} as ResizeObserver);
    });
}

interface HarnessProps {
    sessionId?: string;
    chatPanelMode?: ChatPanelMode;
    isMobile?: boolean;
    isSessionTransitioning?: boolean;
    hasCanvasContent?: boolean;
}

let latest: ReturnType<typeof useBuilderSplitLayout>;

function Harness({ sessionId = "S1", chatPanelMode = "split", isMobile = false, isSessionTransitioning = false, hasCanvasContent = true }: Readonly<HarnessProps>) {
    latest = useBuilderSplitLayout({ sessionId, chatPanelMode, isMobile, isSessionTransitioning, hasCanvasContent });
    return <div ref={latest.rowRef} />;
}

describe("useBuilderSplitLayout", () => {
    beforeEach(() => {
        observerCallback = null;
        initialWidth = 0;
        disconnect.mockClear();
        observe.mockClear();
        vi.stubGlobal("ResizeObserver", ResizeObserverStub);
        vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(() => ({ width: initialWidth }) as DOMRect);
    });

    afterEach(() => {
        vi.unstubAllGlobals();
        vi.restoreAllMocks();
    });

    test("keeps the split and offers no toggle while the row fits both panes", () => {
        initialWidth = SPLIT_MIN_WIDTH;
        render(<Harness />);
        expect(latest.effectiveMode).toBe("split");
        expect(latest.canvasViewToggle).toBeNull();
    });

    test("shows the chat alone with a toggle once the row is too narrow", () => {
        initialWidth = SPLIT_MIN_WIDTH - 1;
        render(<Harness />);
        expect(latest.effectiveMode).toBe("fullscreen");
        expect(latest.canvasViewToggle).toEqual({ showingCanvas: false, onToggle: expect.any(Function) });
    });

    test("reacts to the row being resized", () => {
        initialWidth = 1200;
        render(<Harness />);
        expect(latest.effectiveMode).toBe("split");
        resizeRowTo(SPLIT_MIN_WIDTH - 100);
        expect(latest.effectiveMode).toBe("fullscreen");
        resizeRowTo(1200);
        expect(latest.effectiveMode).toBe("split");
    });

    test("observes the row element it measures", () => {
        initialWidth = 1200;
        const { container } = render(<Harness />);
        expect(observe).toHaveBeenCalledTimes(1);
        expect(observe).toHaveBeenCalledWith(container.firstElementChild);
    });

    test("stops observing when unmounted", () => {
        initialWidth = 1200;
        const { unmount } = render(<Harness />);
        unmount();
        expect(disconnect).toHaveBeenCalledTimes(1);
    });

    test("the toggle swaps between the chat alone and the canvas alone", () => {
        initialWidth = 500;
        render(<Harness />);
        act(() => latest.canvasViewToggle?.onToggle());
        expect(latest.effectiveMode).toBe("minimized");
        expect(latest.canvasViewToggle?.showingCanvas).toBe(true);
        act(() => latest.canvasViewToggle?.onToggle());
        expect(latest.effectiveMode).toBe("fullscreen");
    });

    test("an open canvas view is dropped when the session changes", () => {
        initialWidth = 500;
        const { rerender } = render(<Harness sessionId="S1" />);
        act(() => latest.canvasViewToggle?.onToggle());
        expect(latest.effectiveMode).toBe("minimized");
        rerender(<Harness sessionId="S2" />);
        expect(latest.effectiveMode).toBe("fullscreen");
    });

    test("an open canvas view falls back to the chat when the canvas content disappears, and stays there when it returns", () => {
        initialWidth = 500;
        const { rerender } = render(<Harness />);
        act(() => latest.canvasViewToggle?.onToggle());
        rerender(<Harness hasCanvasContent={false} />);
        expect(latest.effectiveMode).toBe("fullscreen");
        expect(latest.canvasViewToggle).toBeNull();
        rerender(<Harness hasCanvasContent />);
        expect(latest.effectiveMode).toBe("fullscreen");
        expect(latest.canvasViewToggle?.showingCanvas).toBe(false);
    });

    test("an open canvas view does not come back on its own after the window is widened and narrowed again", () => {
        initialWidth = 500;
        render(<Harness />);
        act(() => latest.canvasViewToggle?.onToggle());
        resizeRowTo(1200);
        expect(latest.effectiveMode).toBe("split");
        resizeRowTo(500);
        expect(latest.effectiveMode).toBe("fullscreen");
    });

    test("a session transition shows the chat alone with no toggle", () => {
        initialWidth = 500;
        render(<Harness isSessionTransitioning />);
        expect(latest.effectiveMode).toBe("fullscreen");
        expect(latest.canvasViewToggle).toBeNull();
    });

    test("the floating chat window is left alone with no toggle", () => {
        initialWidth = 500;
        render(<Harness chatPanelMode="floating" />);
        expect(latest.effectiveMode).toBe("floating");
        expect(latest.canvasViewToggle).toBeNull();
    });

    test("mobile keeps the chat fullscreen with no toggle, as before", () => {
        initialWidth = 400;
        render(<Harness isMobile />);
        expect(latest.effectiveMode).toBe("fullscreen");
        expect(latest.canvasViewToggle).toBeNull();
    });

    test("a missing ResizeObserver still measures once and does not throw", () => {
        vi.stubGlobal("ResizeObserver", undefined);
        initialWidth = 500;
        render(<Harness />);
        expect(latest.effectiveMode).toBe("fullscreen");
    });
});
