// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo } from "react";
import { Bar, BarChart, ResponsiveContainer, Tooltip, XAxis } from "recharts";

import type { ExperimentModelConfig } from "@/types/eval";

import { createExperimentModelLookup, MODEL_COLORS } from "../utils";

interface RichTooltipPayloadEntry {
    name: string;
    value: number;
    color: string;
}

interface RichTooltipProps {
    active?: boolean;
    payload?: RichTooltipPayloadEntry[];
    aliasFor: (model: string) => string;
    rawModelFor: (model: string) => string | undefined;
}

function RichTooltip({ active, payload, aliasFor, rawModelFor }: RichTooltipProps) {
    if (!active || !payload || payload.length === 0) return null;
    return (
        <div className="rounded-xs border bg-(--background-w10) px-3 py-2 text-xs shadow-md">
            <div className="flex flex-col gap-1.5">
                {payload.map(entry => {
                    const model = String(entry.name);
                    const alias = aliasFor(model);
                    const rawModel = rawModelFor(model);
                    return (
                        <div key={model} className="flex items-center gap-2">
                            <span className="inline-block size-2 shrink-0 rounded-full" style={{ backgroundColor: entry.color }} />
                            <span className="font-medium text-(--primary-text-wMain)">{alias}</span>
                            {rawModel && rawModel !== alias && <span className="text-(--secondary-text-wMain)">{rawModel}</span>}
                        </div>
                    );
                })}
            </div>
        </div>
    );
}

interface DistributionHistogramProps {
    readonly scoresByModel: Record<string, (number | undefined | null)[]>;
    readonly allModels: string[];
    readonly experimentModels?: ExperimentModelConfig[];
    readonly emptyLabel?: string;
}

export const BUCKETS = [
    { key: "b1", range: [0, 0.2], label: "0–19%" },
    { key: "b2", range: [0.2, 0.4], label: "20–39%" },
    { key: "b3", range: [0.4, 0.6], label: "40–59%" },
    { key: "b4", range: [0.6, 0.8], label: "60–79%" },
    { key: "b5", range: [0.8, 1.0001], label: "80–100%" },
] as const;

export function bucketIndex(score: number): number {
    for (let i = 0; i < BUCKETS.length; i++) {
        const [lo, hi] = BUCKETS[i].range;
        if (score >= lo && score < hi) return i;
    }
    return BUCKETS.length - 1;
}

export function DistributionHistogram({ scoresByModel, allModels, experimentModels, emptyLabel }: DistributionHistogramProps) {
    const { aliasFor, rawModelFor } = createExperimentModelLookup(experimentModels);

    const { chartData, modelsWithScores } = useMemo(() => {
        const models = allModels.filter(m => scoresByModel[m]?.some(v => v != null) ?? false);
        const rows = BUCKETS.map(b => ({ bucket: b.label }) as Record<string, number | string>);
        for (const model of models) {
            const scores = scoresByModel[model] ?? [];
            const counts = [0, 0, 0, 0, 0];
            for (const s of scores) {
                if (s == null) continue;
                counts[bucketIndex(s)]++;
            }
            counts.forEach((c, i) => {
                rows[i][model] = c;
            });
        }
        return { chartData: rows, modelsWithScores: models };
    }, [scoresByModel, allModels]);

    if (modelsWithScores.length === 0) {
        return (
            <div data-testid="evalDistributionChart" data-empty="true" className="flex h-[140px] items-center justify-center rounded-xs border border-dashed text-xs text-(--secondary-text-wMain)">
                {emptyLabel ?? "Come back to see the distribution of scores after the experiment is completed"}
            </div>
        );
    }

    return (
        <div data-testid="evalDistributionChart" className="w-full [&_.recharts-bar-rectangle]:cursor-pointer">
            <ResponsiveContainer width="100%" height={132}>
                <BarChart data={chartData} margin={{ top: 8, right: 0, bottom: -12, left: 0 }} barCategoryGap="10%" barGap={4}>
                    <XAxis dataKey="bucket" tick={{ fontSize: 11, fill: "var(--secondary-text-wMain)" }} tickLine={false} axisLine={{ stroke: "var(--secondary-w20)" }} interval={0} />
                    <Tooltip cursor={{ fill: "transparent" }} content={<RichTooltip aliasFor={aliasFor} rawModelFor={rawModelFor} />} />
                    {modelsWithScores.map(model => {
                        const idx = allModels.indexOf(model);
                        return <Bar key={model} dataKey={model} fill={MODEL_COLORS[idx % MODEL_COLORS.length]} minPointSize={2} />;
                    })}
                </BarChart>
            </ResponsiveContainer>
        </div>
    );
}
