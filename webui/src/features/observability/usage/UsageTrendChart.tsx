// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useId, useMemo } from "react";
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { UsageDay, UsageWindow } from "@/api/usage";

import { ChartDataTable, formatAxisDollars, PARTIAL_PERIOD_OPACITY } from "@/common/usage";

import { TOOLTIP_SURFACE } from "@/ui/tooltip-surface";

import { formatUsageMoney, isUsageMoney, usageMoneyToNumber } from "@/utils";

import { formatCount } from "../feedbackFormat";

import { CostAndTokens } from "./CostAndTokens";
import { DayAxisTick, DayLineCursor } from "./usageChartDays";
import { daysOfWindow, withRestOfMonth } from "./usageDays";
import { formatTooltipDay, formatUsageDay } from "./usageFormat";
import { formatWeekRange, isPartialWeek, partialWeekNote, type UsageWeek, WEEKLY_AFTER_DAYS, weeklySpend } from "./usageWeeks";

const CHART_HEIGHT = 140;
const DOT_RADIUS = 2;

type Measure = "cost" | "activities";
type Figures = Pick<UsageDay, "cost" | "tokens" | "activities">;

const MEASURE: Record<Measure, { column: string; empty: string; plot: (figures: Figures) => number; display: (figures: Figures) => string; tick: (value: number) => string; stroke: string }> = {
    cost: { column: "Cost", empty: "No cost in this period", plot: figures => usageMoneyToNumber(figures.cost), display: figures => formatUsageMoney(figures.cost), tick: formatAxisDollars, stroke: "var(--accent-n0-wMain)" },
    activities: { column: "Activities", empty: "No activities in this period", plot: figures => figures.activities, display: figures => formatCount(figures.activities), tick: formatCount, stroke: "var(--secondary-w70)" },
};

interface UsageTrendChartProps {
    title: string;
    measure: Measure;
    daily: readonly UsageDay[];
    window?: UsageWindow;
    today: string;
    showRestOfMonth?: boolean;
}

interface TrendRow {
    key: string;
    value: number | null;
    day?: UsageDay;
    week?: UsageWeek;
}

function TrendDot({ cx, cy, payload, color }: Readonly<{ cx?: number; cy?: number; payload?: TrendRow; color: string }>) {
    if (payload?.value == null) return null;
    return <circle cx={cx} cy={cy} r={DOT_RADIUS} fill={color} fillOpacity={payload?.week && isPartialWeek(payload.week) ? PARTIAL_PERIOD_OPACITY : 1} />;
}

function rowLabel(row: Pick<TrendRow, "day" | "week">, today: string): string {
    if (row.week) return formatWeekRange(row.week);
    return row.day ? formatTooltipDay(row.day.date, today) : "";
}

export function TrendTooltip({ active, payload, today, measure }: Readonly<{ active?: boolean; payload?: ReadonlyArray<{ payload?: unknown }>; today: string; measure: Measure }>) {
    const row = active ? (payload?.[0]?.payload as Pick<TrendRow, "day" | "week"> | undefined) : undefined;
    const figures = row?.week ?? row?.day;
    if (!row || !figures) return null;
    return (
        <div className={TOOLTIP_SURFACE}>
            <div className="font-semibold">{rowLabel(row, today)}</div>
            {measure === "activities" && (
                <div>
                    <span className="font-semibold">{formatCount(figures.activities)}</span> {figures.activities === 1 ? "activity" : "activities"}
                </div>
            )}
            <CostAndTokens cost={measure === "cost" ? figures.cost : undefined} tokens={figures.tokens} />
            {row.week && isPartialWeek(row.week) && <div>{partialWeekNote(row.week, today)}</div>}
        </div>
    );
}

export function UsageTrendChart({ title, measure, daily: sent, window, today, showRestOfMonth = false }: Readonly<UsageTrendChartProps>) {
    const daily = useMemo(() => daysOfWindow(sent, window), [sent, window]);
    const headingId = useId();
    const config = MEASURE[measure];
    const byWeek = daily.length > WEEKLY_AFTER_DAYS && daily.every(day => isUsageMoney(day.cost));
    const rows = useMemo<TrendRow[]>(() => (byWeek ? weeklySpend(daily).map(week => ({ key: week.firstDay, value: config.plot(week), week })) : daily.map(day => ({ key: day.date, value: config.plot(day), day }))), [byWeek, daily, config]);
    const { rows: plotted, firstFutureIndex } = useMemo(() => withRestOfMonth<TrendRow>(rows, window, showRestOfMonth, key => ({ key, value: null })), [rows, window, showRestOfMonth]);
    return (
        <section aria-labelledby={headingId} className="flex min-w-0 flex-col gap-3 rounded-sm border p-4">
            <h3 id={headingId} className="text-sm font-semibold">
                {title}
            </h3>
            {rows.every(row => row.value === 0) ? (
                <p className="flex items-center justify-center text-sm text-(--secondary-text-wMain)" style={{ minHeight: CHART_HEIGHT }}>
                    {config.empty}
                </p>
            ) : (
                <>
                    <ChartDataTable
                        labelledBy={headingId}
                        rowHeader={byWeek ? "Week" : "Day"}
                        columns={[{ key: measure, label: config.column }]}
                        rows={rows.map(row => ({
                            key: row.key,
                            label: row.week && isPartialWeek(row.week) ? `${rowLabel(row, today)} (${partialWeekNote(row.week, today)})` : rowLabel(row, today),
                            values: { [measure]: config.display(row.week ?? (row.day as UsageDay)) },
                        }))}
                    />
                    <div aria-hidden>
                        <ResponsiveContainer width="100%" height={CHART_HEIGHT}>
                            <AreaChart accessibilityLayer={false} data={plotted} margin={{ top: 4, right: 8, bottom: 0, left: -8 }}>
                                <CartesianGrid strokeDasharray="3 3" stroke="var(--secondary-w10)" vertical={false} />
                                <XAxis dataKey="key" tickFormatter={value => formatUsageDay(String(value), today)} tick={<DayAxisTick firstFutureIndex={firstFutureIndex} />} tickLine={false} axisLine={false} minTickGap={32} />
                                <YAxis tickFormatter={value => config.tick(Number(value))} tick={{ fontSize: 11, fill: "var(--secondary-text-wMain)" }} tickLine={false} axisLine={false} width={56} allowDecimals={measure === "cost"} />
                                <Tooltip cursor={<DayLineCursor firstFutureIndex={firstFutureIndex} />} content={<TrendTooltip today={today} measure={measure} />} isAnimationActive={false} />
                                <Area type="linear" dataKey="value" stroke={config.stroke} strokeWidth={2} fill={config.stroke} fillOpacity={0.12} dot={<TrendDot color={config.stroke} />} isAnimationActive={false} />
                            </AreaChart>
                        </ResponsiveContainer>
                    </div>
                </>
            )}
        </section>
    );
}
