// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const activationKeys = {
    all: ["activation"] as const,
    status: () => [...activationKeys.all, "status"] as const,
    modelsConfigured: () => [...activationKeys.all, "modelsConfigured"] as const,
    freeModel: () => [...activationKeys.all, "freeModel"] as const,
    terms: () => [...activationKeys.all, "terms"] as const,
};
