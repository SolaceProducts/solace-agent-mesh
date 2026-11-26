// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useMemo, useState } from "react";
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/ui/select";
import type { EvalRun } from "@/types/eval";

import { MODEL_COLORS } from "../utils";

interface ScoreProgressChartProps {
    readonly runs: EvalRun[];
    readonly evaluatorNames: string[];
    readonly allModels?: string[];
    readonly title?: string;
    readonly hideHeader?: boolean;
}

export interface ProgressChartPoint {
    date: string;
    [model: string]: number | null | string;
}

export function buildProgressChartData(runs: EvalRun[], selectedMetric: string): { data: ProgressChartPoint[]; models: string[] } {
    const terminal = runs
        .filter(r => (r.status === "completed" || r.status === "completed_with_warnings") && r.summaryScores && r.summaryScores[selectedMetric] !== undefined)
        .sort((a, b) => new Date(a.startedAt).getTime() - new Date(b.startedAt).getTime());

    const uniqueModels = Array.from(new Set(terminal.map(r => r.model)));

    const groupStarts = new Map<string, string>();
    for (const r of terminal) {
        const existing = groupStarts.get(r.runGroupId);
        if (!existing || new Date(r.startedAt) < new Date(existing)) {
            groupStarts.set(r.runGroupId, r.startedAt);
        }
    }

    const dateCounts = new Map<string, number>();
    for (const startedTime of groupStarts.values()) {
        const day = new Date(startedTime).toLocaleDateString(undefined, { month: "short", day: "numeric" });
        dateCounts.set(day, (dateCounts.get(day) ?? 0) + 1);
    }

    const orderedGroupIds = Array.from(groupStarts.entries())
        .sort((a, b) => new Date(a[1]).getTime() - new Date(b[1]).getTime())
        .map(([gid]) => gid);

    const pointByGroup = new Map<string, ProgressChartPoint>();
    for (const gid of orderedGroupIds) {
        const startedTime = groupStarts.get(gid)!;
        const day = new Date(startedTime).toLocaleDateString(undefined, { month: "short", day: "numeric" });
        const label = (dateCounts.get(day) ?? 1) > 1 ? new Date(startedTime).toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" }) : day;
        const point: ProgressChartPoint = { date: label };
        for (const m of uniqueModels) point[m] = null;
        pointByGroup.set(gid, point);
    }

    for (const r of terminal) {
        const point = pointByGroup.get(r.runGroupId);
        if (!point) continue;
        point[r.model] = r.summaryScores![selectedMetric];
    }

    return {
        data: orderedGroupIds.map(gid => pointByGroup.get(gid)!),
        models: uniqueModels,
    };
}

export function ScoreProgressChart({ runs, evaluatorNames, allModels, title, hideHeader }: ScoreProgressChartProps) {
    const [selectedMetric, setSelectedMetric] = useState<string>(evaluatorNames[0] ?? "");

    const { data, models } = useMemo(() => buildProgressChartData(runs, selectedMetric), [runs, selectedMetric]);

    if (data.length === 0) return null;

    return (
        <div className="rounded-xs border p-4">
            {!hideHeader && (
                <div className="mb-4 flex items-center justify-between">
                    <div className="flex items-center gap-4">
                        <div className="text-sm font-semibold">{title ?? "Model Performance History"}</div>
                        {evaluatorNames.length > 1 && (
                            <Select value={selectedMetric} onValueChange={setSelectedMetric}>
                                <SelectTrigger className="h-7 w-auto min-w-[140px] text-xs">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    {evaluatorNames.map(name => (
                                        <SelectItem key={name} value={name}>
                                            {name}
                                        </SelectItem>
                                    ))}
                                </SelectContent>
                            </Select>
                        )}
                    </div>
                    <div className="flex flex-wrap gap-3 text-xs text-(--secondary-text-wMain)">
                        {models.map(model => {
                            const colorIdx = (allModels ?? models).indexOf(model);
                            return (
                                <span key={model} className="flex items-center gap-1 font-mono">
                                    <span className="inline-block size-2 rounded-full" style={{ backgroundColor: MODEL_COLORS[colorIdx % MODEL_COLORS.length] }} />
                                    {model}
                                </span>
                            );
                        })}
                    </div>
                </div>
            )}
            <ResponsiveContainer width="100%" height={220}>
                <LineChart data={data} margin={{ top: 4, right: 8, bottom: 0, left: -16 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke="var(--secondary-w10)" vertical={false} />
                    <XAxis dataKey="date" tick={{ fontSize: 11, fill: "var(--secondary-text-wMain)" }} tickLine={false} axisLine={false} dy={8} />
                    <YAxis domain={[0, 1]} tickFormatter={v => `${Math.round((v as number) * 100)}%`} tick={{ fontSize: 11, fill: "var(--secondary-text-wMain)" }} tickLine={false} axisLine={false} dx={-4} />
                    <Tooltip
                        formatter={(value, name) => [
                            <span key="val" className="font-mono">{`${Math.round(Number(value) * 100)}%`}</span>,
                            <span key="name" className="font-mono">
                                {name}
                            </span>,
                        ]}
                        labelStyle={{ color: "var(--secondary-text-wMain)", fontSize: 11, marginBottom: 4 }}
                        contentStyle={{
                            backgroundColor: "var(--background-wMain)",
                            border: "1px solid var(--secondary-w10)",
                            borderRadius: "4px",
                            fontSize: 12,
                            boxShadow: "0 4px 6px -1px rgb(0 0 0 / 0.1)",
                        }}
                    />
                    {models.map(model => {
                        const colorIdx = (allModels ?? models).indexOf(model);
                        return (
                            <Line
                                key={model}
                                type="monotone"
                                dataKey={model}
                                name={model}
                                stroke={MODEL_COLORS[colorIdx % MODEL_COLORS.length]}
                                strokeWidth={2}
                                dot={{ r: 3, strokeWidth: 2, fill: "var(--background-wMain)" }}
                                activeDot={{ r: 5, strokeWidth: 0 }}
                                connectNulls={true}
                            />
                        );
                    })}
                </LineChart>
            </ResponsiveContainer>
        </div>
    );
}
