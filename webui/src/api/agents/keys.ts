// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const agentKeys = {
    all: ["agents"] as const,
    lists: () => [...agentKeys.all, "list"] as const,
    list: () => [...agentKeys.lists()] as const,
    details: () => [...agentKeys.all, "detail"] as const,
    detail: (id: string) => [...agentKeys.details(), id] as const,
};
