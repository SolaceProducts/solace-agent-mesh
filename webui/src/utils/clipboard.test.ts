// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { copyDeferredToClipboard, copyToClipboard } from "@/utils/clipboard";

describe("copyToClipboard", () => {
    beforeEach(() => {
        vi.restoreAllMocks();
    });

    afterEach(() => {
        vi.unstubAllGlobals();
    });

    test("returns true on successful copy", async () => {
        const writeText = vi.fn().mockResolvedValue(undefined);
        vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
        const result = await copyToClipboard("hello");
        expect(result).toBe(true);
        expect(writeText).toHaveBeenCalledWith("hello");
    });

    test("returns false when clipboard API fails", async () => {
        const writeText = vi.fn().mockRejectedValue(new Error("denied"));
        vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
        const result = await copyToClipboard("hello");
        expect(result).toBe(false);
    });
});

describe("copyDeferredToClipboard", () => {
    beforeEach(() => {
        vi.restoreAllMocks();
    });

    afterEach(() => {
        vi.unstubAllGlobals();
    });

    test("returns true when deferred copy succeeds", async () => {
        const mockWrite = vi.fn().mockResolvedValue(undefined);
        vi.stubGlobal("navigator", { ...navigator, clipboard: { write: mockWrite } });
        vi.stubGlobal(
            "ClipboardItem",
            class {
                items: Record<string, Promise<Blob>>;
                constructor(items: Record<string, Promise<Blob>>) {
                    this.items = items;
                }
            }
        );

        const result = await copyDeferredToClipboard(Promise.resolve("deferred text"));
        expect(result).toBe(true);
        expect(mockWrite).toHaveBeenCalled();
    });

    test("returns false when ClipboardItem constructor throws", async () => {
        vi.stubGlobal("ClipboardItem", undefined);
        const result = await copyDeferredToClipboard(Promise.resolve("text"));
        expect(result).toBe(false);
    });
});
