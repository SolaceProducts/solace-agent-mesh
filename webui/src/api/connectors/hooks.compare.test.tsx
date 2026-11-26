// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode } from "react";
import { renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { UserContextValue } from "@/providers/contexts/UserContext";

describe("useCanCompareConnectorSecrets", () => {
    let useCanCompareConnectorSecrets: typeof import("./hooks").useCanCompareConnectorSecrets;
    let CAPABILITIES: typeof import("@/providers/contexts/UserContext").CAPABILITIES;
    let UserContext: typeof import("@/providers/contexts/UserContext").UserContext;

    beforeEach(async () => {
        vi.resetModules();
        ({ CAPABILITIES, UserContext } = await import("@/providers/contexts/UserContext"));
        ({ useCanCompareConnectorSecrets } = await import("./hooks"));
    });

    const withCapabilities = (granted: string[]): UserContextValue => ({
        userHasCapability: cap => granted.includes(cap),
        CAPABILITIES,
        capabilitiesLoaded: true,
        scopes: [],
        hasScope: () => false,
        isSuperAdmin: false,
    });

    const canCompare = (value: UserContextValue | null) => {
        const wrapper = ({ children }: { children: ReactNode }) => (value ? <UserContext.Provider value={value}>{children}</UserContext.Provider> : children);
        return renderHook(() => useCanCompareConnectorSecrets(), { wrapper }).result.current;
    };

    it("needs both read and update, because secretsDiff is charged to the update scope", () => {
        expect(canCompare(withCapabilities([CAPABILITIES.readConnectors, CAPABILITIES.updateConnectors]))).toBe(true);
        expect(canCompare(withCapabilities([CAPABILITIES.readConnectors]))).toBe(false);
        expect(canCompare(withCapabilities([CAPABILITIES.updateConnectors]))).toBe(false);
        expect(canCompare(withCapabilities([]))).toBe(false);
    });

    it("is false when no UserProvider is mounted (fail-closed)", () => {
        expect(canCompare(null)).toBe(false);
    });
});
