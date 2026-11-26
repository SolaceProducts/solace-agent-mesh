// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { CircleCheck } from "lucide-react";

import type { UsageSavings, UsageSummary } from "@/api/usage";

import { NewTabLink } from "@/common/NewTabLink";
import { costWithoutSavings, formatCompactTokens, isSavingAtLeastACent, usageLoadErrorMessage } from "@/common/usage";

import { Badge } from "@/ui/badge";
import { Skeleton } from "@/ui/skeleton";

import { HelpPopover, MessageBanner } from "@/common";
import { cn, DOC_LINKS, formatUsageMoney, formatUsageMoneyWithFloor, isZeroUsageMoney } from "@/utils";

const HELP = "Smart Context keeps large content out of the model, which lowers the cost of every step that follows. These figures estimate the input tokens, and their cost, that the content would have added to the same calls.";
const NO_COST_ESTIMATE = "These savings have no cost estimate because they came from models with no price set or with a price of $0.";
const NOTHING_SAVED = "No savings were recorded in this period.";
const NOT_MEASURED = "Smart Context savings were not measured in this period.";
const NOT_MEASURED_FIGURE = "—";

interface SmartContextSavingsCardProps {
    savings?: UsageSavings;
    error: Error | null;
    summary: UsageSummary;
    savingsIsStale: boolean;
    summaryIsStale: boolean;
}

function WithoutSavingsLine({ savings, summary, current }: Readonly<{ savings: UsageSavings; summary: UsageSummary; current: boolean }>) {
    const without = current && savings.avoidedCost !== null && isSavingAtLeastACent(savings.avoidedCost) ? costWithoutSavings(summary.totals.cost, savings.avoidedCost) : null;
    if (without === null) return null;
    return (
        <p className="flex items-start gap-1.5 border-t pt-2 text-xs">
            <CircleCheck className="size-4 shrink-0 text-(--success-wMain)" aria-hidden />
            <span>
                Without Smart Context, the cost would have been <strong>{formatUsageMoney(without)}</strong>.
            </span>
        </p>
    );
}

function SavingsFigures({ savings, summary, current }: Readonly<{ savings: UsageSavings; summary: UsageSummary; current: boolean }>) {
    if (savings.avoidedInputTokens === null) {
        return (
            <div className="flex flex-col gap-0.5">
                <p className="text-2xl font-semibold">{NOT_MEASURED_FIGURE}</p>
                <p className="text-xs text-(--secondary-text-wMain)">{NOT_MEASURED}</p>
            </div>
        );
    }
    const tokens = `${formatCompactTokens(savings.avoidedInputTokens)} input tokens`;
    if (savings.avoidedInputTokens <= 0) {
        return (
            <div className="flex flex-col gap-0.5">
                <p className="text-2xl font-semibold">{formatUsageMoney("0")}</p>
                <p className="text-xs text-(--secondary-text-wMain)">{NOTHING_SAVED}</p>
            </div>
        );
    }
    if (savings.avoidedCost === null || isZeroUsageMoney(savings.avoidedCost)) {
        return (
            <div className="flex flex-col gap-0.5">
                <p className="text-2xl font-semibold">{tokens}</p>
                <p className="text-xs text-(--secondary-text-wMain)">{NO_COST_ESTIMATE}</p>
            </div>
        );
    }
    return (
        <>
            <div className="flex flex-wrap items-baseline gap-x-2">
                <p className="text-2xl font-semibold">{formatUsageMoneyWithFloor(savings.avoidedCost, 2)}</p>
                <p className="text-sm text-(--secondary-text-wMain)">{tokens}</p>
            </div>
            {savings.unpricedCalls > 0 && <p className="text-xs text-(--secondary-text-wMain)">{unpricedCallsNote(savings)}</p>}
            <WithoutSavingsLine savings={savings} summary={summary} current={current} />
        </>
    );
}

function unpricedCallsNote(savings: UsageSavings): string {
    const total = savings.calls.toLocaleString();
    if (savings.unpricedCalls === 1) return `1 of ${total} calls used a model with no price set, so the cost leaves out any tokens that call avoided.`;
    return `${savings.unpricedCalls.toLocaleString()} of ${total} calls used a model with no price set, so the cost leaves out any tokens those calls avoided.`;
}

export function SmartContextSavingsCard({ savings, error, summary, savingsIsStale, summaryIsStale }: Readonly<SmartContextSavingsCardProps>) {
    let body;
    if (savings) body = <SavingsFigures savings={savings} summary={summary} current={!savingsIsStale && !summaryIsStale && savings.dataAsOf === summary.dataAsOf} />;
    else if (error) body = <MessageBanner variant="error" message={usageLoadErrorMessage("Couldn't load the savings.", error)} />;
    else body = <Skeleton className="h-12" />;

    return (
        <section
            aria-labelledby="usage-smart-context-savings"
            aria-busy={savingsIsStale}
            className={cn("flex flex-col gap-2 rounded-sm border p-4 transition-opacity", savingsIsStale && !summaryIsStale && "opacity-60")}
            data-testid="usageSmartContextSavings"
        >
            <div className="flex items-center gap-2">
                <h3 id="usage-smart-context-savings" className="text-sm font-medium text-(--secondary-text-wMain)">
                    Smart Context Savings
                </h3>
                <Badge>Estimated</Badge>
                <span className="ml-auto">
                    <HelpPopover
                        label="About Smart Context savings"
                        size="16"
                        side="bottom"
                        align="end"
                        content={
                            <div className="flex flex-col gap-2">
                                <p>{HELP}</p>
                                <NewTabLink to={DOC_LINKS.smartContextSavings} label="Learn more about Smart Context savings" className="h-auto justify-start p-0 has-[>svg]:px-0" />
                            </div>
                        }
                    />
                </span>
            </div>
            {body}
        </section>
    );
}
