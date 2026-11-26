// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { productAnalyticsClient } from "@/common/product-analytics/client";

import { useDesktopFileDialogErrors } from "./useDesktopFileDialogErrors";

const addNotification = vi.fn();

vi.mock("@/providers/contexts", () => ({
    useChatContext: () => ({ addNotification }),
}));

function fireError(detail: { op: "open" | "save"; message?: string } | undefined): void {
    globalThis.dispatchEvent(new CustomEvent("desktop:fileDialogError", { detail }));
}

describe("useDesktopFileDialogErrors", () => {
    beforeEach(() => {
        addNotification.mockClear();
    });

    afterEach(() => {
        addNotification.mockClear();
    });

    test('open-op event renders "Could not open the file picker" toast', () => {
        renderHook(() => useDesktopFileDialogErrors());

        fireError({ op: "open", message: "boom" });

        expect(addNotification).toHaveBeenCalledTimes(1);
        expect(addNotification).toHaveBeenCalledWith("Could not open the file picker: boom", "warning");
    });

    test('save-op event renders "Could not save the file" toast', () => {
        renderHook(() => useDesktopFileDialogErrors());

        fireError({ op: "save", message: "disk full" });

        expect(addNotification).toHaveBeenCalledTimes(1);
        expect(addNotification).toHaveBeenCalledWith("Could not save the file: disk full", "warning");
    });

    test("omits the detail suffix when message is empty or missing", () => {
        renderHook(() => useDesktopFileDialogErrors());

        fireError({ op: "open", message: "" });
        fireError({ op: "save" });

        expect(addNotification).toHaveBeenCalledTimes(2);
        expect(addNotification).toHaveBeenNthCalledWith(1, "Could not open the file picker", "warning");
        expect(addNotification).toHaveBeenNthCalledWith(2, "Could not save the file", "warning");
    });

    test("defaults to open-op copy when detail is missing entirely", () => {
        renderHook(() => useDesktopFileDialogErrors());

        fireError(undefined);

        expect(addNotification).toHaveBeenCalledWith("Could not open the file picker", "warning");
    });

    test("reports the failure as error:displayed, bucketed, without the message", () => {
        const trackSpy = vi.spyOn(productAnalyticsClient, "track").mockImplementation(() => {});
        renderHook(() => useDesktopFileDialogErrors());

        fireError({ op: "save", message: "permission denied writing /Users/jane/Acme Holdings/report.pdf" });

        const errorEvents = (trackSpy.mock.calls as unknown[][]).filter(call => call[0] === "error:displayed").map(call => call[1]);
        expect(errorEvents).toEqual([{ errorCategory: "auth_failure", errorSource: "client", page: "/" }]);
        expect(JSON.stringify(errorEvents)).not.toContain("Acme");
        trackSpy.mockRestore();
    });

    test("removes the listener on unmount (no toast after teardown)", () => {
        const { unmount } = renderHook(() => useDesktopFileDialogErrors());

        unmount();
        fireError({ op: "open", message: "should be ignored" });

        expect(addNotification).not.toHaveBeenCalled();
    });
});
