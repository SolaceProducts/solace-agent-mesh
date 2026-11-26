// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const shareKeys = {
    all: ["shares"] as const,
    links: () => [...shareKeys.all, "link"] as const,
    link: (sessionId: string) => [...shareKeys.links(), sessionId] as const,
    lists: () => [...shareKeys.all, "list"] as const,
    list: (filters?: { page?: number; pageSize?: number; search?: string }) => [...shareKeys.lists(), { filters }] as const,
    users: (shareId: string) => [...shareKeys.all, "users", shareId] as const,
    sharedWithMeAll: () => [...shareKeys.all, "shared-with-me"] as const,
    sharedWithMe: (userId: string) => [...shareKeys.all, "shared-with-me", userId] as const,
    view: (shareId: string) => [...shareKeys.all, "view", shareId] as const,
};
