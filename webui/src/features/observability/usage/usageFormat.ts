// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageTokens, UsageWindow } from "@/api/usage";

import { formatCompactTokens } from "@/common/usage";

import { totalTokens } from "@/utils";

const DAY_LABEL = new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", timeZone: "UTC" });
const WEEKDAY_LABEL = new Intl.DateTimeFormat("en-US", { weekday: "short", month: "short", day: "numeric", timeZone: "UTC" });
const DAY_WITH_YEAR = new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", year: "numeric", timeZone: "UTC" });
const UTC_TIME = new Intl.DateTimeFormat("en-US", { hour: "2-digit", minute: "2-digit", hourCycle: "h23", timeZone: "UTC" });
const MS_PER_DAY = 86_400_000;

export function formatUsageDay(day: string, today: string): string {
    if (day === today) return "Today";
    return DAY_LABEL.format(new Date(`${day}T00:00:00.000Z`));
}

export function formatUsageWindow({ startTime, endTime }: UsageWindow): string | null {
    const start = new Date(startTime);
    const end = new Date(endTime);
    if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime()) || end.getTime() <= start.getTime()) return null;
    const endsPartway = end.getTime() % MS_PER_DAY !== 0;
    const lastDay = endsPartway ? end : new Date(end.getTime() - MS_PER_DAY);
    const dayLabel = start.getUTCFullYear() === lastDay.getUTCFullYear() ? DAY_LABEL : DAY_WITH_YEAR;
    const first = dayLabel.format(start);
    const last = dayLabel.format(lastDay);
    if (!endsPartway) return first === last ? first : `${first} – ${last}`;
    const time = `${UTC_TIME.format(end)} UTC`;
    return first === last ? `${first}, until ${time}` : `${first} – ${last}, ${time}`;
}

export function formatTokenTotal(tokens: UsageTokens): string {
    return `${formatCompactTokens(totalTokens(tokens))} tokens`;
}

export function tokenBreakdownParts(tokens: UsageTokens): Array<{ label: string; value: string }> {
    return [
        { label: "Input", value: formatCompactTokens(tokens.uncachedInput) },
        { label: "Output", value: formatCompactTokens(tokens.output) },
        { label: "Cache read", value: formatCompactTokens(tokens.cacheRead) },
        { label: "Cache write", value: formatCompactTokens(tokens.cacheWrite) },
    ];
}

export function tokenBreakdown(tokens: UsageTokens): string[] {
    return tokenBreakdownParts(tokens).map(({ label, value }) => `${label} ${value}`);
}

export function formatTooltipDay(day: string, today: string): string {
    const label = WEEKDAY_LABEL.format(new Date(`${day}T00:00:00.000Z`));
    return day === today ? `${label} · today, so far` : label;
}
