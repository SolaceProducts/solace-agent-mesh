// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createContext, useContext } from "react";

const HighlightFieldsContext = createContext<Set<string> | null>(null);

export const HighlightFieldsProvider = HighlightFieldsContext.Provider;

export function useIsFieldHighlighted(fieldName: string): boolean {
    const highlightSet = useContext(HighlightFieldsContext);
    if (!highlightSet) return false;
    const bare = fieldName.startsWith("values.") ? fieldName.slice(7) : fieldName;
    return highlightSet.has(bare) || highlightSet.has(fieldName);
}
