// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, test } from "vitest";

import { disableWailsRuntime, enableWailsRuntime } from "@/testing/helpers";

import { useIsUserBudgetsSupported } from "./useIsUserBudgetsSupported";

describe("useIsUserBudgetsSupported", () => {
    afterEach(() => disableWailsRuntime());

    test("returns true outside the desktop app", () => {
        const { result } = renderHook(() => useIsUserBudgetsSupported());
        expect(result.current).toBe(true);
    });

    test("returns false in the desktop app", () => {
        enableWailsRuntime();
        const { result } = renderHook(() => useIsUserBudgetsSupported());
        expect(result.current).toBe(false);
    });
});
