// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { type UsageBudgetStatus, type UsageMonth, useSelfUsage } from "@/api/usage";

import { type BudgetMonth, MonthlyBudgetChart, SpendVsBudget, TRY_AGAIN_SLOW_READ_HINT, UsageFreshness, usageLoadErrorMessage } from "@/common/usage";

import { Skeleton } from "@/ui/skeleton";

import { MessageBanner } from "@/common";

import { SmartContextSavingsNote } from "./SmartContextSavingsNote";

const THIS_MONTH = { period: "this_month" } as const;

const DAY_PATTERN = /^\d{4}-\d{2}-\d{2}$/;
const PARTIAL_DAY = new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", timeZone: "UTC" });

function partlyKeptFrom(month: UsageMonth): Date | null {
    const fromDate = month.partialFromDate;
    if (fromDate === null || !DAY_PATTERN.test(fromDate)) return null;
    const from = new Date(`${fromDate}T00:00:00.000Z`);
    return Number.isNaN(from.getTime()) ? null : from;
}

function MonthsChart({ months, currentStatus }: Readonly<{ months: readonly UsageMonth[]; currentStatus: UsageBudgetStatus | null }>) {
    const current = months.at(-1);
    const budgetMonths = useMemo<BudgetMonth[]>(
        () =>
            months.map(month => {
                const from = partlyKeptFrom(month);
                return { month: month.month, cost: month.cost, budgetAmount: month.budgetAmount, partialNote: from ? `kept from ${PARTIAL_DAY.format(from)}` : null };
            }),
        [months]
    );
    return <MonthlyBudgetChart heading="Last Three Months" months={budgetMonths} currentMonth={current?.month ?? ""} currentBudget={current?.budgetAmount ?? null} currentStatus={currentStatus} budgetLegend={false} />;
}

export function UsageSettings() {
    const query = useSelfUsage(THIS_MONTH);

    if (query.error && !query.data) return <MessageBanner variant="error" message={usageLoadErrorMessage("Couldn't load your usage.", query.error, TRY_AGAIN_SLOW_READ_HINT)} />;
    if (!query.data) return <Skeleton className="h-72" />;

    const spend = query.data.budget?.periodSpend ?? query.data.totals.cost;
    return (
        <div className="flex flex-col gap-4">
            <div className="flex flex-col gap-4 rounded-sm border p-4" data-testid="usageSettings">
                <SpendVsBudget spend={spend} budget={query.data.budget} noBudgetLabel="No budget applies to you" unlimitedLabel="Your budget has no limit" heading="h4" layout="inline" />
                <MonthsChart months={query.data.months} currentStatus={query.data.budget?.status ?? null} />
                <UsageFreshness dataAsOf={query.data.dataAsOf} historyComplete={query.data.historyComplete} />
            </div>
            <SmartContextSavingsNote spend={spend} budget={query.data.budget} dataAsOf={query.data.dataAsOf} />
        </div>
    );
}
