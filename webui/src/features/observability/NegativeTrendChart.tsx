// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { type ReactNode, useCallback, useMemo, useState } from "react";
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, type TooltipContentProps, XAxis, YAxis } from "recharts";

import type { FeedbackSubjectRef, FeedbackSummaryRow, TrendBucketGranularity } from "@/api/feedback";

import { Tabs, TabsList, TabsTrigger } from "@/ui/tabs";

import { cn } from "@/utils";

import { formatCount, formatNegativeRatio } from "./feedbackFormat";
import { SUBJECT_TYPE_ICONS } from "./feedbackLabels";
import { formatBucketLabel } from "./trendBucketLabel";
import { buildTrendRows, buildTrendSeries, type TrendChartRow, type TrendMode, type TrendSeries } from "./trendChartData";

const CHART_HEIGHT = 240;

const CHART_RIGHT_MARGIN = 12;
const HOVER_BAND_STROKE_WIDTH = 16;
const RATIO_AXIS_FLOOR = 0.002;

function ratioDomainMax(dataMax: number): number {
    return Math.max(dataMax * 1.2, RATIO_AXIS_FLOOR);
}

function ratioTickDecimals(dataMax: number): number {
    const stepPercent = (ratioDomainMax(dataMax) * 100) / 4;
    if (stepPercent <= 0) return 0;
    return Math.max(0, Math.ceil(-Math.log10(stepPercent)));
}

function resolveEmptyMessage(isLoading: boolean | undefined, filteredSubject: NegativeTrendChartProps["filteredSubject"]): string {
    if (isLoading) return "Loading trend…";
    if (filteredSubject) return "No feedback recorded for this agent in the selected window.";
    return "No feedback recorded yet. This populates once end users start rating agent responses.";
}

function maxSeriesValue(data: readonly TrendChartRow[], series: readonly TrendSeries[]): number {
    let max = 0;
    data.forEach(row => {
        series.forEach(s => {
            const value = row[s.key];
            if (typeof value === "number" && value > max) max = value;
        });
    });
    return max;
}

function formatAxisTick(value: number, mode: TrendMode, ratioDecimals: number): string {
    if (mode === "count") return formatCount(value);
    return `${(value * 100).toFixed(ratioDecimals)}%`;
}

interface NegativeTrendChartProps {
    rows?: readonly FeedbackSummaryRow[];
    isLoading?: boolean;
    isFetching?: boolean;
    error?: Error | null;
    filteredSubject?: { label: string; kind?: "agent" | "workflow" };
    granularity: TrendBucketGranularity;
    onSelectSubject?: (subject: FeedbackSubjectRef) => void;
}

export function TrendTooltip({
    active,
    payload,
    label,
    series,
    rows,
    granularity,
    hoveredKey,
}: Pick<TooltipContentProps, "active" | "payload" | "label"> & { series: readonly TrendSeries[]; rows: readonly FeedbackSummaryRow[]; granularity: TrendBucketGranularity; hoveredKey?: string | null }) {
    if (!active || !payload?.length || !hoveredKey) return null;

    if (!payload.some(entry => String(entry.dataKey) === hoveredKey)) return null;

    const meta = series.find(s => s.key === hoveredKey);
    const point = rows.find(r => r.subject?.id === hoveredKey)?.trend.find(p => p.date === label);
    if (!meta || !point) return null;

    return (
        <div className="rounded-sm border bg-(--background-w10) p-3 text-sm shadow-md">
            <div className="mb-2 font-semibold text-(--primary-text-wMain)">{formatBucketLabel(String(label), granularity)}</div>
            <div className="flex items-center gap-2">
                <span className="inline-block size-2 shrink-0 rounded-full" style={{ backgroundColor: meta.color }} />
                <span className="text-(--primary-text-wMain)">{meta.name}</span>
            </div>
            <div className="mt-1 pl-4 text-(--secondary-text-wMain)">
                {formatNegativeRatio(point.negativeRatio)} — {formatCount(point.negativeFeedback)} negative of {formatCount(point.totalActivities)} activities
            </div>
        </div>
    );
}

function TrendLegend({ series, subjectOf, onSelectSubject }: Readonly<{ series: readonly TrendSeries[]; subjectOf: (key: string) => FeedbackSubjectRef | undefined; onSelectSubject?: (subject: FeedbackSubjectRef) => void }>) {
    return (
        <div className="ml-[56px] flex flex-wrap gap-x-6 gap-y-1.5 text-sm text-(--secondary-text-wMain)">
            {series.map(s => {
                const swatch = <span className="inline-block size-2.5 shrink-0 rounded-xs" style={{ backgroundColor: s.color }} />;
                const subject = subjectOf(s.key);
                if (!onSelectSubject || !subject) {
                    return (
                        <span key={s.key} className="flex items-center gap-2">
                            {swatch}
                            {s.name}
                        </span>
                    );
                }
                return (
                    <button
                        key={s.key}
                        type="button"
                        className="flex cursor-pointer items-center gap-2 rounded-xs hover:text-(--primary-text-wMain) hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-(--primary-wMain)"
                        onClick={() => onSelectSubject(subject)}
                    >
                        {swatch}
                        {s.name}
                    </button>
                );
            })}
        </div>
    );
}

export function NegativeTrendChart({ rows, isLoading, isFetching, error, filteredSubject, granularity, onSelectSubject }: Readonly<NegativeTrendChartProps>) {
    const [mode, setMode] = useState<TrendMode>("ratio");
    const [hoveredKey, setHoveredKey] = useState<string | null>(null);

    const safeRows = useMemo(() => rows ?? [], [rows]);
    const subjectOf = useCallback((key: string) => safeRows.find(r => r.subject?.id === key)?.subject, [safeRows]);
    const selectSeries = useCallback(
        (key: string) => {
            const subject = subjectOf(key);
            if (subject) onSelectSubject?.(subject);
        },
        [subjectOf, onSelectSubject]
    );
    const series = buildTrendSeries(safeRows);
    const data = buildTrendRows(safeRows, mode);
    const hasSeries = series.length > 0 && data.length > 0;
    const ratioDecimals = mode === "ratio" ? ratioTickDecimals(maxSeriesValue(data, series)) : 0;

    const emptyMessage = resolveEmptyMessage(isLoading, filteredSubject);

    const title = filteredSubject?.label ?? "Negative Trend (Top 5)";
    const TitleIcon = filteredSubject?.kind ? SUBJECT_TYPE_ICONS[filteredSubject.kind] : undefined;
    const rankingNote = "Ranked by negative ratio, not volume — may not be the tenant's highest-volume sources.";
    const rankedSubtitle = onSelectSubject ? `${rankingNote} Select a line to filter the dashboard to it.` : rankingNote;
    const subtitle = filteredSubject ? "Negative feedback ratio over the selected window." : rankedSubtitle;

    const renderTooltip = useCallback((props: TooltipContentProps) => <TrendTooltip {...props} series={series} rows={safeRows} granularity={granularity} hoveredKey={hoveredKey} />, [series, safeRows, granularity, hoveredKey]);

    let body: ReactNode;
    if (error) {
        body = <p className="text-sm text-(--error-wMain)">Couldn&apos;t load the trend: {error.message}</p>;
    } else if (hasSeries) {
        body = (
            <>
                <div className={cn("transition-opacity", isFetching && "opacity-60")} aria-busy={isFetching}>
                    <ResponsiveContainer width="100%" height={CHART_HEIGHT}>
                        <LineChart data={data} margin={{ top: 8, right: CHART_RIGHT_MARGIN, bottom: 0, left: -8 }}>
                            <CartesianGrid strokeDasharray="3 3" stroke="var(--secondary-w10)" vertical={false} />
                            <XAxis dataKey="date" tickFormatter={value => formatBucketLabel(String(value), granularity)} tick={{ fontSize: 11, fill: "var(--secondary-text-wMain)" }} tickLine={false} axisLine={false} dy={8} minTickGap={24} />
                            <YAxis
                                tick={{ fontSize: 11, fill: "var(--secondary-text-wMain)" }}
                                tickLine={false}
                                axisLine={false}
                                width={64}
                                domain={mode === "ratio" ? [0, (dataMax: number) => ratioDomainMax(dataMax)] : undefined}
                                allowDecimals={mode !== "count"}
                                tickFormatter={value => formatAxisTick(value as number, mode, ratioDecimals)}
                            />
                            <Tooltip cursor={hoveredKey ? { stroke: "var(--secondary-w20)", strokeWidth: 1 } : false} content={renderTooltip} isAnimationActive={false} />
                            {series.map(s => {
                                const isHovered = hoveredKey === s.key;
                                const isDimmed = hoveredKey !== null && !isHovered;
                                return (
                                    <Line
                                        key={s.key}
                                        type="linear"
                                        dataKey={s.key}
                                        name={s.name}
                                        stroke={s.color}
                                        strokeWidth={isHovered ? 3 : 2}
                                        strokeOpacity={isDimmed ? 0.25 : 1}
                                        dot={{ r: 2, strokeWidth: 0, fill: s.color, fillOpacity: isDimmed ? 0.25 : 1, onMouseEnter: () => setHoveredKey(s.key), onMouseLeave: () => setHoveredKey(null) }}
                                        activeDot={false}
                                        connectNulls={false}
                                        isAnimationActive={false}
                                        style={onSelectSubject ? { cursor: "pointer" } : undefined}
                                        onClick={() => selectSeries(s.key)}
                                        onMouseEnter={() => setHoveredKey(s.key)}
                                        onMouseLeave={() => setHoveredKey(null)}
                                    />
                                );
                            })}
                            {series.map(s => (
                                <Line
                                    key={`${s.key}-hover-band`}
                                    type="linear"
                                    dataKey={s.key}
                                    stroke="transparent"
                                    strokeWidth={HOVER_BAND_STROKE_WIDTH}
                                    dot={false}
                                    activeDot={false}
                                    connectNulls={false}
                                    isAnimationActive={false}
                                    legendType="none"
                                    tooltipType="none"
                                    style={onSelectSubject ? { cursor: "pointer" } : undefined}
                                    onClick={() => selectSeries(s.key)}
                                    onMouseEnter={() => setHoveredKey(s.key)}
                                    onMouseLeave={() => setHoveredKey(null)}
                                />
                            ))}
                        </LineChart>
                    </ResponsiveContainer>
                </div>
                <TrendLegend series={series} subjectOf={subjectOf} onSelectSubject={onSelectSubject} />
            </>
        );
    } else {
        body = <p className="text-sm text-(--secondary-text-wMain)">{emptyMessage}</p>;
    }

    return (
        <section className="flex flex-col gap-3 rounded-sm border p-4" aria-label={title}>
            <header className="flex flex-wrap items-start gap-3">
                <div className="flex flex-col gap-0.5">
                    <h2 className="flex items-center gap-1.5 text-sm font-semibold text-(--secondary-text-wMain)">
                        {TitleIcon && <TitleIcon className="h-4 w-4 shrink-0" />}
                        {title}
                    </h2>
                    <p className="text-xs text-(--secondary-text-wMain)">{subtitle}</p>
                </div>
                <Tabs className="ml-auto" value={mode} onValueChange={value => setMode(value as TrendMode)}>
                    <TabsList>
                        <TabsTrigger value="ratio" aria-label="Ratio" className="py-1.5 text-sm">
                            Ratio
                        </TabsTrigger>
                        <TabsTrigger value="count" aria-label="Count" className="py-1.5 text-sm">
                            Count
                        </TabsTrigger>
                    </TabsList>
                </Tabs>
            </header>

            {body}
        </section>
    );
}
