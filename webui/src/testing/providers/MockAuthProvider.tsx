// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import React from "react";

import { AuthContext, type AuthContextValue } from "@/providers/contexts/AuthContext";

const defaultMockAuthContext: AuthContextValue = {
    isAuthenticated: true,
    useAuthorization: false,
    userInfo: null,

    login: () => Promise.resolve(),
    logout: () => Promise.resolve(),
};

interface MockAuthProviderProps {
    children: React.ReactNode;
    mockValues?: Partial<AuthContextValue>;
}

export const MockAuthProvider: React.FC<MockAuthProviderProps> = ({ children, mockValues = {} }) => {
    const contextValue: AuthContextValue = {
        ...defaultMockAuthContext,
        ...mockValues,
    };

    return <AuthContext.Provider value={contextValue}>{children}</AuthContext.Provider>;
};
