// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";
import { Link } from "react-router-dom";

import { cn } from "@/utils/cnTailwind";

import type { WatchlistAgentChart as WatchlistAgentChartType } from "@/types/eval";

import { useAgentOptions } from "@/features/entrypoints/hooks";

import { buildSeriesChartData, type SeriesChartInputPoint } from "../seriesChartData";
import { MODEL_COLORS } from "../utils";

import { ScoreLineChart } from "./ScoreLineChart";
import { TargetTypeIcon } from "./TargetTypeIcon";

interface WatchlistAgentChartProps {
    readonly agent: WatchlistAgentChartType;
    readonly fullWidth?: boolean;
}

export function WatchlistAgentChart({ agent, fullWidth }: WatchlistAgentChartProps) {
    const allOptions = useAgentOptions();

    const resolvedOption = useMemo(() => allOptions.find(o => o.name === agent.agentName), [allOptions, agent.agentName]);

    const { data, seriesKeys } = useMemo(() => {
        const flatPoints: SeriesChartInputPoint[] = agent.experiments.flatMap(exp =>
            exp.points.map(p => ({
                groupId: p.runGroupId,
                startedTime: p.startedAt,
                seriesKey: exp.experimentName,
                score: p.score,
            }))
        );
        return buildSeriesChartData(
            flatPoints,
            agent.experiments.map(e => e.experimentName)
        );
    }, [agent]);

    const evaluatorByExperimentName = useMemo(() => {
        const m = new Map<string, string | null | undefined>();
        for (const exp of agent.experiments) {
            m.set(exp.experimentName, exp.primaryEvaluatorName);
        }
        return m;
    }, [agent.experiments]);

    const isWorkflow = agent.targetType === "workflow";
    const displayName = resolvedOption?.displayName ?? agent.agentName;
    const linkTargetId = agent.isBuiltIn || isWorkflow ? undefined : resolvedOption?.agentId;

    const headerName = linkTargetId ? (
        <Link to={`/agent-mesh/${linkTargetId}`} className="text-sm font-bold text-(--primary-wMain) hover:underline">
            {displayName}
        </Link>
    ) : (
        <span className="text-sm font-medium">{displayName}</span>
    );

    const chartHeight = fullWidth ? 200 : 130;

    return (
        <div className={cn("flex flex-col gap-3 rounded-xs border border-(--secondary-w20) p-4")}>
            <div className="flex items-start justify-between gap-4">
                <div className="flex min-w-0 items-center gap-2">
                    {agent.targetType && <TargetTypeIcon targetType={agent.targetType} className="size-4" />}
                    <span className="truncate">{headerName}</span>
                </div>
                <div className="flex flex-wrap justify-end gap-3 text-xs text-(--secondary-text-wMain)">
                    {seriesKeys.map((name, idx) => {
                        const evaluatorName = evaluatorByExperimentName.get(name);
                        return (
                            <span key={name} className="flex items-center gap-1.5">
                                <span className="inline-block size-3 rounded-full" style={{ backgroundColor: MODEL_COLORS[idx % MODEL_COLORS.length] }} />
                                <span className="truncate">
                                    {name}
                                    {evaluatorName && <span className="text-(--secondary-text-wMain)/70">{` (${evaluatorName})`}</span>}
                                </span>
                            </span>
                        );
                    })}
                </div>
            </div>
            {data.length === 0 || seriesKeys.length === 0 ? (
                <div className="flex items-center justify-center text-xs text-(--secondary-text-wMain)" style={{ height: chartHeight }}>
                    No experiments yet for this model
                </div>
            ) : (
                <ScoreLineChart data={data} seriesKeys={seriesKeys} height={chartHeight} />
            )}
        </div>
    );
}
