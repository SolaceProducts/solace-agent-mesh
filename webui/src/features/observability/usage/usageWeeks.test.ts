// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { UsageMoney } from "@/api/usage";

import { NO_TOKENS } from "@/utils";

import { formatWeekRange, isPartialWeek, partialWeekNote, weeklySpend, weeksAsDays } from "./usageWeeks";

const day = (date: string, cost: UsageMoney, activities = 1) => ({ date, cost, tokens: tokensOf(activities), activities });

function tokensOf(activities: number) {
    return { ...NO_TOKENS, uncachedInput: activities * 100, output: activities };
}

describe("weeklySpend", () => {
    test("groups the days into Monday-to-Sunday weeks, counting the days each has in the window", () => {
        const daily = [day("2026-10-04", "1.0000000000"), day("2026-10-05", "0.1000000000", 2), day("2026-10-11", "0.2000000000", 3), day("2026-10-12", "4.0000000000")];
        const weeks = weeklySpend(daily);
        expect(weeks).toEqual([
            { start: "2026-09-28", firstDay: "2026-10-04", lastDay: "2026-10-04", days: 1, cost: "1.0000000000", tokens: tokensOf(1), activities: 1 },
            { start: "2026-10-05", firstDay: "2026-10-05", lastDay: "2026-10-11", days: 2, cost: "0.3000000000", tokens: tokensOf(5), activities: 5 },
            { start: "2026-10-12", firstDay: "2026-10-12", lastDay: "2026-10-12", days: 1, cost: "4.0000000000", tokens: tokensOf(1), activities: 1 },
        ]);
    });

    test("reads a week with all seven days in the window as whole", () => {
        const fullWeek = Array.from({ length: 7 }, (_, index) => day(`2026-10-${String(5 + index).padStart(2, "0")}`, "1.0000000000"));
        const [week] = weeklySpend(fullWeek);
        expect(week.cost).toBe("7.0000000000");
        expect(isPartialWeek(week)).toBe(false);
        expect(isPartialWeek(weeklySpend(fullWeek.slice(1))[0])).toBe(true);
    });

    test("orders the weeks by date whatever order the days come in", () => {
        expect(weeklySpend([day("2026-10-12", "1.0000000000"), day("2026-10-05", "1.0000000000")]).map(week => week.start)).toEqual(["2026-10-05", "2026-10-12"]);
    });
});

describe("weeksAsDays", () => {
    test("dates each week by its Monday, so a series missing the window's first days still lines up", () => {
        expect(weeksAsDays([day("2026-10-07", "1.0000000000"), day("2026-10-08", "2.0000000000"), day("2026-10-13", "4.0000000000")])).toEqual([
            { date: "2026-10-05", cost: "3.0000000000", tokens: tokensOf(2), activities: 2 },
            { date: "2026-10-12", cost: "4.0000000000", tokens: tokensOf(1), activities: 1 },
        ]);
    });
});

describe("week wording", () => {
    const week = (firstDay: string, lastDay: string, days: number) => ({ start: firstDay, firstDay, lastDay, days, cost: "1.0000000000", tokens: tokensOf(1), activities: 1 }) as const;

    test("names the week's days in the period, both months when it crosses one, and a lone day alone", () => {
        expect(formatWeekRange(week("2026-07-20", "2026-07-26", 7))).toBe("Week of Jul 20 – 26");
        expect(formatWeekRange(week("2026-08-31", "2026-09-06", 7))).toBe("Week of Aug 31 – Sep 6");
        expect(formatWeekRange(week("2026-07-19", "2026-07-19", 1))).toBe("Week of Jul 19");
    });

    test("says how much of a partial week is counted, and so far for the week still running", () => {
        expect(partialWeekNote(week("2026-10-12", "2026-10-16", 5), "2026-10-20")).toBe("Partial week · 5 of 7 days");
        expect(partialWeekNote(week("2026-10-19", "2026-10-20", 2), "2026-10-20")).toBe("Partial week · 2 of 7 days so far");
    });
});
