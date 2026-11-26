// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const agentCardKeys = {
    all: ["agentCards"] as const,
    lists: () => [...agentCardKeys.all, "list"] as const,
    list: () => [...agentCardKeys.lists()] as const,
};
