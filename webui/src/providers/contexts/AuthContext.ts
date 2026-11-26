// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createContext, useContext } from "react";

export interface AuthContextValue {
    isAuthenticated: boolean;
    useAuthorization: boolean;
    login: () => Promise<void>;
    logout: () => void;
    userInfo: Record<string, unknown> | null;
}

export const AuthContext = createContext<AuthContextValue | null>(null);

export const useAuthContext = (): AuthContextValue => {
    const context = useContext(AuthContext);
    if (!context) {
        throw new Error("useAuth must be used within an AuthProvider");
    }
    return context;
};
