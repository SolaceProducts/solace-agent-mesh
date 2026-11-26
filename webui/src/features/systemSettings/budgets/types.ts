// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export type BudgetScope = "user" | "mesh";

export const BUDGET_SCOPE_LABELS: Record<BudgetScope, string> = { user: "User", mesh: "Agent Mesh" };

export const MESH_BUDGET_NAME = "Agent Mesh";

export interface BudgetRow {
    id: string;
    name: string;
    scope: BudgetScope;
    amount: string | null;
    warnPercentage: number | null;
    isDefault: boolean;
    assignedCount: number;
    hasUnlistedAssignments: boolean;
}

export interface BudgetPermissions {
    canCreate: boolean;
    canEdit: boolean;
    canDelete: boolean;
    canAssign: boolean;
}

export const LIMITED_DEFAULT_SYSTEM_USERS_NOTE = "The default user budget's limit also applies to every service account with no assigned budget.";
