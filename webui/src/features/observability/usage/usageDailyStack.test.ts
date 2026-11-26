// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import type { UsageDay, UsageMoney } from "@/api/usage";

import { NO_TOKENS } from "@/utils";

import { stackDailyCost } from "./usageDailyStack";

const day = (date: string, cost: UsageMoney): UsageDay => ({ date, cost, tokens: NO_TOKENS, activities: 0 });

describe("stackDailyCost", () => {
    test("splits each day into the listed series and the remainder, in exact money", () => {
        const stack = stackDailyCost(
            [day("2026-10-01", "10.0000000001"), day("2026-10-02", "5.0000000000")],
            [
                { key: "a", daily: [day("2026-10-01", "4.0000000000"), day("2026-10-02", "1.25")] },
                { key: "b", daily: [day("2026-10-01", "3.0000000001")] },
            ]
        );
        expect(stack).toEqual([
            { date: "2026-10-01", costs: { a: "4.0000000000", b: "3.0000000001" }, other: "3.0000000000", total: "10.0000000001" },
            { date: "2026-10-02", costs: { a: "1.25", b: "0.0000000000" }, other: "3.7500000000", total: "5.0000000000" },
        ]);
    });

    test("floors the remainder at zero when the series pass the day's total", () => {
        expect(stackDailyCost([day("2026-10-01", "1.0000000000")], [{ key: "a", daily: [day("2026-10-01", "2.0000000000")] }])?.[0].other).toBe("0.0000000000");
    });

    test("is null for a malformed amount rather than throwing", () => {
        expect(stackDailyCost([day("2026-10-01", "1.0000000000")], [{ key: "a", daily: [day("2026-10-01", "1.5e3")] }])).toBeNull();
    });
});
