// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { describe, expect, test } from "vitest";

import {
    compareUsageMoney,
    formatBudgetMoney,
    formatCostPerActivity,
    formatExactUsageMoney,
    formatUsageMoney,
    formatUsageMoneyWithFloor,
    formatUsagePercent,
    isRoundedUsageMoney,
    isZeroUsageMoney,
    parseUsageMoney,
    remainderUsageMoney,
    sumUsageMoney,
    toUsageMoney,
    usageMoneyToNumber,
} from "./usageMoney";

describe("usage money arithmetic", () => {
    test("round-trips the wire format exactly", () => {
        expect(toUsageMoney(parseUsageMoney("1240.5800000000"))).toBe("1240.5800000000");
        expect(toUsageMoney(parseUsageMoney("-0.0000000001"))).toBe("-0.0000000001");
    });

    test("sums without float drift", () => {
        expect(sumUsageMoney(["0.1000000000", "0.2000000000"])).toBe("0.3000000000");
        expect(sumUsageMoney([])).toBe("0.0000000000");
    });

    test("computes Other as the total minus the listed rows, exactly as the screenshot does", () => {
        expect(remainderUsageMoney("1240.5800000000", ["260.8500000000", "195.4000000000", "162.3000000000", "110.7500000000", "78.6000000000"])).toBe("432.6800000000");
    });

    test("floors Other at zero when the listed rows exceed the total", () => {
        expect(remainderUsageMoney("1.0000000000", ["2.0000000000"])).toBe("0.0000000000");
    });

    test("compares by value, not by string", () => {
        expect(compareUsageMoney("9.0000000000", "10.0000000000")).toBe(-1);
        expect(compareUsageMoney("10.0000000000", "10.0000000000")).toBe(0);
    });

    test("rejects anything that is not a decimal amount", () => {
        expect(() => parseUsageMoney("1e3")).toThrow();
    });

    test("rounds half up past the tenth digit rather than truncating", () => {
        expect(toUsageMoney(parseUsageMoney("0.00000000014"))).toBe("0.0000000001");
        expect(toUsageMoney(parseUsageMoney("0.00000000015"))).toBe("0.0000000002");
        expect(toUsageMoney(parseUsageMoney("-0.00000000015"))).toBe("-0.0000000002");
    });
});

describe("usage money display", () => {
    test("formats cents with grouping", () => {
        expect(formatUsageMoney("1240.5800000000")).toBe("$1,240.58");
        expect(formatUsageMoney("1234567.0000000000")).toBe("$1,234,567.00");
    });

    test("rounds half away from zero on the string", () => {
        expect(formatUsageMoney("0.0050000000")).toBe("$0.01");
        expect(formatUsageMoney("0.0049999999")).toBe("$0.00");
        expect(formatUsageMoney("-1.0050000000")).toBe("-$1.01");
        expect(formatUsageMoney("-0.0010000000")).toBe("$0.00");
    });

    test("keeps three decimals for cost per activity, and a dash when there were no activities", () => {
        expect(formatCostPerActivity("0.2031000000")).toBe("$0.203");
        expect(formatCostPerActivity("0.0992233009")).toBe("$0.099");
        expect(formatCostPerActivity(null)).toBe("—");
    });

    test("a fixed scale keeps its decimals, and an amount too small for it reads as under its smallest step rather than free", () => {
        expect(formatUsageMoneyWithFloor("1240.5800000000", 4)).toBe("$1,240.5800");
        expect(formatUsageMoneyWithFloor("0.0041837000", 4)).toBe("$0.0042");
        expect(formatUsageMoneyWithFloor("0.0000500000", 4)).toBe("$0.0001");
        expect(formatUsageMoneyWithFloor("0.0000499999", 4)).toBe("<$0.0001");
        expect(formatUsageMoneyWithFloor("0.0000000000", 4)).toBe("$0.0000");
        expect(formatUsageMoneyWithFloor("NaN", 4)).toBe("—");
    });

    test("tells whether a scale drops digits, and gives every digit the amount carries", () => {
        expect(isRoundedUsageMoney("0.0041837000", 4)).toBe(true);
        expect(isRoundedUsageMoney("12.4000000000", 4)).toBe(false);
        expect(isRoundedUsageMoney("NaN", 4)).toBe(false);
        expect(formatExactUsageMoney("0.0000523417")).toBe("$0.0000523417");
        expect(formatExactUsageMoney("1240.4000000000")).toBe("$1,240.40");
        expect(formatExactUsageMoney("NaN")).toBe("—");
    });

    test("never calls a malformed amount zero", () => {
        expect(isZeroUsageMoney("NaN")).toBe(false);
        expect(isZeroUsageMoney("0.0000000000")).toBe(true);
    });

    test("shows a dash for a malformed amount instead of throwing during render", () => {
        expect(formatUsageMoney("NaN")).toBe("—");
        expect(formatCostPerActivity("1e3")).toBe("—");
    });

    test("clamps the decimals to what the wire carries", () => {
        expect(formatUsageMoney("1.2345678901", 12)).toBe("$1.2345678901");
        expect(formatUsageMoney("1.5000000000", -1)).toBe("$2");
    });

    test("drops zero cents from budget figures", () => {
        expect(formatBudgetMoney("450.0000000000")).toBe("$450");
        expect(formatBudgetMoney("2400.5000000000")).toBe("$2,400.50");
    });

    test("formats unrounded percentages to one decimal, truncated so a near-threshold figure never reads as the threshold", () => {
        expect(formatUsagePercent(79.96)).toBe("79.9%");
        expect(formatUsagePercent(91.62222222222223)).toBe("91.6%");
        expect(formatUsagePercent(36)).toBe("36.0%");
        expect(formatUsagePercent(0)).toBe("0.0%");
    });

    test("does not lose a tenth to float error on figures that are exact in decimal", () => {
        expect(formatUsagePercent(104.4)).toBe("104.4%");
        expect(formatUsagePercent(0.29)).toBe("0.2%");
        expect(formatUsagePercent(57.3)).toBe("57.3%");
    });

    test("truncates negative changes toward zero", () => {
        expect(formatUsagePercent(-17.36)).toBe("-17.3%");
        expect(formatUsagePercent(-0.04)).toBe("0.0%");
    });

    test("reads a quotient that float division leaves just under a tenth as that tenth, as the server does", () => {
        expect(formatUsagePercent(79.99999999999999)).toBe("80.0%");
        expect(formatUsagePercent(-79.99999999999999)).toBe("-80.0%");
        expect(formatUsagePercent(0.57 * 100)).toBe("57.0%");
        expect(formatUsagePercent(79.9999)).toBe("79.9%");
    });
});

describe("usageMoneyToNumber", () => {
    test("gives chart coordinates, and plots a malformed amount as 0 rather than NaN", () => {
        expect(usageMoneyToNumber("1240.5800000000")).toBe(1240.58);
        expect(usageMoneyToNumber("1.5e3")).toBe(0);
        expect(usageMoneyToNumber("")).toBe(0);
    });
});
