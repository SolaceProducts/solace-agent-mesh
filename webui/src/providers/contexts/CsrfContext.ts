// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createContext, useContext } from "react";

export interface CsrfContextValue {
    fetchCsrfToken: () => Promise<string | null>;
    clearCsrfToken: () => void;
}

export const CsrfContext = createContext<CsrfContextValue | null>(null);

export function useCsrfContext(): CsrfContextValue {
    const context = useContext(CsrfContext);
    if (!context) {
        throw new Error("useCsrf must be used within a CsrfProvider");
    }
    return context;
}
