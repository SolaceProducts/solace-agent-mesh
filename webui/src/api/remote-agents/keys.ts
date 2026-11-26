// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const remoteAgentKeys = {
    all: ["remoteAgents"] as const,
    lists: () => [...remoteAgentKeys.all, "list"] as const,
    list: () => [...remoteAgentKeys.lists()] as const,
    details: () => [...remoteAgentKeys.all, "detail"] as const,
    detail: (id: string) => [...remoteAgentKeys.details(), id] as const,
};
