// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { cn } from "@/utils/cnTailwind";

import type { EvaluatorRunStats, ExperimentModelConfig } from "@/types/eval";

import { MODEL_COLORS, scoreColorClass } from "../utils";

import { evaluatorStatsLine } from "./decisionScore";

interface EvaluatorResultCardProps {
    readonly evaluatorName: string;
    readonly summary?: string;
    readonly models: ExperimentModelConfig[];
    readonly scoresByModel: Record<string, number | undefined>;
    readonly modelColorIndex: Map<string, number>;
    readonly stats?: EvaluatorRunStats;
}

export function EvaluatorResultCard({ evaluatorName, summary, models, scoresByModel, modelColorIndex, stats }: EvaluatorResultCardProps) {
    const statsLine = evaluatorStatsLine(stats);
    return (
        <div className="flex flex-col gap-3 rounded-xs border p-4">
            <div className="flex flex-col gap-1">
                <h4 className="text-sm font-medium text-(--primary-text-wMain)">{evaluatorName}</h4>
                {summary && <p className="text-sm text-(--secondary-text-wMain)">{summary}</p>}
                {statsLine && (
                    <p className="text-xs text-(--secondary-text-wMain)" data-testid="evaluatorStatsLine">
                        {statsLine}
                    </p>
                )}
            </div>
            <div className="flex flex-col gap-1.5">
                {models.map(m => {
                    const colorIdx = (m.modelConfigId ? modelColorIndex.get(m.modelConfigId) : undefined) ?? 0;
                    const score = m.modelConfigId ? scoresByModel[m.modelConfigId] : undefined;
                    const hasScore = score != null && !Number.isNaN(score);
                    return (
                        <div key={m.modelConfigId ?? m.alias} className="flex min-w-0 items-center gap-2">
                            <span className="size-2 shrink-0 rounded-full" style={{ backgroundColor: MODEL_COLORS[colorIdx % MODEL_COLORS.length] }} />
                            <span className="shrink-0 text-sm text-(--primary-text-wMain)">{m.alias}</span>
                            <span className="truncate text-sm text-(--secondary-text-wMain)">{m.model}</span>
                            <span className={cn("shrink-0 pl-4 text-sm font-medium", hasScore ? scoreColorClass(score) : "text-(--secondary-text-wMain)")}>{hasScore ? `${Math.round(score * 100)}%` : "—"}</span>
                        </div>
                    );
                })}
            </div>
        </div>
    );
}
