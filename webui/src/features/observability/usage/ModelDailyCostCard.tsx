// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useId, useMemo } from "react";
import { Bar, BarChart, CartesianGrid, Cell, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { UsageDay, UsageRow, UsageWindow } from "@/api/usage";

import { ChartDataTable, formatAxisDollars, PARTIAL_PERIOD_OPACITY, usageLoadErrorMessage } from "@/common/usage";

import { Skeleton } from "@/ui/skeleton";
import { TOOLTIP_SURFACE } from "@/ui/tooltip-surface";

import { MessageBanner } from "@/common";
import { cn, formatUsageMoney, isUsageMoney, isZeroUsageMoney, usageMoneyToNumber } from "@/utils";

import { SubjectName } from "./SubjectName";
import { DayAxisTick, DayBandCursor } from "./usageChartDays";
import { stackDailyCost, type StackDay } from "./usageDailyStack";
import { daysOfWindow, withRestOfMonth } from "./usageDays";
import type { UsageDetailTarget } from "./usageDetailTarget";
import { formatTooltipDay, formatUsageDay } from "./usageFormat";
import { displayName, SPENDERS_CARD_MIN_HEIGHT, SPENDERS_SHOWN } from "./usageKinds";
import { OTHER_COLOR, SPENDER_COLORS } from "./usageSpenders";
import { formatWeekRange, isPartialWeek, partialWeekNote, type UsageWeek, WEEKLY_AFTER_DAYS, weeklySpend, weeksAsDays } from "./usageWeeks";

const CHART_HEIGHT = 200;
const OTHER_KEY = "other";
const OTHER_LABEL = "Other";

export interface Series {
    key: string;
    row: UsageRow;
    color: string;
}

type ChartRow = Record<string, number | string> & { date: string };

function seriesKey(index: number): string {
    return `model${index}`;
}

function barLabel(date: string, weeks: ReadonlyMap<string, UsageWeek> | null, today: string): string {
    const week = weeks?.get(date);
    if (!week) return formatTooltipDay(date, today);
    return isPartialWeek(week) ? `${formatWeekRange(week)} (${partialWeekNote(week, today)})` : formatWeekRange(week);
}

export function DailyByModelTooltip({
    active,
    payload,
    series,
    days,
    weeks = null,
    today,
}: Readonly<{ active?: boolean; payload?: ReadonlyArray<{ payload?: unknown }>; series: readonly Series[]; days: ReadonlyMap<string, StackDay>; weeks?: ReadonlyMap<string, UsageWeek> | null; today: string }>) {
    const date = active ? (payload?.[0]?.payload as ChartRow | undefined)?.date : undefined;
    const day = date ? days.get(date) : undefined;
    if (!day) return null;
    const lines = [...series.map(({ key, row, color }) => ({ key, label: displayName("models", row), color, cost: day.costs[key] })), { key: OTHER_KEY, label: OTHER_LABEL, color: OTHER_COLOR, cost: day.other }];
    return (
        <div className={cn("flex flex-col gap-1", TOOLTIP_SURFACE)}>
            <div className="font-semibold">
                {barLabel(day.date, weeks, today)} · {formatUsageMoney(day.total)}
            </div>
            {lines.map(line => (
                <div key={line.key} className="flex items-center gap-2">
                    <span className="size-2.5 shrink-0 rounded-xs" style={{ backgroundColor: line.color }} aria-hidden />
                    <span className="flex-1">{line.label}</span>
                    <span className="tabular-nums">{formatUsageMoney(line.cost)}</span>
                </div>
            ))}
        </div>
    );
}

interface ModelStack {
    stack: StackDay[] | null;
    weeks: ReadonlyMap<string, UsageWeek> | null;
}

const NOTHING_STACKED: ModelStack = { stack: null, weeks: null };

function stackByModel(daily: readonly UsageDay[], series: readonly Series[]): ModelStack {
    if (series.some(({ row }) => row.daily === null)) return NOTHING_STACKED;
    const readable = [...daily, ...series.flatMap(({ row }) => row.daily ?? [])].every(day => isUsageMoney(day.cost));
    const byWeek = readable && daily.length > WEEKLY_AFTER_DAYS;
    const bucket = (days: readonly UsageDay[]) => (byWeek ? weeksAsDays(days) : days);
    const stack = stackDailyCost(
        bucket(daily),
        series.map(({ key, row }) => ({ key, daily: bucket(row.daily ?? []) }))
    );
    return { stack, weeks: byWeek ? new Map(weeklySpend(daily).map(week => [week.start, week])) : null };
}

function chartRowsOf(stack: readonly StackDay[]): ChartRow[] {
    return stack.map(day => ({ date: day.date, ...Object.fromEntries(Object.entries(day.costs).map(([key, cost]) => [key, usageMoneyToNumber(cost)])), [OTHER_KEY]: usageMoneyToNumber(day.other) }));
}

function barOpacity(date: string, weeks: ReadonlyMap<string, UsageWeek> | null): number {
    const week = weeks?.get(date);
    return week && isPartialWeek(week) ? PARTIAL_PERIOD_OPACITY : 1;
}

interface ModelDailyCostCardProps {
    rows?: readonly UsageRow[];
    daily?: readonly UsageDay[];
    window?: UsageWindow;
    isStale: boolean;
    isLoading: boolean;
    error: Error | null;
    dailyError?: Error | null;
    today: string;
    showRestOfMonth?: boolean;
    onOpen?: (target: UsageDetailTarget) => void;
}

export function ModelDailyCostCard({ rows, daily, window, isStale, isLoading, error, dailyError = null, today, showRestOfMonth = false, onOpen }: Readonly<ModelDailyCostCardProps>) {
    const headingId = useId();
    const series = useMemo<Series[]>(() => (rows ?? []).slice(0, SPENDERS_SHOWN).map((row, index) => ({ key: seriesKey(index), row, color: SPENDER_COLORS[index % SPENDER_COLORS.length] })), [rows]);

    const ready = rows !== undefined && daily !== undefined;
    const { stack, weeks } = useMemo(() => (rows !== undefined && daily !== undefined ? stackByModel(daysOfWindow(daily, window), series) : NOTHING_STACKED), [rows, daily, window, series]);
    const byWeek = weeks !== null;
    const unreadable = ready && stack === null;
    const days = new Map((stack ?? []).map(day => [day.date, day]));
    const { rows: chartRows, firstFutureIndex } = withRestOfMonth(chartRowsOf(stack ?? []), window, showRestOfMonth, (date): ChartRow => ({ date }));
    const isEmpty = stack?.every(day => isZeroUsageMoney(day.total)) ?? false;

    let body;
    let loadError: Error | null = null;
    if (stack === null) loadError = error ?? (daily === undefined ? dailyError : null);
    if (loadError || unreadable) {
        body = <MessageBanner variant="error" message={usageLoadErrorMessage("Couldn't load daily cost by model.", loadError)} />;
    } else if (isLoading || !stack) {
        body = <Skeleton style={{ height: CHART_HEIGHT }} />;
    } else if (isEmpty) {
        body = (
            <p className="flex items-center justify-center text-sm text-(--secondary-text-wMain)" style={{ minHeight: CHART_HEIGHT }}>
                No cost in this period yet
            </p>
        );
    } else {
        body = (
            <div className={cn("transition-opacity", isStale && "opacity-60")} aria-busy={isStale}>
                <ChartDataTable
                    labelledBy={headingId}
                    rowHeader={byWeek ? "Week" : "Day"}
                    columns={[...series.map(({ key, row }) => ({ key, label: displayName("models", row) })), { key: OTHER_KEY, label: OTHER_LABEL }]}
                    rows={stack.map(day => ({ key: day.date, label: barLabel(day.date, weeks, today), values: { ...Object.fromEntries(series.map(({ key }) => [key, formatUsageMoney(day.costs[key])])), [OTHER_KEY]: formatUsageMoney(day.other) } }))}
                />
                <div aria-hidden>
                    <ResponsiveContainer width="100%" height={CHART_HEIGHT}>
                        <BarChart accessibilityLayer={false} data={chartRows} margin={{ top: 4, right: 4, bottom: 0, left: -8 }}>
                            <CartesianGrid strokeDasharray="3 3" stroke="var(--secondary-w10)" vertical={false} />
                            <XAxis
                                dataKey="date"
                                tickFormatter={value => formatUsageDay(weeks?.get(String(value))?.firstDay ?? String(value), today)}
                                tick={<DayAxisTick firstFutureIndex={firstFutureIndex} />}
                                tickLine={false}
                                axisLine={false}
                                minTickGap={24}
                            />
                            <YAxis tickFormatter={value => formatAxisDollars(Number(value))} tick={{ fontSize: 11, fill: "var(--secondary-text-wMain)" }} tickLine={false} axisLine={false} width={56} />
                            <Tooltip cursor={<DayBandCursor firstFutureIndex={firstFutureIndex} />} content={<DailyByModelTooltip series={series} days={days} weeks={weeks} today={today} />} isAnimationActive={false} />
                            {[...series, { key: OTHER_KEY, color: OTHER_COLOR }].map(({ key, color }) => (
                                <Bar key={key} dataKey={key} stackId="cost" fill={color} radius={key === OTHER_KEY ? [2, 2, 0, 0] : undefined} isAnimationActive={false}>
                                    {chartRows.map(row => (
                                        <Cell key={row.date} fill={color} fillOpacity={barOpacity(row.date, weeks)} />
                                    ))}
                                </Bar>
                            ))}
                        </BarChart>
                    </ResponsiveContainer>
                </div>
            </div>
        );
    }

    return (
        <section aria-labelledby={headingId} className="flex min-w-0 flex-col gap-3 rounded-sm border p-4" style={{ minHeight: SPENDERS_CARD_MIN_HEIGHT }}>
            <header className="flex items-baseline justify-between gap-4">
                <h3 id={headingId} className="shrink-0 text-sm font-semibold">
                    {byWeek ? "Weekly Cost by Model" : "Daily Cost by Model"}
                </h3>
                {series.length > 0 && (
                    <ul aria-label="Models shown" className="flex min-w-0 items-center gap-x-4 text-sm">
                        {series.map(({ key, row, color }) => (
                            <li key={key} className="flex min-w-0 items-center gap-1.5">
                                <span className="size-2.5 shrink-0 rounded-xs" style={{ backgroundColor: color }} aria-hidden />
                                <SubjectName kind="models" row={row} onOpen={onOpen} className="min-w-0" />
                            </li>
                        ))}
                        <li className="flex shrink-0 items-center gap-1.5 whitespace-nowrap text-(--secondary-text-wMain)">
                            <span className="size-2.5 shrink-0 rounded-xs" style={{ backgroundColor: OTHER_COLOR }} aria-hidden />
                            {OTHER_LABEL}
                        </li>
                    </ul>
                )}
            </header>
            {body}
        </section>
    );
}
