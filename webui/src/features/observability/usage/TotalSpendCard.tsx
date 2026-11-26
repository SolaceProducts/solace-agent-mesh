// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useId, useMemo } from "react";
import { ArrowDown, ArrowUp } from "lucide-react";

import type { UsageBudget, UsageComparison, UsageDay, UsageMoney, UsageSummary, UsageWindow } from "@/api/usage";

import { BudgetBar, ChartDataTable, formatUsageMonth, HintedText, MonthlySpendChart, type MonthSpend, type MonthSpendRow } from "@/common/usage";

import { TOOLTIP_SURFACE } from "@/ui/tooltip-surface";

import { cn, formatUsageMoney, formatUsagePercent, isUsageMoney, parseUsageMoney } from "@/utils";

import { daysOfWindow } from "./usageDays";
import { formatUsageWindow } from "./usageFormat";
import { monthsOfPeriod, partialMonthNote, type PeriodMonth, reachesToday } from "./usageMonths";
import { USAGE_HISTORY_DAYS } from "./usagePeriod";
import { WEEKLY_AFTER_DAYS } from "./usageWeeks";

interface TotalSpendCardProps {
    summary: UsageSummary;
    comparisonLabel: string;
    isThisMonth: boolean;
    today: string;
}

const COMPARISON_NOT_KEPT = `No usage from the previous period to compare. Usage is kept for ${USAGE_HISTORY_DAYS} days.`;
const HISTORY_BUILDING = "Older usage is still being calculated, so there is nothing to compare yet.";

const MONTHS_CHART_HEIGHT = 120;
const SPENT_COLUMN = { key: "spent", label: "Cost" };

function ComparedPeriod({ label, hint }: Readonly<{ label: string; hint?: string }>) {
    const period = label.replace(/^vs\. /, "");
    return <>vs. {hint ? <HintedText hint={hint}>{period}</HintedText> : period}</>;
}

const NO_COST_RECORDED = "No cost recorded for the previous period.";

const SHOWN_AS_ZERO = formatUsageMoney("0.0000000000");

function Change({ rising, children }: Readonly<{ rising: boolean; children: ReactNode }>) {
    const Arrow = rising ? ArrowUp : ArrowDown;
    return (
        <span className={cn("flex items-center font-semibold", rising ? "text-(--error-wMain)" : "text-(--success-wMain)")}>
            <Arrow className="size-3" aria-hidden />
            {children}
        </span>
    );
}

function ComparisonLine({ comparison, window, cost, label }: Readonly<{ comparison: UsageComparison; window: UsageWindow; cost: UsageMoney; label: string }>) {
    const change = comparison.changePercent;
    const dates = formatUsageWindow(window);
    if (change === null) {
        const noCost = dates ? `No cost recorded ${dates}.` : NO_COST_RECORDED;
        if (!isUsageMoney(cost) || parseUsageMoney(cost) < 0n) return <NoComparisonLine label={label} reason={noCost} />;
        const period = <ComparedPeriod label={label} hint={noCost} />;
        if (formatUsageMoney(cost) === SHOWN_AS_ZERO) return <p className="text-xs text-(--secondary-text-wMain)">No change {period}</p>;
        return (
            <p className="flex items-center gap-1 text-xs text-(--secondary-text-wMain)">
                <Change rising>Up from $0</Change>
                <span>{period}</span>
            </p>
        );
    }
    const period = <ComparedPeriod label={label} hint={dates ?? undefined} />;
    const magnitude = formatUsagePercent(Math.abs(change));
    if (magnitude === formatUsagePercent(0)) return <p className="text-xs text-(--secondary-text-wMain)">No change {period}</p>;
    const rising = change > 0;
    return (
        <p className="flex items-center gap-1 text-xs text-(--secondary-text-wMain)">
            <Change rising={rising}>
                <span className="sr-only">{rising ? "Up" : "Down"}</span>
                {magnitude}
            </Change>
            <span>{period}</span>
        </p>
    );
}

function NoComparisonLine({ label, reason }: Readonly<{ label: string; reason: string }>) {
    return (
        <p className="text-xs text-(--secondary-text-wMain)">
            <span aria-hidden>—</span> <ComparedPeriod label={label} hint={reason} />
        </p>
    );
}

function currentBudgetShare(budget: UsageBudget | null): string | null {
    return budget?.usagePercent == null ? null : `${formatUsagePercent(budget.usagePercent)} of current budget`;
}

export function MonthSpendTooltip({ active, payload, today, budget }: Readonly<{ active?: boolean; payload?: ReadonlyArray<{ payload?: unknown }>; today: string; budget: UsageBudget | null }>) {
    const row = active ? (payload?.[0]?.payload as MonthSpendRow<PeriodMonth> | undefined) : undefined;
    if (!row) return null;
    const isCurrent = row.month === today.slice(0, 7) && !row.source.partial;
    const share = isCurrent ? currentBudgetShare(budget) : null;
    return (
        <div className={TOOLTIP_SURFACE}>
            <div className="font-semibold">{formatUsageMonth(row.month)}</div>
            <div>
                {formatUsageMoney(row.source.cost)} cost{isCurrent && " so far"}
            </div>
            {share && <div>{share}</div>}
            {row.source.partial && <div>{partialMonthNote(row.source)}</div>}
        </div>
    );
}

function MonthlySpend({ days, budget, today }: Readonly<{ days: readonly UsageDay[]; budget: UsageBudget | null; today: string }>) {
    const headingId = useId();
    const months = useMemo(() => monthsOfPeriod(days, today), [days, today]);
    const currentMonth = today.slice(0, 7);
    const toToday = reachesToday(months, today);
    const share = toToday ? currentBudgetShare(budget) : null;
    const spentLabel = (month: MonthSpend) => (month.month === currentMonth && share ? `${formatUsageMoney(month.cost)} (${share})` : formatUsageMoney(month.cost));
    const monthLabel = (month: PeriodMonth) => (month.partial ? `${formatUsageMonth(month.month)} (${partialMonthNote(month)})` : formatUsageMonth(month.month));
    return (
        <section aria-labelledby={headingId} className="mt-2 flex flex-col gap-1">
            <h4 id={headingId} className="sr-only">
                Cost by Month
            </h4>
            <ChartDataTable labelledBy={headingId} rowHeader="Month" columns={[SPENT_COLUMN]} rows={months.map(month => ({ key: month.month, label: monthLabel(month), values: { spent: spentLabel(month) } }))} />
            <MonthlySpendChart
                months={months}
                currentBudget={budget?.effectiveAmount ?? null}
                currentMonth={currentMonth}
                currentStatus={toToday ? (budget?.status ?? null) : null}
                tooltip={<MonthSpendTooltip today={today} budget={budget} />}
                height={MONTHS_CHART_HEIGHT}
            />
        </section>
    );
}

export function TotalSpendCard({ summary, comparisonLabel, isThisMonth, today }: Readonly<TotalSpendCardProps>) {
    const { totals, comparison, meshBudget } = summary;
    const days = useMemo(() => daysOfWindow(summary.daily, summary.window), [summary.daily, summary.window]);
    return (
        <section aria-labelledby="usage-total-spend" className="flex flex-col gap-2 rounded-sm border p-4" data-testid="usageTotalSpend">
            <h3 id="usage-total-spend" className="text-sm font-medium text-(--secondary-text-wMain)">
                Agent Mesh Total Cost
            </h3>
            <div className="flex flex-col gap-0.5">
                <p className="text-2xl font-semibold">{formatUsageMoney(totals.cost)}</p>
                {comparison && summary.comparisonWindow ? (
                    <ComparisonLine comparison={comparison} window={summary.comparisonWindow} cost={totals.cost} label={comparisonLabel} />
                ) : (
                    <NoComparisonLine label={comparisonLabel} reason={summary.historyComplete ? COMPARISON_NOT_KEPT : HISTORY_BUILDING} />
                )}
            </div>
            {meshBudget && isThisMonth && <BudgetBar budget={meshBudget} caption="this month" unlimitedLabel="No Agent Mesh budget limit" className="mt-2" />}
            {days.length > WEEKLY_AFTER_DAYS && <MonthlySpend days={days} budget={meshBudget} today={today} />}
        </section>
    );
}
