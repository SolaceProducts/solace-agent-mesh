// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { useId, useMemo } from "react";
import { Bar, BarChart, CartesianGrid, Cell, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";

import type { UsageDay, UsageTokens, UsageWindow } from "@/api/usage";

import { ChartDataTable, formatAxisDollars, PARTIAL_PERIOD_OPACITY } from "@/common/usage";

import { TOOLTIP_SURFACE } from "@/ui/tooltip-surface";

import { formatUsageMoney, isUsageMoney, isZeroUsageMoney, usageMoneyToNumber } from "@/utils";

import { formatCount } from "../feedbackFormat";

import { CostAndTokens } from "./CostAndTokens";
import { DayAxisTick, DayBandCursor } from "./usageChartDays";
import { daysOfWindow, withRestOfMonth } from "./usageDays";
import { formatTokenTotal, formatTooltipDay, formatUsageDay, tokenBreakdown } from "./usageFormat";
import { formatWeekRange, isPartialWeek, partialWeekNote, type UsageWeek, WEEKLY_AFTER_DAYS, weeklySpend } from "./usageWeeks";

const CHART_HEIGHT = 180;
const BAR_COLOR = "var(--primary-wMain)";
const COLUMNS = [
    { key: "cost", label: "Cost" },
    { key: "tokens", label: "Tokens" },
    { key: "activities", label: "Activities" },
];

const tokensCell = (tokens: UsageTokens) => `${formatTokenTotal(tokens)} (${tokenBreakdown(tokens).join(", ")})`;

interface DayRow {
    key: string;
    cost: number;
    day: UsageDay;
}

interface WeekRow {
    key: string;
    cost: number;
    week: UsageWeek;
}

interface FutureRow {
    key: string;
}

export function DailyCostTooltip({ active, payload, today }: Readonly<{ active?: boolean; payload?: ReadonlyArray<{ payload?: unknown }>; today: string }>) {
    const row = active ? (payload?.[0]?.payload as Partial<DayRow> | undefined) : undefined;
    if (!row?.day) return null;
    return (
        <div className={TOOLTIP_SURFACE}>
            <div className="font-semibold">{formatTooltipDay(row.day.date, today)}</div>
            <CostAndTokens cost={row.day.cost} tokens={row.day.tokens} />
        </div>
    );
}

export function WeeklyCostTooltip({ active, payload, today }: Readonly<{ active?: boolean; payload?: ReadonlyArray<{ payload?: unknown }>; today: string }>) {
    const row = active ? (payload?.[0]?.payload as Partial<WeekRow> | undefined) : undefined;
    if (!row?.week) return null;
    return (
        <div className={TOOLTIP_SURFACE}>
            <div className="font-semibold">{formatWeekRange(row.week)}</div>
            <CostAndTokens cost={row.week.cost} tokens={row.week.tokens} />
            {isPartialWeek(row.week) && <div>{partialWeekNote(row.week, today)}</div>}
        </div>
    );
}

interface TokenCostChartProps {
    daily: readonly UsageDay[];
    window?: UsageWindow;
    today: string;
    showRestOfMonth?: boolean;
}

export function TokenCostChart({ daily: sent, window, today, showRestOfMonth = false }: Readonly<TokenCostChartProps>) {
    const daily = useMemo(() => daysOfWindow(sent, window), [sent, window]);
    const byWeek = daily.length > WEEKLY_AFTER_DAYS && daily.every(day => isUsageMoney(day.cost));
    const weeks = useMemo(() => (byWeek ? weeklySpend(daily) : []), [byWeek, daily]);
    const { rows, firstFutureIndex } = useMemo(() => {
        const plotted: Array<DayRow | WeekRow | FutureRow> = byWeek ? weeks.map(week => ({ key: week.firstDay, cost: usageMoneyToNumber(week.cost), week })) : daily.map(day => ({ key: day.date, cost: usageMoneyToNumber(day.cost), day }));
        return withRestOfMonth(plotted, window, showRestOfMonth, (key): FutureRow => ({ key }));
    }, [byWeek, weeks, daily, window, showRestOfMonth]);
    const isEmpty = daily.every(day => isZeroUsageMoney(day.cost));
    const headingId = useId();

    const tableRows = byWeek
        ? weeks.map(week => ({
              key: week.firstDay,
              label: isPartialWeek(week) ? `${formatWeekRange(week)} (${partialWeekNote(week, today)})` : formatWeekRange(week),
              values: { cost: formatUsageMoney(week.cost), tokens: tokensCell(week.tokens), activities: formatCount(week.activities) },
          }))
        : daily.map(day => ({ key: day.date, label: formatTooltipDay(day.date, today), values: { cost: formatUsageMoney(day.cost), tokens: tokensCell(day.tokens), activities: formatCount(day.activities) } }));

    return (
        <section aria-labelledby={headingId} className="flex min-w-0 flex-col gap-3 rounded-sm border p-4">
            <h3 id={headingId} className="text-sm font-semibold">
                {byWeek ? "Weekly Token Cost" : "Daily Token Cost"}
            </h3>
            {isEmpty ? (
                <p className="flex flex-1 items-center justify-center text-sm text-(--secondary-text-wMain)" style={{ minHeight: CHART_HEIGHT }}>
                    No cost in this period yet
                </p>
            ) : (
                <>
                    <ChartDataTable labelledBy={headingId} rowHeader={byWeek ? "Week" : "Day"} columns={COLUMNS} rows={tableRows} />
                    <div aria-hidden>
                        <ResponsiveContainer key={byWeek ? "weeks" : "days"} width="100%" height={CHART_HEIGHT}>
                            <BarChart accessibilityLayer={false} data={rows} margin={{ top: 4, right: 4, bottom: 0, left: -8 }}>
                                <CartesianGrid strokeDasharray="3 3" stroke="var(--secondary-w10)" vertical={false} />
                                <XAxis dataKey="key" tickFormatter={value => formatUsageDay(String(value), today)} tick={<DayAxisTick firstFutureIndex={firstFutureIndex} />} tickLine={false} axisLine={false} minTickGap={24} />
                                <YAxis tickFormatter={value => formatAxisDollars(Number(value))} tick={{ fontSize: 11, fill: "var(--secondary-text-wMain)" }} tickLine={false} axisLine={false} width={56} />
                                <Tooltip cursor={<DayBandCursor firstFutureIndex={firstFutureIndex} />} content={byWeek ? <WeeklyCostTooltip today={today} /> : <DailyCostTooltip today={today} />} isAnimationActive={false} />
                                <Bar dataKey="cost" fill={BAR_COLOR} radius={[2, 2, 0, 0]} isAnimationActive={false}>
                                    {weeks.map(week => (
                                        <Cell key={week.firstDay} fill={BAR_COLOR} fillOpacity={isPartialWeek(week) ? PARTIAL_PERIOD_OPACITY : 1} />
                                    ))}
                                </Bar>
                            </BarChart>
                        </ResponsiveContainer>
                    </div>
                </>
            )}
        </section>
    );
}
