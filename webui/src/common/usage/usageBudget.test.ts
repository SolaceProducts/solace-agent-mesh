// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { usageBudget } from "@/testing/data/usage";

import { daysLeftInPeriod, formatCurrentBudget, formatTopUpBreakdown } from "./usageBudget";

describe("daysLeftInPeriod", () => {
    test.each([
        ["2026-10-16T14:00:00.000Z", 16],
        ["2026-10-31T23:00:00.000Z", 1],
        ["2026-11-01T00:00:00.000Z", 0],
        ["2026-11-02T00:00:00.000Z", 0],
    ])("at %s, %i left", (now, expected) => {
        expect(daysLeftInPeriod("2026-11-01T00:00:00.000Z", new Date(now))).toBe(expected);
    });
});

describe("budget labels", () => {
    test("name the current budget, Unlimited when it has no cap", () => {
        expect(formatCurrentBudget(usageBudget({ amount: "200", spend: "10", topUp: "50" }))).toBe("$250");
        expect(formatCurrentBudget(usageBudget({ amount: null, spend: "10" }))).toBe("Unlimited");
    });

    test("break a top-up out of the budget, and say nothing without one", () => {
        expect(formatTopUpBreakdown(usageBudget({ amount: "200", spend: "10", topUp: "50" }))).toBe("$200 + $50 top-up");
        expect(formatTopUpBreakdown(usageBudget({ amount: "200", spend: "10" }))).toBeNull();
        expect(formatTopUpBreakdown(usageBudget({ amount: null, spend: "10" }))).toBeNull();
    });
});
