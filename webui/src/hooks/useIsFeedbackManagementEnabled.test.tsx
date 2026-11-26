// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { renderHook } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { useIsFeedbackManagementEnabled } from "./useIsFeedbackManagementEnabled";

function createWrapper(flags: Record<string, boolean> = {}) {
    return function Wrapper({ children }: { children: React.ReactNode }) {
        return <OpenFeatureTestProvider flagValueMap={flags}>{children}</OpenFeatureTestProvider>;
    };
}

describe("useIsFeedbackManagementEnabled", () => {
    test("returns false when the flag is off", () => {
        const { result } = renderHook(() => useIsFeedbackManagementEnabled(), {
            wrapper: createWrapper({ feedback_management: false }),
        });
        expect(result.current).toBe(false);
    });

    test("returns true when the flag is on", () => {
        const { result } = renderHook(() => useIsFeedbackManagementEnabled(), {
            wrapper: createWrapper({ feedback_management: true }),
        });
        expect(result.current).toBe(true);
    });

    test("is off when nothing sets the flag", () => {
        const { result } = renderHook(() => useIsFeedbackManagementEnabled(), {
            wrapper: createWrapper({}),
        });
        expect(result.current).toBe(false);
    });

    test("stays off for a near-miss key", () => {
        const { result } = renderHook(() => useIsFeedbackManagementEnabled(), {
            wrapper: createWrapper({ feedbackManagement: true }),
        });
        expect(result.current).toBe(false);
    });
});
