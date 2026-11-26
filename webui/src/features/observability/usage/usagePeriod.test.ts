// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { comparisonLabel, isThisMonth, parseUsageRange, rangeWithinHistory, serializeUsageRange, toWindowParams, usageHistoryFloor } from "./usagePeriod";

describe("usage period", () => {
    test("opens on This month with no param, and on an unknown or malformed one", () => {
        for (const raw of [null, "yesterday", "2026-10-05..2026-10-01", "2026-10-01..x", "a..b..c"]) {
            expect(parseUsageRange(raw)).toEqual({ kind: "preset", preset: "this_month" });
        }
    });

    test("round-trips presets and custom ranges through the URL", () => {
        for (const raw of ["last_7_days", "2026-09-01..2026-09-30"]) {
            expect(serializeUsageRange(parseUsageRange(raw))).toBe(raw);
        }
    });

    test("sends a preset as period alone", () => {
        expect(toWindowParams({ kind: "preset", preset: "last_90_days" })).toEqual({ period: "last_90_days" });
    });

    test("sends a custom range's first and last days, which the server includes both of", () => {
        expect(toWindowParams({ kind: "custom", from: "2026-09-01", to: "2026-09-30" })).toEqual({ startDate: "2026-09-01T00:00:00.000Z", endDate: "2026-09-30T00:00:00.000Z" });
    });

    test("offers 90 retained days, today included", () => {
        expect(usageHistoryFloor(new Date("2026-10-16T23:59:00Z"))).toBe("2026-07-19");
    });

    test("words the comparison for the period it compares against", () => {
        expect(comparisonLabel({ kind: "preset", preset: "this_month" })).toBe("vs. same point last month");
        expect(comparisonLabel({ kind: "preset", preset: "last_30_days" })).toBe("vs. the previous 30 days");
        expect(comparisonLabel({ kind: "custom", from: "2026-09-01", to: "2026-09-10" })).toBe("vs. the previous period");
    });

    test("only This month counts as this month", () => {
        expect(isThisMonth({ kind: "preset", preset: "this_month" })).toBe(true);
        expect(isThisMonth({ kind: "preset", preset: "last_month" })).toBe(false);
        expect(isThisMonth({ kind: "custom", from: "2026-10-01", to: "2026-10-16" })).toBe(false);
    });

    test("opens a linked range the server would refuse on This month instead", () => {
        const floor = "2026-07-20";
        const today = "2026-10-17";
        expect(rangeWithinHistory({ kind: "custom", from: "2026-07-19", to: "2026-07-31" }, floor, today)).toEqual({ kind: "preset", preset: "this_month" });
        expect(rangeWithinHistory({ kind: "custom", from: "2026-10-18", to: "2026-10-20" }, floor, today)).toEqual({ kind: "preset", preset: "this_month" });
        expect(rangeWithinHistory({ kind: "custom", from: "2026-07-20", to: "2026-10-30" }, floor, today)).toEqual({ kind: "custom", from: "2026-07-20", to: "2026-10-30" });
        expect(rangeWithinHistory({ kind: "preset", preset: "last_90_days" }, floor, today)).toEqual({ kind: "preset", preset: "last_90_days" });
    });
});
