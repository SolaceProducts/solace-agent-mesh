// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, test } from "vitest";

import { MockConfigProvider } from "@/testing/providers/MockConfigProvider";

import { useIsProjectSharingEnabled } from "./useIsProjectSharingEnabled";

function createWrapper(flags: Record<string, boolean> = {}, identityServiceType: string | null = "okta") {
    return function Wrapper({ children }: { children: React.ReactNode }) {
        return (
            <OpenFeatureTestProvider flagValueMap={flags}>
                <MockConfigProvider mockValues={{ identityServiceType }}>{children}</MockConfigProvider>
            </OpenFeatureTestProvider>
        );
    };
}

describe("useIsProjectSharingEnabled", () => {
    afterEach(() => {
        delete (globalThis as unknown as { runtime?: unknown }).runtime;
    });

    test("returns false when flag is off", () => {
        const { result } = renderHook(() => useIsProjectSharingEnabled(), {
            wrapper: createWrapper(),
        });
        expect(result.current).toBe(false);
    });

    test("returns true when flag is on and an identity service is wired", () => {
        const { result } = renderHook(() => useIsProjectSharingEnabled(), {
            wrapper: createWrapper({ project_sharing: true }),
        });
        expect(result.current).toBe(true);
    });

    test("returns false when flag is on but no identity service", () => {
        const { result } = renderHook(() => useIsProjectSharingEnabled(), {
            wrapper: createWrapper({ project_sharing: true }, null),
        });
        expect(result.current).toBe(false);
    });

    test("returns false in desktop mode even when flag is on", () => {
        (globalThis as unknown as { runtime?: unknown }).runtime = { BrowserOpenURL: () => {} };
        const { result } = renderHook(() => useIsProjectSharingEnabled(), {
            wrapper: createWrapper({ project_sharing: true }),
        });
        expect(result.current).toBe(false);
    });
});
