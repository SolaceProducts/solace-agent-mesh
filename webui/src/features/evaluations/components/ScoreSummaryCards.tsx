// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cn } from "@/utils/cnTailwind";

import type { EvalRun } from "@/types/eval";

import { ScoreBadge } from "./ScoreBadge";

interface ScoreSummaryCardsProps {
    readonly runs: EvalRun[];
    readonly evaluatorNames: string[];
    readonly primaryEvaluatorName?: string;
    readonly evaluatorNameMap?: Map<string, string>;
}

interface EvalStats {
    avg: number;
    passCount: number;
    total: number;
    min: number;
    max: number;
}

function computeStats(runs: EvalRun[], evaluatorKey: string): EvalStats | null {
    const scores = runs.map(r => r.summaryScores?.[evaluatorKey]).filter((s): s is number => s !== undefined && s !== null);
    if (scores.length === 0) return null;
    const avg = scores.reduce((a, b) => a + b, 0) / scores.length;
    return {
        avg,
        passCount: scores.filter(s => s >= 0.8).length,
        total: scores.length,
        min: Math.min(...scores),
        max: Math.max(...scores),
    };
}

function StatCard({ label, stats, isPrimary }: { readonly label: string; readonly stats: EvalStats | null; readonly isPrimary?: boolean }) {
    return (
        <div className={cn("flex flex-col gap-2 rounded-sm border p-4", isPrimary && "border-(--brand-w100) ring-1 ring-(--brand-w100)/20")}>
            <div className="flex items-center gap-2">
                <span className="text-sm font-semibold text-(--secondary-text-wMain)">{label}</span>
                {isPrimary && <span className="rounded-full bg-(--brand-w100)/10 px-1.5 py-0.5 text-xs font-medium text-(--brand-w100)">Primary</span>}
            </div>
            {stats ? (
                <>
                    <div>
                        <ScoreBadge score={stats.avg} size="lg" format="percent" />
                    </div>
                    <div className="text-xs text-(--secondary-text-wMain)">
                        {stats.passCount}/{stats.total} passed
                    </div>
                    <div className="text-xs text-(--secondary-text-wMain)">
                        Range: {stats.min.toFixed(2)} – {stats.max.toFixed(2)}
                    </div>
                </>
            ) : (
                <div className="text-sm text-(--secondary-text-wMain)">No data</div>
            )}
        </div>
    );
}

export function ScoreSummaryCards({ runs, evaluatorNames, primaryEvaluatorName, evaluatorNameMap }: ScoreSummaryCardsProps) {
    if (evaluatorNames.length === 0) return null;

    return (
        <div className="grid gap-4" style={{ gridTemplateColumns: `repeat(${Math.min(evaluatorNames.length, 4)}, minmax(0, 1fr))` }}>
            {evaluatorNames.map(key => (
                <StatCard key={key} label={evaluatorNameMap?.get(key) ?? key} stats={computeStats(runs, key)} isPrimary={key === primaryEvaluatorName} />
            ))}
        </div>
    );
}
