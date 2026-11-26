// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import { costWithoutSavings, isSavingAtLeastACent, percentOfBudget } from "./usageSavings";

describe("costWithoutSavings", () => {
    test("adds at the wire's ten-digit scale, losing no fraction of a cent", () => {
        expect(costWithoutSavings("1240.5800000001", "312.4700000009")).toBe("1553.0500000010");
    });

    test("is null rather than throwing for a malformed amount", () => {
        expect(costWithoutSavings("1240.58", "n/a")).toBeNull();
    });
});

describe("percentOfBudget", () => {
    test.each([
        ["87.60", "250.0000000000", 35],
        ["199.99", "250", 79.9],
        ["0", "250", 0],
        ["300", "250", 120],
    ])("%s of %s → %s", (spend, budget, expected) => {
        expect(percentOfBudget(spend, budget)).toBe(expected);
    });

    test("is null for an unlimited, empty or malformed budget, or a malformed spend", () => {
        expect(percentOfBudget("10", null)).toBeNull();
        expect(percentOfBudget("10", "0.0000000000")).toBeNull();
        expect(percentOfBudget("10", "lots")).toBeNull();
        expect(percentOfBudget("ten", "250")).toBeNull();
    });
});

describe("isSavingAtLeastACent", () => {
    test.each([
        ["0.0050000000", true],
        ["312.4700000000", true],
        ["0.0001500000", false],
        ["0.0000000000", false],
        ["n/a", false],
    ])("%s → %s", (avoidedCost, expected) => {
        expect(isSavingAtLeastACent(avoidedCost)).toBe(expected);
    });
});
