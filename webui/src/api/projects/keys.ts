// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const projectKeys = {
    all: ["projects"] as const,
    lists: () => [...projectKeys.all, "list"] as const,
    list: (filters?: Record<string, unknown>) => [...projectKeys.lists(), { filters }] as const,
    details: () => [...projectKeys.all, "detail"] as const,
    detail: (id: string) => [...projectKeys.details(), id] as const,
    artifacts: (id: string) => [...projectKeys.detail(id), "artifacts"] as const,
    sessions: (id: string) => [...projectKeys.detail(id), "sessions"] as const,
    shares: (id: string) => [...projectKeys.detail(id), "shares"] as const,
};
