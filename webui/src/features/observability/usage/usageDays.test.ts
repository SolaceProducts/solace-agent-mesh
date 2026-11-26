// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { UsageWindow } from "@/api/usage";

import { NO_TOKENS } from "@/utils";

import { daysOfWindow, restOfMonth, windowDays } from "./usageDays";

const unreadable = (startTime: string, endTime: string) => ({ startTime, endTime }) as unknown as UsageWindow;

describe("windowDays", () => {
    test("covers each day from the start to the exclusive end, a part day included", () => {
        expect(windowDays({ startTime: "2026-10-14T00:00:00.000Z", endTime: "2026-10-20T14:05:00.000Z" })).toEqual(["2026-10-14", "2026-10-15", "2026-10-16", "2026-10-17", "2026-10-18", "2026-10-19", "2026-10-20"]);
    });

    test("stops before an end at midnight", () => {
        expect(windowDays({ startTime: "2026-09-01T00:00:00.000Z", endTime: "2026-10-01T00:00:00.000Z" })).toHaveLength(30);
    });

    test("has no days for a window it cannot read", () => {
        expect(windowDays(unreadable("soon", "2026-10-01T00:00:00.000Z"))).toEqual([]);
        expect(windowDays({ startTime: "2026-10-02T00:00:00.000Z", endTime: "2026-10-01T00:00:00.000Z" })).toEqual([]);
    });
});

describe("daysOfWindow", () => {
    const window = { startTime: "2026-10-14T00:00:00.000Z", endTime: "2026-10-17T00:00:00.000Z" } as const;

    test("gives every day of the window, zero where the server sent none, and drops days outside it", () => {
        const daily = [
            { date: "2026-10-13", cost: "9.0000000000", tokens: NO_TOKENS, activities: 9 },
            { date: "2026-10-15", cost: "2.0000000000", tokens: NO_TOKENS, activities: 3 },
        ] as const;
        expect(daysOfWindow(daily, window)).toEqual([
            { date: "2026-10-14", cost: "0.0000000000", tokens: NO_TOKENS, activities: 0 },
            { date: "2026-10-15", cost: "2.0000000000", tokens: NO_TOKENS, activities: 3 },
            { date: "2026-10-16", cost: "0.0000000000", tokens: NO_TOKENS, activities: 0 },
        ]);
    });

    test("keeps the days as sent without a window it can read", () => {
        const daily = [{ date: "2026-10-15", cost: "2.0000000000", tokens: NO_TOKENS, activities: 3 }] as const;
        expect(daysOfWindow(daily, undefined)).toEqual(daily);
        expect(daysOfWindow(daily, unreadable("?", "?"))).toEqual(daily);
    });
});

describe("restOfMonth", () => {
    test.each([
        ["a month part way through, to its last day", "2026-11-01T00:00:00.000Z", "2026-11-02T14:05:00.000Z", 28, "2026-11-03", "2026-11-30"],
        ["a leap February", "2028-02-01T00:00:00.000Z", "2028-02-27T09:00:00.000Z", 2, "2028-02-28", "2028-02-29"],
    ])("gives the days after the window's last through its month's end: %s", (_what, startTime, endTime, count, first, last) => {
        const rest = restOfMonth({ startTime, endTime } as UsageWindow);
        expect(rest).toHaveLength(count);
        expect([rest[0], rest.at(-1)]).toEqual([first, last]);
    });

    test("gives none once the window reaches its month's last day", () => {
        expect(restOfMonth({ startTime: "2026-11-01T00:00:00.000Z", endTime: "2026-11-30T23:00:00.000Z" })).toEqual([]);
    });
});
