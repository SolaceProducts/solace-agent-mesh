// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { renderHook } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import type { ConfigContextValue } from "@/providers/contexts/ConfigContext";
import { ConfigContext } from "@/providers/contexts/ConfigContext";

import { useIsPeopleSearchAvailable } from "./useIsPeopleSearchAvailable";

function makeConfigValue(overrides: Partial<ConfigContextValue> = {}): ConfigContextValue {
    return {
        webuiServerUrl: "",
        platformServerUrl: "",
        configAuthLoginUrl: "",
        configUseAuthorization: false,
        configWelcomeMessage: "",
        configDisclaimerText: "",
        configRedirectUrl: "",
        configCollectFeedback: false,
        configAppName: "",
        configLogoUrl: "",
        configSmallLogoUrl: "",
        frontend_use_authorization: false,
        platformConfigured: false,
        identityServiceType: null,
        claimKey: "",
        refetch: async () => {},
        ...overrides,
    };
}

function renderWithConfig(overrides: Partial<ConfigContextValue>) {
    const value = makeConfigValue(overrides);
    return renderHook(() => useIsPeopleSearchAvailable(), {
        wrapper: ({ children }: { children: React.ReactNode }) => <ConfigContext.Provider value={value}>{children}</ConfigContext.Provider>,
    });
}

describe("useIsPeopleSearchAvailable", () => {
    test("returns true when the gateway reports people search available", () => {
        expect(renderWithConfig({ peopleSearchAvailable: true }).result.current).toBe(true);
    });

    test("returns false when the gateway reports people search unavailable", () => {
        expect(renderWithConfig({ peopleSearchAvailable: false }).result.current).toBe(false);
    });

    test("returns false when the gateway does not report the field", () => {
        expect(renderWithConfig({}).result.current).toBe(false);
    });
});
