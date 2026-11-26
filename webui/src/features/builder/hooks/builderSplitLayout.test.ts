// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { canToggleCanvasView, CANVAS_PANE_SIZING, clampSplitPanelWidth, isSplitTooNarrow, resolveChatPanelMode, SPLIT_MIN_WIDTH, splitPanelMaxWidth, splitPanelSizing } from "./builderSplitLayout";

const wide = { isMobile: false, isSessionTransitioning: false, isSplitTooNarrow: false, canvasViewOpen: false };
const narrow = { ...wide, isSplitTooNarrow: true };

describe("isSplitTooNarrow", () => {
    test("the split needs 720px: a 320px chat panel beside a 400px canvas", () => {
        expect(SPLIT_MIN_WIDTH).toBe(720);
        expect(isSplitTooNarrow(719)).toBe(true);
        expect(isSplitTooNarrow(720)).toBe(false);
    });

    test("an unmeasured or zero-sized container is never narrow", () => {
        expect(isSplitTooNarrow(null)).toBe(false);
        expect(isSplitTooNarrow(0)).toBe(false);
    });
});

describe("split pane sizing", () => {
    test("the chat panel can shrink to give the canvas room, but not below 320px or past 60vw", () => {
        const style = splitPanelSizing(480);
        expect(style).toMatchObject({ width: "480px", minWidth: "320px", maxWidth: "60vw" });
        expect(style.flexShrink).toBeGreaterThan(0);
    });

    test("the canvas pane keeps 400px", () => {
        expect(CANVAS_PANE_SIZING.minWidth).toBe("400px");
    });
});

describe("splitPanelMaxWidth", () => {
    test.each([
        { name: "the row leaves the canvas its minimum", rowWidth: 944, viewport: 1200, expected: 544 },
        { name: "60% of the viewport caps a wide row", rowWidth: 1664, viewport: 1920, expected: 1152 },
        { name: "a row too small for both still allows the chat minimum", rowWidth: 600, viewport: 856, expected: 320 },
    ])("$name", ({ rowWidth, viewport, expected }) => {
        expect(splitPanelMaxWidth(rowWidth, viewport)).toBe(expected);
        expect(clampSplitPanelWidth(Number.MAX_SAFE_INTEGER, rowWidth, viewport)).toBe(expected);
    });
});

describe("clampSplitPanelWidth", () => {
    test("leaves a width that fits alone", () => {
        expect(clampSplitPanelWidth(480, 1184, 1440)).toBe(480);
    });

    test("never gives the chat so much that the canvas drops below its minimum", () => {
        expect(clampSplitPanelWidth(720, 944, 1200)).toBe(544);
    });

    test("never exceeds 60% of the viewport", () => {
        expect(clampSplitPanelWidth(2000, 1664, 1920)).toBe(1152);
    });

    test("never goes below the chat minimum, even when the row is too small for both", () => {
        expect(clampSplitPanelWidth(100, 1000, 1280)).toBe(320);
        expect(clampSplitPanelWidth(500, 600, 856)).toBe(320);
    });
});

describe("resolveChatPanelMode", () => {
    test("keeps the requested mode when chat and canvas fit side by side", () => {
        expect(resolveChatPanelMode({ ...wide, chatPanelMode: "split" })).toBe("split");
        expect(resolveChatPanelMode({ ...wide, chatPanelMode: "fullscreen" })).toBe("fullscreen");
        expect(resolveChatPanelMode({ ...wide, chatPanelMode: "minimized" })).toBe("minimized");
    });

    test("shows the chat alone instead of a squeezed split when too narrow", () => {
        expect(resolveChatPanelMode({ ...narrow, chatPanelMode: "split" })).toBe("fullscreen");
    });

    test("shows the canvas alone when too narrow and the canvas view is open", () => {
        expect(resolveChatPanelMode({ ...narrow, chatPanelMode: "split", canvasViewOpen: true })).toBe("minimized");
    });

    test("a requested minimized chat is replaced by the header-controlled single pane when too narrow", () => {
        expect(resolveChatPanelMode({ ...narrow, chatPanelMode: "minimized" })).toBe("fullscreen");
        expect(resolveChatPanelMode({ ...narrow, chatPanelMode: "minimized", canvasViewOpen: true })).toBe("minimized");
    });

    test("ignores the canvas view request when the split fits", () => {
        expect(resolveChatPanelMode({ ...wide, chatPanelMode: "split", canvasViewOpen: true })).toBe("split");
    });

    test("leaves the floating chat window alone", () => {
        expect(resolveChatPanelMode({ ...narrow, chatPanelMode: "floating" })).toBe("floating");
    });

    test("mobile and session transitions stay fullscreen even when the canvas view is open", () => {
        expect(resolveChatPanelMode({ ...narrow, isMobile: true, chatPanelMode: "split", canvasViewOpen: true })).toBe("fullscreen");
        expect(resolveChatPanelMode({ ...wide, isSessionTransitioning: true, chatPanelMode: "split" })).toBe("fullscreen");
    });
});

describe("canToggleCanvasView", () => {
    const base = { chatPanelMode: "split" as const, isMobile: false, isSessionTransitioning: false, isSplitTooNarrow: true, hasCanvasContent: true };

    test("is offered when narrow and there is a canvas to show", () => {
        expect(canToggleCanvasView(base)).toBe(true);
    });

    test.each([
        ["the split fits", { isSplitTooNarrow: false }],
        ["there is no canvas content", { hasCanvasContent: false }],
        ["on mobile, where the canvas is deliberately hidden", { isMobile: true }],
        ["a session is transitioning", { isSessionTransitioning: true }],
        ["the chat is floating, where toggling would change nothing", { chatPanelMode: "floating" as const }],
    ])("is not offered when %s", (_name, override) => {
        expect(canToggleCanvasView({ ...base, ...override })).toBe(false);
    });

    test("whenever the toggle is offered, opening the canvas view changes the rendered mode", () => {
        for (const chatPanelMode of ["fullscreen", "split", "minimized", "floating"] as const) {
            const context = { chatPanelMode, isMobile: false, isSessionTransitioning: false, isSplitTooNarrow: true };
            if (!canToggleCanvasView({ ...context, hasCanvasContent: true })) continue;
            expect(resolveChatPanelMode({ ...context, canvasViewOpen: true })).not.toBe(resolveChatPanelMode({ ...context, canvasViewOpen: false }));
        }
    });
});
