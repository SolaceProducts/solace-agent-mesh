// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";
import { OpenFeatureTestProvider } from "@openfeature/react-sdk";
import { renderHook } from "@testing-library/react";
import { afterEach, describe, expect, test } from "vitest";

import type { ConfigContextValue } from "@/providers/contexts/ConfigContext";
import { ConfigContext } from "@/providers/contexts/ConfigContext";

import { useIsChatSharingEnabled } from "./useIsChatSharingEnabled";

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

describe("useIsChatSharingEnabled", () => {
    afterEach(() => {
        delete (globalThis as unknown as { runtime?: unknown }).runtime;
    });

    test("returns false when the flag is off, even with identity wired and persistence on", () => {
        const config = makeConfigValue({ identityServiceType: "okta", persistenceEnabled: true });
        const { result } = renderHook(() => useIsChatSharingEnabled(), {
            wrapper: createWrapper(config),
        });

        expect(result.current).toBe(false);
    });

    test("returns false when the flag is on but identity is not wired", () => {
        const config = makeConfigValue({ identityServiceType: null, persistenceEnabled: true });
        const { result } = renderHook(() => useIsChatSharingEnabled(), {
            wrapper: createWrapper(config, { chat_sharing: true }),
        });

        expect(result.current).toBe(false);
    });

    test("returns false when the flag is on and identity wired but no persistence", () => {
        const config = makeConfigValue({ identityServiceType: "okta", persistenceEnabled: false });
        const { result } = renderHook(() => useIsChatSharingEnabled(), {
            wrapper: createWrapper(config, { chat_sharing: true }),
        });

        expect(result.current).toBe(false);
    });

    test("returns true when the flag is on, identity wired, and persistence on", () => {
        const config = makeConfigValue({ identityServiceType: "okta", persistenceEnabled: true });
        const { result } = renderHook(() => useIsChatSharingEnabled(), {
            wrapper: createWrapper(config, { chat_sharing: true }),
        });

        expect(result.current).toBe(true);
    });

    test("returns false in desktop mode even when flag is on", () => {
        const config = makeConfigValue({ identityServiceType: "okta", persistenceEnabled: true });
        (globalThis as unknown as { runtime?: unknown }).runtime = { BrowserOpenURL: () => {} };
        const { result } = renderHook(() => useIsChatSharingEnabled(), {
            wrapper: createWrapper(config, { chat_sharing: true }),
        });
        expect(result.current).toBe(false);
    });
});
