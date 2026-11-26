// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cn } from "@/utils/cnTailwind";

interface ScoreBadgeProps {
    readonly score: number | undefined | null;
    readonly size?: "sm" | "lg";
    readonly format?: "decimal" | "percent";
}

function scoreColorClass(score: number): string {
    if (score >= 0.75) return "bg-(--success-wMain)/25 text-(--success-wMain)";
    if (score >= 0.5) return "bg-(--warning-wMain)/25 text-(--warning-wMain)";
    return "bg-(--error-wMain)/25 text-(--error-wMain)";
}

export function ScoreBadge({ score, size = "sm", format = "decimal" }: ScoreBadgeProps) {
    if (score === undefined || score === null) {
        return <span className="text-(--secondary-text-wMain)">—</span>;
    }

    const colorClass = scoreColorClass(score);

    const displayValue = format === "percent" ? `${Math.round(score * 100)}%` : score.toFixed(2);

    return <span className={cn("inline-flex items-center rounded-full font-medium", size === "sm" ? "px-2 py-0.5 text-xs" : "px-3 py-1 text-sm", colorClass)}>{displayValue}</span>;
}
