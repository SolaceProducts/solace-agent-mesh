// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useState } from "react";

type SortDir = "asc" | "desc";

export function useSortState<K extends string>(defaultKey: K, defaultDir: SortDir = "asc") {
    const [sortKey, setSortKey] = useState<K>(defaultKey);
    const [sortDir, setSortDir] = useState<SortDir>(defaultDir);

    const handleSort = (column: string) => {
        if (sortKey === column) {
            setSortDir(d => (d === "asc" ? "desc" : "asc"));
        } else {
            setSortKey(column as K);
            setSortDir("asc");
        }
    };

    return { sortKey, sortDir, handleSort };
}

export function useSortableTable<T extends object>(data: T[], columns: (keyof T & string)[]) {
    const { sortKey, sortDir, handleSort } = useSortState(columns[0]);

    const toStr = (val: unknown): string => (typeof val === "string" || typeof val === "number" || typeof val === "boolean" ? String(val) : "").toLowerCase();

    const sortedData = [...data].sort((a, b) => {
        const aVal = toStr((a as Record<string, unknown>)[sortKey]);
        const bVal = toStr((b as Record<string, unknown>)[sortKey]);
        const cmp = aVal.localeCompare(bVal);
        return sortDir === "asc" ? cmp : -cmp;
    });

    return { sortedData, sortKey, sortDir, handleSort };
}
