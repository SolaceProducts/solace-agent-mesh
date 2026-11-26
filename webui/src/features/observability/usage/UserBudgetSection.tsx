// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useMemo } from "react";

import type { UsageBudget, UsageMoney, UsageUserDetail } from "@/api/usage";

import { CurrentBudgetAmount, daysLeftInPeriod, MonthlyBudgetChart } from "@/common/usage";

import { Button } from "@/ui/button";

import { MessageBanner } from "@/common";
import { formatUsageMoney, formatUsagePercent } from "@/utils";

import { daysOfWindow } from "./usageDays";
import { budgetMonthsOfPeriod } from "./usageMonths";
import { WEEKLY_AFTER_DAYS } from "./usageWeeks";

function spendSentence(spend: string, daysLeft: number): string {
    if (daysLeft === 0) return `Cost in a budget period that has ended: ${spend}.`;
    return `Cost this month: ${spend}, with ${daysLeft} ${daysLeft === 1 ? "day" : "days"} left.`;
}

interface BudgetStandingBannerProps {
    name: string;
    budget: UsageBudget;
    now: Date;
    onGoToUsers?: () => void;
}

export function BudgetStandingBanner({ name, budget, now, onGoToUsers }: Readonly<BudgetStandingBannerProps>) {
    if ((budget.status !== "approaching" && budget.status !== "exceeded") || budget.usagePercent === null) return null;
    const detail = spendSentence(formatUsageMoney(budget.periodSpend), daysLeftInPeriod(budget.periodEnd, now));
    return (
        <MessageBanner
            variant={budget.status === "exceeded" ? "error" : "warning"}
            message={
                <span className="flex flex-1 flex-wrap items-center justify-between gap-x-4 gap-y-2">
                    <span className="flex flex-col">
                        <span className="font-semibold">
                            {name} is at {formatUsagePercent(budget.usagePercent)} of their <CurrentBudgetAmount budget={budget} /> current budget
                        </span>
                        <span>{detail}</span>
                    </span>
                    {onGoToUsers && (
                        <Button variant="ghost" size="sm" className="h-auto p-0" onClick={onGoToUsers}>
                            Go to Users
                        </Button>
                    )}
                </span>
            }
        />
    );
}

function Figure({ label, children }: Readonly<{ label: string; children: ReactNode }>) {
    return (
        <div className="flex flex-col gap-1">
            <dt className="text-sm font-medium text-(--secondary-text-wMain)">{label}</dt>
            <dd className="flex flex-col">{children}</dd>
        </div>
    );
}

export function UserMonthlySpend({ detail, today }: Readonly<{ detail: UsageUserDetail; today: string }>) {
    const months = useMemo(() => {
        const days = daysOfWindow(detail.daily, detail.window);
        return days.length > WEEKLY_AFTER_DAYS ? budgetMonthsOfPeriod(days, today, detail.budget) : [];
    }, [detail.daily, detail.window, detail.budget, today]);
    if (months.length === 0) return null;
    const currentMonth = today.slice(0, 7);
    const toToday = months.some(month => month.month === currentMonth && month.partialNote === null);
    return (
        <div className="rounded-sm border p-4">
            <MonthlyBudgetChart heading="Cost by Month" months={months} currentMonth={currentMonth} currentBudget={detail.budget?.effectiveAmount ?? null} currentStatus={toToday ? (detail.budget?.status ?? null) : null} />
        </div>
    );
}

export function PeriodSpendAndBudget({ spend, budget }: Readonly<{ spend: UsageMoney; budget: UsageBudget | null }>) {
    return (
        <dl className="grid grid-cols-2 gap-4 rounded-sm border p-4" aria-label="Cost and Budget">
            <Figure label="Cost This Period">
                <span className="text-2xl font-semibold tabular-nums">{formatUsageMoney(spend)}</span>
            </Figure>
            <Figure label="Current Monthly Budget">{budget ? <CurrentBudgetAmount budget={budget} className="text-2xl font-semibold tabular-nums" /> : <span className="text-sm text-(--secondary-text-wMain)">No budget applies</span>}</Figure>
        </dl>
    );
}
