// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { renderHook } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import { MockUserProvider } from "@/testing/providers/MockUserProvider";

import { useBudgetsEnabled } from "./useBudgetsEnabled";

function createWrapper(hasCapability: boolean) {
    return function Wrapper({ children }: { children: React.ReactNode }) {
        return <MockUserProvider mockValues={{ userHasCapability: cap => hasCapability && cap === CAPABILITIES.readBudgets }}>{children}</MockUserProvider>;
    };
}

describe("useBudgetsEnabled", () => {
    test("returns false when the capability is missing", () => {
        const { result } = renderHook(() => useBudgetsEnabled(), { wrapper: createWrapper(false) });
        expect(result.current).toBe(false);
    });

    test("returns true when the capability is held", () => {
        const { result } = renderHook(() => useBudgetsEnabled(), { wrapper: createWrapper(true) });
        expect(result.current).toBe(true);
    });
});
