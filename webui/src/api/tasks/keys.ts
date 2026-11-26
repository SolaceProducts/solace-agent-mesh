// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { TaskListFilters } from "./types";

export const taskKeys = {
    all: ["tasks"] as const,
    lists: () => [...taskKeys.all, "list"] as const,
    recent: (pageSize: number, filters: TaskListFilters = {}) => [...taskKeys.lists(), "recent", pageSize, filters.userId ?? "", filters.sessionIds?.join(",") ?? "", filters.search ?? ""] as const,
};
