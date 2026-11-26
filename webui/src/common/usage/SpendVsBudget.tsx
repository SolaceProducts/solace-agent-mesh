// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ReactNode } from "react";

import type { UsageBudget, UsageMoney } from "@/api/usage";

import { cn, formatUsageMoney } from "@/utils";

import { BudgetBar, BudgetBarText } from "./BudgetBar";

interface SpendVsBudgetProps {
    spend: UsageMoney | null;
    budget: UsageBudget | null;
    noBudgetLabel: string;
    unlimitedLabel?: string;
    heading?: "h3" | "h4";
    layout?: "stacked" | "inline";
    className?: string;
}

const CAPTION = "this month";

function InlineSpend({ spend, budget, noBudgetLabel, unlimitedLabel }: Readonly<Pick<SpendVsBudgetProps, "spend" | "budget" | "noBudgetLabel" | "unlimitedLabel">>) {
    let text: ReactNode = noBudgetLabel;
    if (budget) text = budget.usagePercent === null ? (unlimitedLabel ?? "No budget limit") : <BudgetBarText budget={budget} caption={CAPTION} />;
    return (
        <>
            <p className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
                {spend !== null && <span className="text-2xl font-semibold tabular-nums">{formatUsageMoney(spend)}</span>}
                <span className="text-sm">{text}</span>
            </p>
            {budget && <BudgetBar budget={budget} showText={false} />}
        </>
    );
}

export function SpendVsBudget({ spend, budget, noBudgetLabel, unlimitedLabel, heading: Heading = "h3", layout = "stacked", className }: Readonly<SpendVsBudgetProps>) {
    return (
        <section aria-label="Cost Versus Current Budget" className={cn("flex flex-col gap-2", className)}>
            <Heading className="text-sm font-medium text-(--secondary-text-wMain)">Cost Versus Current Budget</Heading>
            {layout === "inline" ? (
                <InlineSpend spend={spend} budget={budget} noBudgetLabel={noBudgetLabel} unlimitedLabel={unlimitedLabel} />
            ) : (
                <>
                    {spend !== null && <p className="text-2xl font-semibold tabular-nums">{formatUsageMoney(spend)}</p>}
                    {budget ? <BudgetBar budget={budget} caption={CAPTION} unlimitedLabel={unlimitedLabel} /> : <p className="text-xs text-(--secondary-text-wMain)">{noBudgetLabel}</p>}
                </>
            )}
        </section>
    );
}
