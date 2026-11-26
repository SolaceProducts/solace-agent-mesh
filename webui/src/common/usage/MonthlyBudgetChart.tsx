// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useId } from "react";

import type { UsageBudgetStatus, UsageMoney } from "@/api/usage";

import { TOOLTIP_SURFACE } from "@/ui/tooltip-surface";

import { formatBudgetMoney, formatUsageMoney } from "@/utils";

import { ChartDataTable } from "./ChartDataTable";
import { MonthlySpendChart, type MonthSpend, type MonthSpendRow } from "./MonthlySpendChart";
import { formatUsageMonth } from "./usageChartFormat";

const CHART_HEIGHT = 160;
const SPENT_COLUMN = { key: "spent", label: "Spent" };
const BUDGET_COLUMN = { key: "budget", label: "Budget" };
const EARLIER_MONTH_BUDGET = "Not recorded";

export interface BudgetMonth extends MonthSpend {
    budgetAmount: UsageMoney | null;
    partialNote: string | null;
}

function monthBudgetLabel(month: BudgetMonth, currentMonth: string): string | null {
    if (month.month !== currentMonth || !month.budgetAmount) return null;
    return `Current budget ${formatBudgetMoney(month.budgetAmount)}`;
}

interface BudgetMonthTooltipProps {
    active?: boolean;
    payload?: ReadonlyArray<{ payload?: unknown }>;
    currentMonth: string;
}

export function BudgetMonthTooltip({ active, payload, currentMonth }: Readonly<BudgetMonthTooltipProps>) {
    const row = active ? (payload?.[0]?.payload as MonthSpendRow<BudgetMonth> | undefined) : undefined;
    if (!row) return null;
    const budget = monthBudgetLabel(row.source, currentMonth);
    return (
        <div className={TOOLTIP_SURFACE}>
            <div className="font-semibold">{formatUsageMonth(row.source.month)}</div>
            <div>{formatUsageMoney(row.source.cost)} spent</div>
            {budget && <div>{budget}</div>}
            {row.source.partialNote && <div className="first-letter:uppercase">{row.source.partialNote}</div>}
        </div>
    );
}

interface MonthlyBudgetChartProps {
    heading: string;
    months: readonly BudgetMonth[];
    currentMonth: string;
    currentBudget: UsageMoney | null;
    currentStatus: UsageBudgetStatus | null;
    budgetLegend?: boolean;
}

export function MonthlyBudgetChart({ heading, months, currentMonth, currentBudget, currentStatus, budgetLegend }: Readonly<MonthlyBudgetChartProps>) {
    const headingId = useId();
    const hasBudget = months.some(month => month.budgetAmount !== null);
    const bars = months.map(month => ({ ...month, partial: month.partialNote !== null }));

    return (
        <section aria-labelledby={headingId} className="flex flex-col gap-1">
            <h4 id={headingId} className="sr-only">
                {heading}
            </h4>
            <ChartDataTable
                labelledBy={headingId}
                rowHeader="Month"
                columns={hasBudget ? [SPENT_COLUMN, BUDGET_COLUMN] : [SPENT_COLUMN]}
                rows={months.map(month => ({
                    key: month.month,
                    label: month.partialNote ? `${formatUsageMonth(month.month)} (${month.partialNote})` : formatUsageMonth(month.month),
                    values: { spent: formatUsageMoney(month.cost), budget: month.month === currentMonth ? (monthBudgetLabel(month, currentMonth) ?? "None") : EARLIER_MONTH_BUDGET },
                }))}
            />
            <MonthlySpendChart months={bars} currentBudget={currentBudget} currentMonth={currentMonth} currentStatus={currentStatus} tooltip={<BudgetMonthTooltip currentMonth={currentMonth} />} height={CHART_HEIGHT} budgetLegend={budgetLegend} />
        </section>
    );
}
