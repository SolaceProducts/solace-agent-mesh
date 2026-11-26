// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageDay, UsageWindow } from "@/api/usage";

import { NO_TOKENS } from "@/utils";

const MS_PER_DAY = 86_400_000;
const MAX_WINDOW_DAYS = 400;

export function windowDays(window: UsageWindow): string[] {
    const start = Date.parse(window.startTime);
    const end = Date.parse(window.endTime);
    if (Number.isNaN(start) || Number.isNaN(end) || end <= start) return [];
    const first = start - (start % MS_PER_DAY);
    const count = Math.min(Math.ceil((end - first) / MS_PER_DAY), MAX_WINDOW_DAYS);
    return Array.from({ length: count }, (_, index) => new Date(first + index * MS_PER_DAY).toISOString().slice(0, 10));
}

export function restOfMonth(window: UsageWindow | undefined): string[] {
    const days = window ? windowDays(window) : [];
    const last = days.at(-1);
    if (last === undefined) return [];
    const [year, month] = days[0].split("-").map(Number);
    const startTime = new Date(Date.parse(`${last}T00:00:00.000Z`) + MS_PER_DAY).toISOString();
    const endTime = new Date(Date.UTC(year, month, 1)).toISOString();
    return windowDays({ startTime, endTime } as UsageWindow);
}

export function withRestOfMonth<R>(rows: readonly R[], window: UsageWindow | undefined, show: boolean, futureRow: (day: string) => R): { rows: R[]; firstFutureIndex: number } {
    const future = show ? restOfMonth(window).map(futureRow) : [];
    return { rows: [...rows, ...future], firstFutureIndex: rows.length };
}

export function daysOfWindow(daily: readonly UsageDay[], window: UsageWindow | undefined): UsageDay[] {
    const days = window ? windowDays(window) : [];
    if (days.length === 0) return [...daily];
    const sent = new Map(daily.map(day => [day.date, day]));
    return days.map(date => sent.get(date) ?? { date, cost: "0.0000000000", tokens: NO_TOKENS, activities: 0 });
}
