// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { Budget, ListBudgetAssignmentsParams, ListBudgetsParams, ListBudgetSubjectsParams } from "./types";

export const budgetKeys = {
    all: ["budgets"] as const,
    lists: () => [...budgetKeys.all, "list"] as const,
    list: (params: ListBudgetsParams) => [...budgetKeys.lists(), params] as const,
    detail: (id: string) => [...budgetKeys.all, "detail", id] as const,
    defaultBudget: (subjectType: Budget["subjectType"]) => [...budgetKeys.all, "default", subjectType] as const,
    subjects: (params: ListBudgetSubjectsParams) => [...budgetKeys.all, "subjects", params] as const,
    assignments: (params: Omit<ListBudgetAssignmentsParams, "pageNumber">) => [...budgetKeys.all, "assignments", params] as const,
};
