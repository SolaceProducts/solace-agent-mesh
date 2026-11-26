// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Budget } from "@/api/budgets";

import type { BudgetPermissions, BudgetRow } from "@/features/systemSettings/budgets/types";

export const mockBudgetRows: BudgetRow[] = [
    { id: "budget-default", scope: "user", name: "Standard Monthly", amount: "200.00", warnPercentage: 80, isDefault: true, assignedCount: 0, hasUnlistedAssignments: false },
    { id: "budget-engineering", scope: "user", name: "Engineering", amount: "2000.00", warnPercentage: 90, isDefault: false, assignedCount: 0, hasUnlistedAssignments: false },
    { id: "budget-unlimited", scope: "user", name: "Executive Exemption", amount: null, warnPercentage: null, isDefault: false, assignedCount: 3, hasUnlistedAssignments: false },
    { id: "budget-no-threshold", scope: "user", name: "Research Pilot", amount: "500.00", warnPercentage: null, isDefault: false, assignedCount: 0, hasUnlistedAssignments: false },
    { id: "budget-large", scope: "user", name: "Data Platform", amount: "1250000.00", warnPercentage: 75, isDefault: false, assignedCount: 0, hasUnlistedAssignments: false },
    {
        id: "budget-long-name",
        scope: "user",
        name: "Customer Success Operations and Support Tooling Shared Budget for the EMEA Region",
        amount: "750.50",
        warnPercentage: 50,
        isDefault: false,
        assignedCount: 0,
        hasUnlistedAssignments: false,
    },
];

export const mockMeshBudget: BudgetRow = { id: "budget-mesh", scope: "mesh", name: "Agent Mesh", amount: "50000.00", warnPercentage: 90, isDefault: true, assignedCount: 0, hasUnlistedAssignments: false };

export const fullBudgetPermissions: BudgetPermissions = { canCreate: true, canEdit: true, canDelete: true, canAssign: true };
export const noBudgetPermissions: BudgetPermissions = { canCreate: false, canEdit: false, canDelete: false, canAssign: false };

const apiBudget = (partial: Pick<Budget, "id" | "name" | "amount"> & Partial<Budget>): Budget => ({
    type: "budget",
    subjectType: "user",
    warningPercentage: null,
    isDefault: false,
    assignedCount: 0,
    hiddenAssignedCount: 0,
    pendingCount: 0,
    createdBy: "amy@example.com",
    createdTime: "2026-09-01T00:00:00.000Z",
    updatedBy: "amy@example.com",
    updatedTime: "2026-09-01T00:00:00.000Z",
    ...partial,
});

export const mockApiUserBudgets: Budget[] = [apiBudget({ id: "budget-standard", name: "Standard Monthly", amount: "200.00", isDefault: true }), apiBudget({ id: "budget-engineering", name: "Engineering", amount: "2000.00" })];
