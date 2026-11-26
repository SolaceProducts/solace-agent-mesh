// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { ReactNode } from "react";

import { type UsageSummary, type UsageWindowParams, useUsageSavings } from "@/api/usage";

import { UsageFreshness, usageLoadErrorMessage } from "@/common/usage";

import { Skeleton } from "@/ui/skeleton";

import { MessageBanner } from "@/common";
import { cn } from "@/utils";

import { SmartContextSavingsCard } from "./SmartContextSavingsCard";
import { TokenCostChart } from "./TokenCostChart";
import { TotalSpendCard } from "./TotalSpendCard";

interface UsageOverviewProps {
    summary?: UsageSummary;
    windowParams: UsageWindowParams;
    isLoading: boolean;
    isStale: boolean;
    error: Error | null;
    comparisonLabel: string;
    isThisMonth: boolean;
    periodPicker: ReactNode;
    today: string;
}

function OverviewGrid({ cards, chart, withSavings, className, isStale }: Readonly<{ cards: ReactNode; chart: ReactNode; withSavings: boolean; className?: string; isStale?: boolean }>) {
    if (!withSavings) {
        return (
            <div className={cn("grid gap-4 lg:grid-cols-[18rem_1fr]", className)} aria-busy={isStale}>
                {cards}
                {chart}
            </div>
        );
    }
    return (
        <div className={cn("@container", className)} aria-busy={isStale}>
            <div className="grid gap-4 @3xl:grid-cols-2 @7xl:grid-cols-[18rem_18rem_1fr]">
                {cards}
                <div className="flex min-w-0 *:flex-1 @3xl:col-span-2 @7xl:col-span-1">{chart}</div>
            </div>
        </div>
    );
}

export function UsageOverview({ summary, windowParams, isLoading, isStale, error, comparisonLabel, isThisMonth, periodPicker, today }: Readonly<UsageOverviewProps>) {
    const savingsQuery = useUsageSavings(windowParams);
    const withSavings = savingsQuery.data !== null;
    let body;
    if (error && !summary) {
        body = <MessageBanner variant="error" message={usageLoadErrorMessage("Couldn't load the usage overview.", error)} />;
    } else if (isLoading || !summary) {
        body = (
            <OverviewGrid
                withSavings={withSavings}
                cards={
                    <>
                        <Skeleton className="h-48" />
                        {withSavings && <Skeleton className="h-48" />}
                    </>
                }
                chart={<Skeleton className="h-48" />}
            />
        );
    } else {
        body = (
            <OverviewGrid
                withSavings={withSavings}
                className={cn("transition-opacity", isStale && "opacity-60")}
                isStale={isStale}
                cards={
                    <>
                        <TotalSpendCard summary={summary} comparisonLabel={comparisonLabel} isThisMonth={isThisMonth} today={today} />
                        {withSavings && <SmartContextSavingsCard savings={savingsQuery.data ?? undefined} error={savingsQuery.error} summary={summary} savingsIsStale={savingsQuery.isPlaceholderData} summaryIsStale={isStale} />}
                    </>
                }
                chart={<TokenCostChart daily={summary.daily} window={summary.window} today={today} showRestOfMonth={isThisMonth && !isStale} />}
            />
        );
    }

    return (
        <section aria-labelledby="usage-overview-heading" className="flex flex-col gap-3">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="flex flex-col gap-0.5">
                    <h2 id="usage-overview-heading" className="font-semibold">
                        Overview
                    </h2>
                    {summary && <UsageFreshness dataAsOf={summary.dataAsOf} historyComplete={summary.historyComplete} className="text-left" />}
                </div>
                {periodPicker}
            </div>
            {body}
        </section>
    );
}
