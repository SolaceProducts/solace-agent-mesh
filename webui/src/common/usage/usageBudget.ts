// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageBudget, UsageBudgetStatus } from "@/api/usage";

import { formatBudgetMoney } from "@/utils";

export const STATUS_INDICATOR: Record<UsageBudgetStatus, string> = {
    ok: "bg-(--success-wMain)",
    approaching: "bg-(--warning-wMain)",
    exceeded: "bg-(--error-wMain)",
    unlimited: "bg-(--secondary-w40)",
};

export function formatCurrentBudget(budget: UsageBudget): string {
    return budget.effectiveAmount === null ? "Unlimited" : formatBudgetMoney(budget.effectiveAmount);
}

export function formatTopUpBreakdown(budget: UsageBudget): string | null {
    if (!budget.topUp || budget.amount === null) return null;
    return `${formatBudgetMoney(budget.amount)} + ${formatBudgetMoney(budget.topUp.amount)} top-up`;
}

const MS_PER_DAY = 86_400_000;

export function daysLeftInPeriod(periodEnd: string, now: Date): number {
    return Math.max(0, Math.ceil((Date.parse(periodEnd) - now.getTime()) / MS_PER_DAY));
}
