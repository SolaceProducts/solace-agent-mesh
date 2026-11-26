// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, test } from "vitest";

import { useIsDesktop } from "./useIsDesktop";

describe("useIsDesktop", () => {
    afterEach(() => {
        delete (globalThis as unknown as { runtime?: unknown }).runtime;
    });

    test("returns false when Wails runtime is absent", () => {
        const { result } = renderHook(() => useIsDesktop());
        expect(result.current).toBe(false);
    });

    test("returns true when Wails runtime is present", () => {
        (globalThis as unknown as { runtime?: unknown }).runtime = { BrowserOpenURL: () => {} };
        const { result } = renderHook(() => useIsDesktop());
        expect(result.current).toBe(true);
    });
});
