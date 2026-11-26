// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

export const BUDGETS_ROUTE = "/budgets";

export const budgetUsageRoute = (budgetId: string) => `${BUDGETS_ROUTE}/${encodeURIComponent(budgetId)}/usage`;
