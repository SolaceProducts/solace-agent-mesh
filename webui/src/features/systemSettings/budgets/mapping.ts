// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Budget, CreateBudgetRequest, UpdateBudgetRequest } from "@/api/budgets";

import type { BudgetFormValues } from "./budgetForm.schema";
import type { BudgetRow } from "./types";

export function toBudgetRow(budget: Budget): BudgetRow {
    return {
        id: budget.id,
        name: budget.name,
        scope: budget.subjectType,
        amount: budget.amount,
        warnPercentage: budget.warningPercentage,
        isDefault: budget.isDefault,
        assignedCount: budget.assignedCount,
        hasUnlistedAssignments: budget.hiddenAssignedCount + budget.pendingCount > 0,
    };
}

export function toCreateRequest(values: BudgetFormValues): CreateBudgetRequest {
    return { name: values.name, subjectType: values.scope, amount: values.amount, warningPercentage: values.warnPercentage };
}

export function toUpdateRequest(budget: BudgetRow, values: BudgetFormValues): UpdateBudgetRequest {
    const changes: UpdateBudgetRequest = {};
    if (values.name !== budget.name) changes.name = values.name;
    if (values.amount !== budget.amount) changes.amount = values.amount;
    if (values.warnPercentage !== budget.warnPercentage || changes.amount === null) changes.warningPercentage = values.warnPercentage;
    return changes;
}
