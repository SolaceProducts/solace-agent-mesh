// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { TooltipContentProps } from "recharts";
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { SeriesChartRow } from "../seriesChartData";
import { MODEL_COLORS } from "../utils";

function ScoreTooltip({ active, payload, label }: TooltipContentProps) {
    if (!active || !payload || payload.length === 0) return null;
    const tooltipLabel = (payload[0]?.payload as SeriesChartRow | undefined)?.tooltipLabel ?? String(label);
    return (
        <div className="rounded-xs border border-(--darkSurface-border) bg-(--darkSurface-bg) px-3 py-2 text-xs text-(--darkSurface-text) shadow-md">
            <div className="mb-1.5 text-(--darkSurface-textMuted)">{tooltipLabel}</div>
            <div className="flex flex-col gap-1.5">
                {payload.map((entry, idx) => {
                    const name = String(entry.name ?? "");
                    const value = entry.value as number | null | undefined;
                    return (
                        <div key={`${name}-${idx}`} className="flex items-center gap-2">
                            <span className="inline-block size-2 shrink-0 rounded-full" style={{ backgroundColor: entry.color as string }} />
                            <span className="flex-1 font-medium">{name}</span>
                            <span className="font-mono">{typeof value === "number" ? `${Math.round(value * 100)}%` : "—"}</span>
                        </div>
                    );
                })}
            </div>
        </div>
    );
}

interface ScoreLineChartProps {
    readonly data: readonly SeriesChartRow[];
    readonly seriesKeys: readonly string[];
    readonly height: number;
    readonly colorOf?: (seriesKey: string, idx: number) => string;
    readonly seriesNameOf?: (seriesKey: string) => string;
}

export function ScoreLineChart({ data, seriesKeys, height, colorOf = (_, idx) => MODEL_COLORS[idx % MODEL_COLORS.length], seriesNameOf = key => key }: ScoreLineChartProps) {
    return (
        <ResponsiveContainer width="100%" height={height}>
            <LineChart data={data as SeriesChartRow[]} margin={{ top: 4, right: 8, bottom: 0, left: -16 }}>
                <CartesianGrid strokeDasharray="3 3" stroke="var(--secondary-w10)" vertical={false} />
                <XAxis dataKey="label" tick={{ fontSize: 11, fill: "var(--secondary-text-wMain)" }} tickLine={false} axisLine={false} dy={8} />
                <YAxis domain={[0, 1]} ticks={[0, 0.25, 0.5, 0.75, 1]} interval={0} tickFormatter={v => `${Math.round((v as number) * 100)}%`} tick={{ fontSize: 11, fill: "var(--secondary-text-wMain)" }} tickLine={false} axisLine={false} dx={-4} />
                <Tooltip cursor={{ stroke: "var(--secondary-w20)", strokeWidth: 1 }} content={ScoreTooltip} />
                {seriesKeys.map((key, idx) => (
                    <Line
                        key={key}
                        type="monotone"
                        dataKey={key}
                        name={seriesNameOf(key)}
                        stroke={colorOf(key, idx)}
                        strokeWidth={2}
                        dot={{ r: 3, strokeWidth: 2, fill: "var(--background-wMain)" }}
                        activeDot={{ r: 5, strokeWidth: 0 }}
                        connectNulls={true}
                    />
                ))}
            </LineChart>
        </ResponsiveContainer>
    );
}
