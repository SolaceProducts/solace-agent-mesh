// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageBudget } from "@/api/usage";

import { Progress } from "@/ui/progress";

import { cn, formatUsagePercent } from "@/utils";

import { CurrentBudgetAmount } from "./CurrentBudgetAmount";
import { STATUS_INDICATOR } from "./usageBudget";

export function BudgetBarText({ budget, caption }: Readonly<{ budget: UsageBudget; caption?: string }>) {
    if (budget.usagePercent === null) return null;
    return (
        <>
            {formatUsagePercent(budget.usagePercent)} of <CurrentBudgetAmount budget={budget} /> current budget{caption && ` ${caption}`}
        </>
    );
}

interface BudgetBarProps {
    budget: UsageBudget;
    caption?: string;
    unlimitedLabel?: string;
    showText?: boolean;
    className?: string;
}

export function BudgetBar({ budget, caption, unlimitedLabel = "No budget limit", showText = true, className }: Readonly<BudgetBarProps>) {
    if (budget.usagePercent === null) {
        return showText ? <p className={cn("text-xs text-(--secondary-text-wMain)", className)}>{unlimitedLabel}</p> : null;
    }
    return (
        <div className={cn("flex flex-col gap-1.5", className)}>
            <Progress value={budget.usagePercent} indicatorClassName={STATUS_INDICATOR[budget.status]} />
            {showText && (
                <p className="text-xs">
                    <BudgetBarText budget={budget} caption={caption} />
                </p>
            )}
        </div>
    );
}
