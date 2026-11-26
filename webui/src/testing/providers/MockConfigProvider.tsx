// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React, { useMemo } from "react";

import { api } from "@/api/client";

import { ConfigContext, type ConfigContextValue } from "@/providers/contexts/ConfigContext";

const UNRESOLVABLE_ORIGIN = "http://test.invalid:9999";

const defaultMockConfigContext: ConfigContextValue = {
    webuiServerUrl: UNRESOLVABLE_ORIGIN,
    platformServerUrl: UNRESOLVABLE_ORIGIN,
    configAuthLoginUrl: `${UNRESOLVABLE_ORIGIN}/auth/login`,
    configUseAuthorization: false,
    configWelcomeMessage: "Welcome to the mock Solace Agent Mesh!",
    configDisclaimerText: "",
    configRedirectUrl: "http://localhost:3000",
    configCollectFeedback: false,
    configAppName: "Mock App",
    frontend_use_authorization: false,
    configLogoUrl: null,
    configSmallLogoUrl: null,
    platformConfigured: true,
    identityServiceType: null,
    claimKey: "groups",
    refetch: async () => {},
};

interface MockConfigProviderProps {
    children: React.ReactNode;
    mockValues?: Partial<ConfigContextValue>;
}

export const MockConfigProvider: React.FC<MockConfigProviderProps> = ({ children, mockValues = {} }) => {
    const contextValue = useMemo(
        () => ({
            ...defaultMockConfigContext,
            ...mockValues,
        }),
        [mockValues]
    );

    api.configure(contextValue.webuiServerUrl, contextValue.platformServerUrl);

    return <ConfigContext.Provider value={contextValue}>{children}</ConfigContext.Provider>;
};
