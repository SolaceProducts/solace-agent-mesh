// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const toolsetKeys = {
    all: ["toolsets"] as const,
    lists: () => [...toolsetKeys.all, "list"] as const,
    builtin: () => [...toolsetKeys.lists(), "builtin"] as const,
    custom: () => [...toolsetKeys.lists(), "custom"] as const,
    buildTarget: () => [...toolsetKeys.all, "buildTarget"] as const,
};
