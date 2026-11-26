// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const notificationKeys = {
    all: ["notifications"] as const,
    inboxes: () => [...notificationKeys.all, "inbox"] as const,
    inbox: (filters?: { pageNumber?: number; pageSize?: number; unreadOnly?: boolean; category?: string }) => [...notificationKeys.inboxes(), { filters }] as const,
    preferences: () => [...notificationKeys.all, "preferences"] as const,
    categoryPreferences: () => [...notificationKeys.all, "categoryPreferences"] as const,
    channels: () => [...notificationKeys.all, "channels"] as const,
    matrix: () => [...notificationKeys.all, "matrix"] as const,
};
