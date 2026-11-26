// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { renderHook } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import { CAPABILITIES } from "@/providers/contexts/UserContext";

import { MockUserProvider } from "@/testing/providers/MockUserProvider";

import { useFeedbackManagementEnabled } from "./useFeedbackManagementEnabled";

function createWrapper(flagEnabled: boolean, hasCapability: boolean) {
    return function Wrapper({ children }: { children: React.ReactNode }) {
        return (
            <OpenFeatureTestProvider flagValueMap={{ feedback_management: flagEnabled }}>
                <MockUserProvider mockValues={{ userHasCapability: cap => hasCapability && cap === CAPABILITIES.readFeedback }}>{children}</MockUserProvider>
            </OpenFeatureTestProvider>
        );
    };
}

describe("useFeedbackManagementEnabled", () => {
    test("returns false when the flag is off, even with the capability", () => {
        const { result } = renderHook(() => useFeedbackManagementEnabled(), {
            wrapper: createWrapper(false, true),
        });
        expect(result.current).toBe(false);
    });

    test("returns false when the flag is on but the capability is missing", () => {
        const { result } = renderHook(() => useFeedbackManagementEnabled(), {
            wrapper: createWrapper(true, false),
        });
        expect(result.current).toBe(false);
    });

    test("returns true when the flag is on and the capability is held", () => {
        const { result } = renderHook(() => useFeedbackManagementEnabled(), {
            wrapper: createWrapper(true, true),
        });
        expect(result.current).toBe(true);
    });
});
