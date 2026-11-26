// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageBudget, UsageDay, UsageMoney } from "@/api/usage";

import type { BudgetMonth, MonthSpend } from "@/common/usage";

import { sumUsageMoney } from "@/utils";

export interface PeriodMonth extends MonthSpend {
    days: number;
    daysInMonth: number;
    partial: boolean;
}

function daysInMonth(month: string): number {
    return new Date(Date.UTC(Number(month.slice(0, 4)), Number(month.slice(5, 7)), 0)).getUTCDate();
}

export function monthsOfPeriod(days: readonly UsageDay[], today: string): PeriodMonth[] {
    const currentMonth = today.slice(0, 7);
    const byMonth = new Map<string, UsageDay[]>();
    for (const day of [...days].sort((a, b) => a.date.localeCompare(b.date))) {
        const month = day.date.slice(0, 7);
        byMonth.set(month, [...(byMonth.get(month) ?? []), day]);
    }
    return [...byMonth].map(([month, monthDays]) => {
        const total = daysInMonth(month);
        const daysSoFar = month === currentMonth ? Number(today.slice(8, 10)) : total;
        return { month, cost: sumUsageMoney(monthDays.map(day => day.cost)), days: monthDays.length, daysInMonth: total, partial: monthDays.length < daysSoFar };
    });
}

export function partialMonthNote(month: PeriodMonth): string {
    return `Partial month · ${month.days} of ${month.daysInMonth} days`;
}

function monthBudget(budget: UsageBudget | null, isCurrentMonth: boolean): UsageMoney | null {
    if (!budget) return null;
    return isCurrentMonth ? budget.effectiveAmount : budget.amount;
}

export function budgetMonthsOfPeriod(days: readonly UsageDay[], today: string, budget: UsageBudget | null): BudgetMonth[] {
    const currentMonth = today.slice(0, 7);
    return monthsOfPeriod(days, today).map(month => ({
        month: month.month,
        cost: month.cost,
        budgetAmount: monthBudget(budget, month.month === currentMonth),
        partialNote: month.partial ? partialMonthNote(month) : null,
    }));
}

export function reachesToday(months: readonly PeriodMonth[], today: string): boolean {
    return months.some(month => month.month === today.slice(0, 7) && !month.partial);
}
