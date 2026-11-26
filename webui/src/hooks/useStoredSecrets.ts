// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { createContext, useContext, useMemo } from "react";

export const StoredSecretsContext = createContext<Set<string>>(new Set());

export const useStoredSecrets = () => useContext(StoredSecretsContext);

export const useTrackStoredSecrets = (initialValues?: Record<string, any>): Set<string> => {
    return useMemo(() => {
        const fields = new Set<string>();
        if (initialValues) {
            Object.entries(initialValues).forEach(([key, value]) => {
                if (value === "<REDACTED>") {
                    fields.add(`values.${key}`);
                }
            });
        }
        return fields;
    }, [initialValues]);
};
