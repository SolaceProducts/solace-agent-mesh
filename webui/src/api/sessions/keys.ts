// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const sessionKeys = {
    all: ["sessions"] as const,
    lists: () => [...sessionKeys.all, "list"] as const,
    recents: (userId: string) => [...sessionKeys.lists(), "recent", userId] as const,
    recent: (userId: string, maxItems: number, agentId?: string) => [...sessionKeys.recents(userId), maxItems, agentId ?? null] as const,
    infinites: (userId: string) => [...sessionKeys.lists(), "infinite", userId] as const,
    infinite: (userId: string, pageSize: number, source?: string, agentId?: string) => [...sessionKeys.infinites(userId), pageSize, source, agentId ?? null] as const,
    details: () => [...sessionKeys.all, "detail"] as const,
    detail: (id: string) => [...sessionKeys.details(), id] as const,
    chatTasks: (id: string) => [...sessionKeys.detail(id), "chat-tasks"] as const,
    contextUsage: (id: string, agentName?: string) => [...sessionKeys.detail(id), "context-usage", agentName ?? null] as const,
};
