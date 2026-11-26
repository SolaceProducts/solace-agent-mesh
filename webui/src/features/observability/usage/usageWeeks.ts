// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { UsageDay, UsageMoney, UsageTokens } from "@/api/usage";

import { sumTokens, sumUsageMoney } from "@/utils";

const MS_PER_DAY = 86_400_000;
const DAYS_PER_WEEK = 7;

export const WEEKLY_AFTER_DAYS = 31;

export interface UsageWeek {
    start: string;
    firstDay: string;
    lastDay: string;
    days: number;
    cost: UsageMoney;
    tokens: UsageTokens;
    activities: number;
}

function mondayOf(day: string): string {
    const date = new Date(`${day}T00:00:00.000Z`);
    const daysSinceMonday = (date.getUTCDay() + DAYS_PER_WEEK - 1) % DAYS_PER_WEEK;
    return new Date(date.getTime() - daysSinceMonday * MS_PER_DAY).toISOString().slice(0, 10);
}

export function isPartialWeek(week: UsageWeek): boolean {
    return week.days < DAYS_PER_WEEK;
}

export function weeklySpend(daily: readonly UsageDay[]): UsageWeek[] {
    const byStart = new Map<string, UsageDay[]>();
    for (const day of [...daily].sort((a, b) => a.date.localeCompare(b.date))) {
        const start = mondayOf(day.date);
        byStart.set(start, [...(byStart.get(start) ?? []), day]);
    }
    return [...byStart].map(([start, days]) => ({
        start,
        firstDay: days[0].date,
        lastDay: (days.at(-1) ?? days[0]).date,
        days: days.length,
        cost: sumUsageMoney(days.map(day => day.cost)),
        tokens: sumTokens(days.map(day => day.tokens)),
        activities: days.reduce((sum, day) => sum + day.activities, 0),
    }));
}

export function weeksAsDays(daily: readonly UsageDay[]): UsageDay[] {
    return weeklySpend(daily).map(week => ({ date: week.start, cost: week.cost, tokens: week.tokens, activities: week.activities }));
}

const DAY = new Intl.DateTimeFormat("en-US", { day: "numeric", timeZone: "UTC" });
const MONTH_DAY = new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", timeZone: "UTC" });
const atMidnight = (day: string) => new Date(`${day}T00:00:00.000Z`);

export function formatWeekRange(week: UsageWeek): string {
    const first = MONTH_DAY.format(atMidnight(week.firstDay));
    if (week.firstDay === week.lastDay) return `Week of ${first}`;
    const sameMonth = week.firstDay.slice(0, 7) === week.lastDay.slice(0, 7);
    return `Week of ${first} – ${(sameMonth ? DAY : MONTH_DAY).format(atMidnight(week.lastDay))}`;
}

export function partialWeekNote(week: UsageWeek, today: string): string {
    return `Partial week · ${week.days} of ${DAYS_PER_WEEK} days${week.lastDay === today ? " so far" : ""}`;
}
