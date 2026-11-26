// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { deferPanelClose, useRadixPointerEventsFix } from "./useRadixPointerEventsFix";

async function flushFrames() {
    for (let i = 0; i < 5; i++) {
        await Promise.resolve();
        await vi.runAllTimersAsync();
    }
}

describe("useRadixPointerEventsFix", () => {
    beforeEach(() => {
        vi.useFakeTimers();
        document.body.style.removeProperty("pointer-events");
        document.body.replaceChildren();
    });

    afterEach(() => {
        vi.useRealTimers();
        document.body.style.removeProperty("pointer-events");
        document.body.replaceChildren();
    });

    test("clears stuck `pointer-events: none` on body when no modal dialog is open", async () => {
        renderHook(() => useRadixPointerEventsFix());

        document.body.style.pointerEvents = "none";

        await flushFrames();

        expect(document.body.style.pointerEvents).toBe("");
    });

    test("does NOT clear when a modal dialog is genuinely still open", async () => {
        renderHook(() => useRadixPointerEventsFix());

        const dialog = document.createElement("div");
        dialog.setAttribute("role", "dialog");
        dialog.setAttribute("data-state", "open");
        document.body.appendChild(dialog);

        document.body.style.pointerEvents = "none";

        await flushFrames();

        expect(document.body.style.pointerEvents).toBe("none");
    });

    test("does NOT clear when an alertdialog is genuinely still open", async () => {
        renderHook(() => useRadixPointerEventsFix());

        const dialog = document.createElement("div");
        dialog.setAttribute("role", "alertdialog");
        dialog.setAttribute("data-state", "open");
        document.body.appendChild(dialog);

        document.body.style.pointerEvents = "none";

        await flushFrames();

        expect(document.body.style.pointerEvents).toBe("none");
    });

    test("regression: open accordion/tab/popover (role-less data-state=open) does NOT fool the hook", async () => {
        renderHook(() => useRadixPointerEventsFix());

        const accordion = document.createElement("div");
        accordion.setAttribute("data-state", "open");
        document.body.appendChild(accordion);

        document.body.style.pointerEvents = "none";

        await flushFrames();

        expect(document.body.style.pointerEvents).toBe("");
    });

    test("re-runs the check after a later mutation re-introduces the stuck lock", async () => {
        renderHook(() => useRadixPointerEventsFix());

        document.body.style.pointerEvents = "none";
        await flushFrames();
        expect(document.body.style.pointerEvents).toBe("");

        document.body.style.pointerEvents = "none";
        await flushFrames();
        expect(document.body.style.pointerEvents).toBe("");
    });

    test("cleans up the MutationObserver on unmount (no further clearing fires)", async () => {
        const { unmount } = renderHook(() => useRadixPointerEventsFix());

        unmount();

        document.body.style.pointerEvents = "none";
        await flushFrames();

        expect(document.body.style.pointerEvents).toBe("none");
    });
});

describe("deferPanelClose", () => {
    afterEach(() => {
        vi.restoreAllMocks();
    });

    test("schedules the close callback via requestAnimationFrame, not synchronously", () => {
        const rafSpy = vi.spyOn(globalThis, "requestAnimationFrame").mockImplementation(() => 1);
        const close = vi.fn();

        deferPanelClose(close);

        expect(close).not.toHaveBeenCalled();
        expect(rafSpy).toHaveBeenCalledTimes(1);
    });

    test("invokes the close callback when the frame runs", () => {
        const close = vi.fn();
        const rafCallbacks: FrameRequestCallback[] = [];
        vi.spyOn(globalThis, "requestAnimationFrame").mockImplementation(cb => {
            rafCallbacks.push(cb);
            return rafCallbacks.length;
        });

        deferPanelClose(close);
        rafCallbacks.forEach(cb => cb(0));

        expect(close).toHaveBeenCalledTimes(1);
    });
});
