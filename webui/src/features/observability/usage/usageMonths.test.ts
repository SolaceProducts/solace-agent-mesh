// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { UsageMoney } from "@/api/usage";

import { usageBudget } from "@/testing/data/usage";
import { NO_TOKENS } from "@/utils";

import { budgetMonthsOfPeriod, monthsOfPeriod, partialMonthNote, reachesToday } from "./usageMonths";

const day = (date: string, cost: UsageMoney) => ({ date, cost, tokens: NO_TOKENS, activities: 1 });
const run = (from: string, to: string, cost: UsageMoney) => {
    const days = [];
    for (let at = Date.parse(`${from}T00:00:00.000Z`); at <= Date.parse(`${to}T00:00:00.000Z`); at += 86_400_000) days.push(day(new Date(at).toISOString().slice(0, 10), cost));
    return days;
};

describe("monthsOfPeriod", () => {
    test("gives every month the days touch, oldest first, with its spend in the period", () => {
        const months = monthsOfPeriod([...run("2026-07-22", "2026-07-31", "1.0000000000"), ...run("2026-08-01", "2026-08-31", "0.1000000000"), day("2026-09-01", "7.0000000000")], "2026-09-01");
        expect(months.map(({ month, cost }) => ({ month, cost }))).toEqual([
            { month: "2026-07", cost: "10.0000000000" },
            { month: "2026-08", cost: "3.1000000000" },
            { month: "2026-09", cost: "7.0000000000" },
        ]);
    });

    test("marks a month the period cuts short as partial, but not this month, whose days so far are all it has", () => {
        const [july, august, september] = monthsOfPeriod([...run("2026-07-22", "2026-07-31", "1.0000000000"), ...run("2026-08-01", "2026-08-31", "1.0000000000"), day("2026-09-01", "1.0000000000")], "2026-09-01");
        expect(july).toMatchObject({ days: 10, daysInMonth: 31, partial: true });
        expect(partialMonthNote(july)).toBe("Partial month · 10 of 31 days");
        expect(august.partial).toBe(false);
        expect(september).toMatchObject({ days: 1, daysInMonth: 30, partial: false });
    });

    test("marks a month a past period ends inside as partial", () => {
        const [, august] = monthsOfPeriod([...run("2026-07-01", "2026-07-31", "1.0000000000"), ...run("2026-08-01", "2026-08-20", "1.0000000000")], "2026-10-20");
        expect(august).toMatchObject({ days: 20, partial: true });
    });

    test("marks this month partial when the period ends before today", () => {
        const months = monthsOfPeriod([...run("2026-08-15", "2026-09-30", "1.0000000000"), ...run("2026-10-01", "2026-10-05", "1.0000000000")], "2026-10-21");
        expect(months.at(-1)).toMatchObject({ month: "2026-10", days: 5, partial: true });
        expect(reachesToday(months, "2026-10-21")).toBe(false);
        expect(reachesToday(monthsOfPeriod(run("2026-09-01", "2026-10-21", "1.0000000000"), "2026-10-21"), "2026-10-21")).toBe(true);
    });

    test("counts February's days by its year", () => {
        expect(monthsOfPeriod(run("2028-02-01", "2028-02-29", "1.0000000000"), "2028-04-01")[0]).toMatchObject({ daysInMonth: 29, partial: false });
    });
});

describe("budgetMonthsOfPeriod", () => {
    const days = [...run("2026-08-20", "2026-08-31", "1.0000000000"), ...run("2026-09-01", "2026-09-30", "1.0000000000"), day("2026-10-01", "1.0000000000")];

    test("gives this month the budget with its top-up and earlier months the base, as budgets keep no history", () => {
        const months = budgetMonthsOfPeriod(days, "2026-10-01", usageBudget({ amount: "200", spend: "1", topUp: "50" }));
        expect(months.map(({ month, budgetAmount }) => ({ month, budgetAmount }))).toEqual([
            { month: "2026-08", budgetAmount: "200.0000000000" },
            { month: "2026-09", budgetAmount: "200.0000000000" },
            { month: "2026-10", budgetAmount: "250.0000000000" },
        ]);
    });

    test("says how much of a month the period cuts short is counted", () => {
        expect(budgetMonthsOfPeriod(days, "2026-10-01", null).map(month => month.partialNote)).toEqual(["Partial month · 12 of 31 days", null, null]);
    });

    test("carries no budget without one", () => {
        expect(budgetMonthsOfPeriod(days, "2026-10-01", null).every(month => month.budgetAmount === null)).toBe(true);
    });
});
