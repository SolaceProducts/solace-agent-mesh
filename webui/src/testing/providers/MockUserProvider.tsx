// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { CAPABILITIES, UserContext, type UserContextValue } from "@/providers/contexts/UserContext";

import { SUPERADMIN_SCOPE } from "@/utils/scopes";

const defaultMockUserContext: UserContextValue = {
    userHasCapability: () => true,
    CAPABILITIES,
    capabilitiesLoaded: true,
    scopes: [SUPERADMIN_SCOPE],
    hasScope: () => true,
    username: null,
    isSuperAdmin: true,
};
interface MockUserProviderProps {
    children: React.ReactNode;
    mockValues?: Partial<UserContextValue>;
}

export const MockUserProvider: React.FC<MockUserProviderProps> = ({ children, mockValues = {} }) => {
    const contextValue: UserContextValue = {
        ...defaultMockUserContext,
        ...mockValues,
    };

    return <UserContext.Provider value={contextValue}>{children}</UserContext.Provider>;
};
