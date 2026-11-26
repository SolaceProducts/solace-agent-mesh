// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { api } from "@/api/client";

import type { TaskFE } from "@/types";

import { type PersistedTaskRow, recentTasksPath, rowToTaskFE, type TaskListFilters } from "./types";

export const getRecentTasks = async (pageSize: number, filters: TaskListFilters = {}): Promise<TaskFE[]> => {
    const resp = await api.webui.get<{ data?: PersistedTaskRow[] }>(recentTasksPath(pageSize, filters));
    const rows = Array.isArray(resp?.data) ? resp.data : [];
    return rows.filter(row => Boolean(row.id)).map(rowToTaskFE);
};
