// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";

import { cn } from "@/utils/cnTailwind";

import type { EvaluatorRunStats, ExperimentModelConfig } from "@/types/eval";

import { consistencyLine, createExperimentModelLookup, MODEL_COLORS, scoreColorClass, type TrialConsistency } from "../utils";

import { evaluatorStatsLine } from "./decisionScore";
import { DistributionHistogram } from "./DistributionHistogram";

interface EvaluatorTileProps {
    readonly evaluatorName: string;
    readonly evaluatorSummary?: string;
    readonly scoresByModel: Record<string, (number | undefined | null)[]>;
    readonly allModels: string[];
    readonly experimentModels?: ExperimentModelConfig[];
    readonly stats?: EvaluatorRunStats;
    readonly consistency?: TrialConsistency | null;
}

function average(values: (number | undefined | null)[]): number | null {
    const clean = values.filter((v): v is number => v != null);
    if (!clean.length) return null;
    return clean.reduce((a, b) => a + b, 0) / clean.length;
}

export function EvaluatorTile({ evaluatorName, evaluatorSummary, scoresByModel, allModels, experimentModels, stats, consistency }: EvaluatorTileProps) {
    const { aliasFor, rawModelFor } = createExperimentModelLookup(experimentModels);
    const statsLine = evaluatorStatsLine(stats);

    const modelAverages = useMemo(() => allModels.map(model => ({ model, avg: average(scoresByModel[model] ?? []) })).filter((m): m is { model: string; avg: number } => m.avg !== null), [scoresByModel, allModels]);

    return (
        <div className="rounded-xs border px-5 pt-5 pb-5">
            <div className="gap-6" style={{ display: "grid", gridTemplateColumns: "minmax(0, 600px) minmax(0, 1fr)" }}>
                <div className="flex min-w-0 flex-col gap-6">
                    <div>
                        <div className="text-sm font-semibold">{evaluatorName}</div>
                        {evaluatorSummary && <div className="mt-1 text-xs text-(--secondary-text-wMain)">{evaluatorSummary}</div>}
                        {statsLine && (
                            <div className="mt-1 text-xs text-(--secondary-text-wMain)" data-testid="evaluatorStatsLine">
                                {statsLine}
                            </div>
                        )}
                        {consistency && (
                            <div className={cn("mt-1 text-xs", consistency.inconsistent > 0 ? "text-(--warning-w100)" : "text-(--secondary-text-wMain)")} data-testid="evaluatorConsistencyLine">
                                {consistencyLine(consistency)}
                            </div>
                        )}
                    </div>
                    {modelAverages.length > 0 ? (
                        <div className="flex flex-col gap-2">
                            {modelAverages.map(({ model, avg }) => {
                                const idx = allModels.indexOf(model);
                                const alias = aliasFor(model);
                                const rawModel = rawModelFor(model);
                                return (
                                    <div key={model} className="flex min-w-0 items-center gap-2">
                                        <span className="inline-block size-2 shrink-0 rounded-full" style={{ backgroundColor: MODEL_COLORS[idx % MODEL_COLORS.length] }} />
                                        <span className="shrink-0 text-sm font-medium text-(--primary-text-wMain)">{alias}</span>
                                        {rawModel && rawModel !== alias && <span className="truncate text-sm text-(--secondary-text-wMain)">{rawModel}</span>}
                                        <span className={cn("ml-auto shrink-0 pl-4 text-sm font-semibold", scoreColorClass(avg))}>{Math.round(avg * 100)}%</span>
                                    </div>
                                );
                            })}
                        </div>
                    ) : (
                        <div className="text-xs text-(--secondary-text-wMain)">No scores yet</div>
                    )}
                </div>
                <div className="flex min-h-0 min-w-0 flex-col gap-1 border-l pl-6">
                    <div className="text-sm text-(--secondary-text-wMain)">Distribution of Scores</div>
                    <DistributionHistogram scoresByModel={scoresByModel} allModels={allModels} experimentModels={experimentModels} />
                </div>
            </div>
        </div>
    );
}
