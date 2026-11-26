// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageBudget } from "@/api/usage";

import { daysLeftInPeriod } from "@/common/usage";

import { Button } from "@/ui/button";

import { MessageBanner } from "@/common";
import { compareUsageMoney, formatUsageMoney, formatUsagePercent } from "@/utils";

interface MeshBudgetBannerProps {
    budget: UsageBudget;
    now: Date;
    onGoToBudgets?: () => void;
}

export function MeshBudgetBanner({ budget, now, onGoToBudgets }: Readonly<MeshBudgetBannerProps>) {
    if ((budget.status !== "approaching" && budget.status !== "exceeded") || budget.usagePercent === null || budget.effectiveAmount === null) return null;
    const reached = budget.status === "exceeded";
    const daysLeft = daysLeftInPeriod(budget.periodEnd, now);
    const spend = formatUsageMoney(budget.periodSpend);
    const amount = formatUsageMoney(budget.effectiveAmount);
    const spent = compareUsageMoney(budget.periodSpend, budget.effectiveAmount) > 0 ? `${spend} spent, over the ${amount} budget,` : `${spend} of ${amount} spent`;
    return (
        <MessageBanner
            variant={reached ? "error" : "warning"}
            message={
                <span className="flex flex-1 flex-wrap items-center justify-between gap-x-4 gap-y-2">
                    <span className="flex flex-col">
                        <span className="font-semibold">{reached ? "Agent Mesh monthly budget reached" : `Agent Mesh is at ${formatUsagePercent(budget.usagePercent)} of its monthly budget`}</span>
                        <span>
                            {spent} with {daysLeft} {daysLeft === 1 ? "day" : "days"} left in the period.
                        </span>
                    </span>
                    {onGoToBudgets && (
                        <Button variant="ghost" size="sm" className="h-auto p-0" onClick={onGoToBudgets}>
                            Go to Budgets
                        </Button>
                    )}
                </span>
            }
        />
    );
}
