// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const featureKeys = {
    all: ["features"] as const,
    list: () => [...featureKeys.all, "list"] as const,
};
