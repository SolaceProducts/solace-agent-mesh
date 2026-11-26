// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { CircleCheck } from "lucide-react";

import { type UsageBudget, type UsageSavings, type UsageTimestamp, useSelfUsageSavings } from "@/api/usage";

import { costWithoutSavings, formatCompactTokens, formatCurrentBudget, isSavingAtLeastACent, percentOfBudget } from "@/common/usage";

import { formatUsageMoney, formatUsagePercent } from "@/utils";

const THIS_MONTH = { period: "this_month" } as const;

interface SmartContextSavingsNoteProps {
    spend: string;
    budget: UsageBudget | null;
    dataAsOf: UsageTimestamp | null;
}

function withoutSavingsLine(spend: string, avoidedCost: string, budget: UsageBudget | null): string | null {
    const without = costWithoutSavings(spend, avoidedCost);
    if (without === null) return null;
    const percentWithout = budget ? percentOfBudget(without, budget.effectiveAmount) : null;
    if (budget?.usagePercent == null || percentWithout === null || formatUsagePercent(percentWithout) === formatUsagePercent(budget.usagePercent)) return `Without Smart Context, this month's cost would be ${formatUsageMoney(without)}.`;
    return `Without Smart Context, this month's cost would use ${formatUsagePercent(percentWithout)} of the ${formatCurrentBudget(budget)} budget instead of ${formatUsagePercent(budget.usagePercent)}.`;
}

function NoteText({ savings, avoidedInputTokens, spend, budget, dataAsOf }: Readonly<{ savings: UsageSavings; avoidedInputTokens: number } & SmartContextSavingsNoteProps>) {
    if (savings.avoidedCost === null || !isSavingAtLeastACent(savings.avoidedCost)) {
        return <p className="font-semibold">Smart Context kept an estimated {formatCompactTokens(avoidedInputTokens)} input tokens out of LLM requests this month.</p>;
    }
    const without = savings.dataAsOf === dataAsOf ? withoutSavingsLine(spend, savings.avoidedCost, budget) : null;
    return (
        <>
            <p className="font-semibold">Smart Context saved an estimated {formatUsageMoney(savings.avoidedCost)} this month.</p>
            {without && <p>{without}</p>}
        </>
    );
}

export function SmartContextSavingsNote({ spend, budget, dataAsOf }: Readonly<SmartContextSavingsNoteProps>) {
    const { data: savings } = useSelfUsageSavings(THIS_MONTH);
    const avoidedInputTokens = savings?.avoidedInputTokens;
    if (!savings || avoidedInputTokens == null || avoidedInputTokens <= 0) return null;
    return (
        <div className="flex items-start gap-3 rounded-sm border-l-4 border-(--success-wMain) bg-(--success-w10) px-3 py-2 text-sm" data-testid="smartContextSavingsNote">
            <CircleCheck className="mt-0.5 size-4 shrink-0 text-(--success-wMain)" aria-hidden />
            <div className="flex flex-col gap-0.5">
                <NoteText savings={savings} avoidedInputTokens={avoidedInputTokens} spend={spend} budget={budget} dataAsOf={dataAsOf} />
            </div>
        </div>
    );
}
