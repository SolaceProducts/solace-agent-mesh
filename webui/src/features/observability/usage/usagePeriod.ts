// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { USAGE_PERIODS, type UsagePeriod, type UsageWindowParams } from "@/api/usage";

import type { DateRange } from "../dashboardFilters";

export type UsageRange = DateRange<UsagePeriod>;

export const USAGE_PERIOD_PRESETS: ReadonlyArray<{ value: UsagePeriod; label: string }> = [
    { value: "this_month", label: "This month" },
    { value: "last_month", label: "Last month" },
    { value: "last_7_days", label: "Last 7 days" },
    { value: "last_30_days", label: "Last 30 days" },
    { value: "last_90_days", label: "Last 90 days" },
];

export const DEFAULT_USAGE_RANGE: UsageRange = { kind: "preset", preset: "this_month" };

export const USAGE_HISTORY_DAYS = 90;

const CUSTOM_SEPARATOR = "..";
const CALENDAR_DAY = /^\d{4}-\d{2}-\d{2}$/;
const MS_PER_DAY = 86_400_000;

function isCalendarDay(value: string | undefined): value is string {
    if (value === undefined || !CALENDAR_DAY.test(value)) return false;
    const parsed = Date.parse(`${value}T00:00:00.000Z`);
    return !Number.isNaN(parsed) && utcDay(new Date(parsed)) === value;
}

function isUsagePeriod(value: string): value is UsagePeriod {
    return (USAGE_PERIODS as readonly string[]).includes(value);
}

export function utcDay(at: Date): string {
    return at.toISOString().slice(0, 10);
}

function addUtcDays(day: string, days: number): string {
    return utcDay(new Date(Date.parse(`${day}T00:00:00.000Z`) + days * MS_PER_DAY));
}

export function usageHistoryFloor(now: Date = new Date()): string {
    return addUtcDays(utcDay(now), -(USAGE_HISTORY_DAYS - 1));
}

export function parseUsageRange(raw: string | null): UsageRange {
    if (raw === null) return DEFAULT_USAGE_RANGE;
    if (isUsagePeriod(raw)) return { kind: "preset", preset: raw };
    const [from, to, ...rest] = raw.split(CUSTOM_SEPARATOR);
    if (rest.length > 0 || !isCalendarDay(from) || !isCalendarDay(to) || from > to) return DEFAULT_USAGE_RANGE;
    return { kind: "custom", from, to };
}

export function rangeWithinHistory(range: UsageRange, floor: string, today: string): UsageRange {
    if (range.kind === "custom" && (range.from < floor || range.from > today)) return DEFAULT_USAGE_RANGE;
    return range;
}

export function serializeUsageRange(range: UsageRange): string {
    return range.kind === "preset" ? range.preset : `${range.from}${CUSTOM_SEPARATOR}${range.to}`;
}

export function toWindowParams(range: UsageRange): UsageWindowParams {
    if (range.kind === "preset") return { period: range.preset };
    return { startDate: `${range.from}T00:00:00.000Z`, endDate: `${range.to}T00:00:00.000Z` };
}

const PRESET_COMPARISON: Record<UsagePeriod, string> = {
    this_month: "vs. same point last month",
    last_month: "vs. the month before",
    last_7_days: "vs. the previous 7 days",
    last_30_days: "vs. the previous 30 days",
    last_90_days: "vs. the previous 90 days",
};

export function comparisonLabel(range: UsageRange): string {
    return range.kind === "preset" ? PRESET_COMPARISON[range.preset] : "vs. the previous period";
}

export function isThisMonth(range: UsageRange): boolean {
    return range.kind === "preset" && range.preset === "this_month";
}
