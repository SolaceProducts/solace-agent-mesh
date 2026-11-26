// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const configKeys = {
    all: ["config"] as const,
    detail: () => [...configKeys.all, "detail"] as const,
};
