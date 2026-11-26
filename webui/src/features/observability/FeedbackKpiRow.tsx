// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { FeedbackSummaryRow } from "@/api/feedback";

import { HelpPopover } from "@/common";
import { cn } from "@/utils";

import { formatCount, formatNegativeRatio } from "./feedbackFormat";

interface FeedbackKpiRowProps {
    row?: FeedbackSummaryRow;
    isLoading?: boolean;
    isFetching?: boolean;
    error?: Error | null;
}

const NO_VALUE = "—";

function KpiCard({ label, value, sub, help }: Readonly<{ label: string; value: React.ReactNode; sub?: React.ReactNode; help?: React.ReactNode }>) {
    return (
        <div className="flex flex-col gap-2 rounded-sm border p-4">
            <div className="flex items-center gap-1">
                <span className="text-sm font-medium text-(--secondary-text-wMain)">{label}</span>
                {help && <HelpPopover content={help} size="16" />}
            </div>
            <div className="text-2xl font-semibold">{value}</div>
            {sub && <div className="text-xs text-(--secondary-text-wMain)">{sub}</div>}
        </div>
    );
}

export function FeedbackKpiRow({ row, isLoading, isFetching, error }: Readonly<FeedbackKpiRowProps>) {
    if (error) {
        return <div className="rounded-sm border p-4 text-sm text-(--error-wMain)">Couldn't load feedback summary: {error.message}</div>;
    }

    const hasData = row !== undefined && (row.totalActivities > 0 || row.positiveFeedback > 0 || row.negativeFeedback > 0);
    const emptySub = isLoading ? undefined : "No data yet";
    const gridClassName = cn("grid gap-4 sm:grid-cols-3", isFetching && "opacity-60");

    if (!hasData) {
        return (
            <div className={gridClassName} aria-busy={isFetching}>
                <KpiCard label="Total Activities" value={NO_VALUE} sub={emptySub} />
                <KpiCard label="Positive Feedback" value={NO_VALUE} sub={emptySub} />
                <KpiCard label="Negative Feedback" value={NO_VALUE} sub={emptySub} />
            </div>
        );
    }

    return (
        <div className={gridClassName} aria-busy={isFetching}>
            <KpiCard
                label="Total Activities"
                value={formatCount(row.totalActivities)}
                help="Every completed run for the selected agents and window — not positive plus negative. Most activity is never rated, so this is a much larger number than the rated counts beside it."
            />
            <KpiCard label="Positive Feedback" value={formatCount(row.positiveFeedback)} />
            <KpiCard
                label="Negative Feedback"
                value={
                    <span className="flex items-baseline gap-2">
                        {formatCount(row.negativeFeedback)}
                        <span className="text-sm font-medium text-(--secondary-text-wMain)">({formatNegativeRatio(row.negativeRatio)})</span>
                    </span>
                }
                help={`Share of all activity rated negative — out of ${formatCount(row.totalActivities)} total, not out of ${formatCount(row.positiveFeedback + row.negativeFeedback)} rated.`}
            />
        </div>
    );
}
