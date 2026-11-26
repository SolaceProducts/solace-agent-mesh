// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { renderHook } from "@testing-library/react";
import { describe, expect, test } from "vitest";

import type { ConfigContextValue } from "@/providers/contexts/ConfigContext";
import { ConfigContext } from "@/providers/contexts/ConfigContext";

import { useIsSchedulerEnabled } from "./useIsSchedulerEnabled";

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

function createWrapper(configValue: ConfigContextValue, flags: Record<string, boolean> = {}) {
    return function Wrapper({ children }: { children: React.ReactNode }) {
        return (
            <OpenFeatureTestProvider flagValueMap={flags}>
                <ConfigContext.Provider value={configValue}>{children}</ConfigContext.Provider>
            </OpenFeatureTestProvider>
        );
    };
}

describe("useIsSchedulerEnabled", () => {
    test("returns false when scheduler flag is unset even if schedulerEnabled is true", () => {
        const config = makeConfigValue({ schedulerEnabled: true });
        const { result } = renderHook(() => useIsSchedulerEnabled(), {
            wrapper: createWrapper(config),
        });
        expect(result.current).toBe(false);
    });

    test("returns false when flag is on but the gateway is not serving scheduler routes", () => {
        const config = makeConfigValue({ schedulerEnabled: false, persistenceEnabled: true });
        const { result } = renderHook(() => useIsSchedulerEnabled(), {
            wrapper: createWrapper(config, { scheduler: true }),
        });
        expect(result.current).toBe(false);
    });

    test("returns true only when both the flag and schedulerEnabled are true", () => {
        const config = makeConfigValue({ schedulerEnabled: true });
        const { result } = renderHook(() => useIsSchedulerEnabled(), {
            wrapper: createWrapper(config, { scheduler: true }),
        });
        expect(result.current).toBe(true);
    });
});
