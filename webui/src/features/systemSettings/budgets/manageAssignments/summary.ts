// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UpdateBudgetAssignmentsResponse } from "@/api/budgets";

export function summarizeAssignmentChanges(result: UpdateBudgetAssignmentsResponse): string {
    const parts: string[] = [];
    if (result.added.length > 0) parts.push(`${result.added.length} added`);
    if (result.moved.length > 0) {
        const from = new Set(result.moved.map(move => move.fromBudget.name));
        const source = from.size === 1 ? [...from][0] : `${from.size} budgets`;
        parts.push(`${result.moved.length} moved from ${source}`);
    }
    if (result.removed.length > 0) parts.push(`${result.removed.length} removed`);
    return parts.length > 0 ? parts.join(", ") : "No changes";
}
